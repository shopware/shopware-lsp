package admin

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/shopware/shopware-lsp/internal/indexer"
	javascriptparser "github.com/shopware/shopware-lsp/internal/parser/javascript"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestJavaScriptShopwareContextMemberTracksNestedReceivers(t *testing.T) {
	source := `
Shopware.Context.api.languageId;
Shopware.Context.app.config.version;
Shopware.Context.api.;
other.Context.api.languageId;
`
	root := javascriptparser.Parse(source).Tree.Root
	tests := []struct {
		needle   string
		receiver []string
		member   string
		matched  bool
	}{
		{"languageId", []string{"api"}, "languageId", true},
		{"version", []string{"app", "config"}, "version", true},
		{"api.;", []string{"api"}, "", true},
		{"other.Context", nil, "", false},
	}
	for _, test := range tests {
		t.Run(test.needle, func(t *testing.T) {
			offset := strings.Index(source, test.needle)
			require.NotEqual(t, -1, offset)
			if test.needle == "api.;" {
				offset += len("api.") - 1
			} else {
				offset++
			}
			receiver, member, matched := JavaScriptShopwareContextMember(
				root.NodeAtOffset(uint32(offset)),
			)
			assert.Equal(t, test.matched, matched)
			assert.Equal(t, test.receiver, receiver)
			assert.Equal(t, test.member, member)
		})
	}
}

func TestResolveShopwareContextUsesIndexedContextState(t *testing.T) {
	root := t.TempDir()
	idx, err := NewAdminComponentIndexer(filepath.Join(root, "cache"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, idx.Close()) })
	adminRoot := filepath.Join(
		root, "src/Administration/Resources/app/administration/src",
	)
	contextPath := filepath.Join(adminRoot, "app/composables/use-context.ts")
	consumerPath := filepath.Join(adminRoot, "module/example/index.ts")
	require.NoError(t, idx.Index(indexer.NewParsedFile(
		contextPath,
		[]byte(`export interface ContextState {
    app: {
        config: { version: null | string; };
        environment: null | 'development' | 'production';
    };
    api: {
        languageId: null | string;
        versionId: null | string;
    };
}`),
	)))

	rootShape, err := idx.ResolveShopwareContext("", consumerPath)
	require.NoError(t, err)
	assert.False(t, rootShape.Complete)
	assert.ElementsMatch(t, []string{"app", "api"}, vueMemberNames(rootShape.Members))

	apiShape, err := idx.ResolveShopwareContext("api", consumerPath)
	require.NoError(t, err)
	assert.True(t, apiShape.Complete)
	assert.ElementsMatch(
		t, []string{"languageId", "versionId"}, vueMemberNames(apiShape.Members),
	)
	languageID, found := twigVueMemberNamed(apiShape.Members, "languageId")
	require.True(t, found)
	assert.Equal(t, "null | string", languageID.Type)
	assert.Equal(t, contextPath, languageID.DefinitionPath)
	assert.True(t, languageID.DefinitionRange.Identifier)

	configShape, err := idx.ResolveShopwareContext("app.config", consumerPath)
	require.NoError(t, err)
	assert.True(t, configShape.Complete)
	assert.Equal(t, []string{"version"}, vueMemberNames(configShape.Members))

	resolved, found, err := idx.resolveComponentExpressionType(
		VueComponent{}, "Shopware.Context.api.languageId", consumerPath,
	)
	require.NoError(t, err)
	require.True(t, found)
	assert.Equal(t, "null | string", resolved.Type)
	assert.Equal(t, contextPath, resolved.ContextPath)
}
