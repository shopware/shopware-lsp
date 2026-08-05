package admin

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/shopware/shopware-lsp/internal/indexer"
	javascriptparser "github.com/shopware/shopware-lsp/internal/parser/javascript"
	jsquery "github.com/shopware/shopware-lsp/internal/parser/javascript/query"
	jssyntax "github.com/shopware/shopware-lsp/internal/parser/javascript/syntax"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestJavaScriptApplicationContainerMemberSupportsDirectAndConstAliases(
	t *testing.T,
) {
	source := `
Shopware.Application.getContainer('factory').locale;
function run() {
    const services = Application.getContainer('service');
    services.repositoryFactory;
	if (enabled) {
		const services = Application.getContainer('init');
		services.httpClient;
	}
    services.loginService;
    let mutable = Application.getContainer('factory');
    mutable.module;
}
`
	root := javascriptparser.Parse(source).Tree.Root
	tests := []struct {
		needle    string
		container string
		member    string
		matched   bool
	}{
		{"locale", "factory", "locale", true},
		{"repositoryFactory", "service", "repositoryFactory", true},
		{"httpClient", "init", "httpClient", true},
		{"loginService", "service", "loginService", true},
		{"module", "", "", false},
	}
	for _, test := range tests {
		t.Run(test.needle, func(t *testing.T) {
			offset := strings.Index(source, test.needle)
			if test.needle == "loginService" {
				offset = strings.LastIndex(source, test.needle)
			}
			require.NotEqual(t, -1, offset)
			node := root.NodeAtOffset(uint32(offset + 1))
			container, member, matched := JavaScriptApplicationContainerMember(node)
			assert.Equal(t, test.matched, matched)
			assert.Equal(t, test.container, container)
			assert.Equal(t, test.member, member)
		})
	}
}

func TestApplicationContainerNameReference(t *testing.T) {
	source := `Application.getContainer('factory'); other.getContainer('service')`
	root := javascriptparser.Parse(source).Tree.Root
	literals := jsquery.Nodes(root, jssyntax.JsString)
	require.Len(t, literals, 2)
	assert.True(t, IsApplicationContainerNameReference(literals[0]))
	assert.False(t, IsApplicationContainerNameReference(literals[1]))
}

func TestResolveApplicationContainerMergesAmbientInterfacesAndRuntimeServices(
	t *testing.T,
) {
	root := t.TempDir()
	idx, err := NewAdminComponentIndexer(filepath.Join(root, "cache"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, idx.Close()) })
	adminRoot := filepath.Join(
		root, "src/Administration/Resources/app/administration/src",
	)
	globalPath := filepath.Join(adminRoot, "global.types.ts")
	extensionPath := filepath.Join(adminRoot, "module/extension.ts")
	registrationPath := filepath.Join(adminRoot, "service.ts")
	for path, source := range map[string]string{
		globalPath: `
export interface SubContainer<T extends string> { $list(): string[]; }
declare global {
    interface ServiceContainer extends SubContainer<'service'> {
        acl: AclService;
    }
    interface ServiceContainer {
        localeHelper: LocaleHelper;
    }
    interface FactoryContainer extends SubContainer<'factory'> {
        locale: LocaleFactory;
    }
}`,
		extensionPath: `
import type { SubContainer } from '../global.types';
declare global {
    interface ServiceContainer extends SubContainer<'service'> {
        extensionSdkService: ExtensionSdkService;
    }
}`,
		registrationPath: `
class RuntimeService {}
Shopware.Application.addServiceProvider('runtimeService', () => new RuntimeService());`,
	} {
		require.NoError(t, idx.Index(indexer.NewParsedFile(path, []byte(source))))
	}

	serviceShape, err := idx.ResolveApplicationContainer("service", extensionPath)
	require.NoError(t, err)
	assert.False(t, serviceShape.Complete)
	serviceMembers := make(map[string]TwigVueMember, len(serviceShape.Members))
	for _, member := range serviceShape.Members {
		serviceMembers[member.Name] = member
	}
	for _, name := range []string{
		"$list", "acl", "localeHelper", "extensionSdkService", "runtimeService",
	} {
		assert.Contains(t, serviceMembers, name)
	}
	assert.Equal(t, "AclService", serviceMembers["acl"].Type)
	assert.Equal(t, registrationPath, serviceMembers["runtimeService"].DefinitionPath)

	factoryShape, err := idx.ResolveApplicationContainer("factory", extensionPath)
	require.NoError(t, err)
	assert.True(t, factoryShape.Complete)
	assert.Contains(t, vueMemberNames(factoryShape.Members), "locale")
}
