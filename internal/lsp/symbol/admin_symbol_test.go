package symbol

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/shopware/shopware-lsp/internal/admin"
	"github.com/shopware/shopware-lsp/internal/indexer"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	"github.com/shopware/shopware-lsp/internal/uriutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAdminWorkspaceSymbolsCoverRuntimeRegistries(t *testing.T) {
	root := t.TempDir()
	adminIndex, err := admin.NewAdminComponentIndexer(filepath.Join(root, "cache"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, adminIndex.Close()) })
	filePath := filepath.Join(
		root,
		"src/Administration/Resources/app/administration/src/main.ts",
	)
	require.NoError(t, adminIndex.Index(indexer.NewParsedFile(
		filePath,
		[]byte(`
Shopware.Application.addServiceProvider('acl', () => createAcl());
Shopware.Store.register('session', {
    state: () => ({ currentUser: null }),
    getters: { locale() { return 'en-GB'; } },
    actions: { login() {} },
});
Shopware.Mixin.register('notification', {});
Shopware.Directive.register('tooltip', {});
Shopware.Filter.register('currency', (value) => value);
Shopware.Component.register('sw-owner', { directives: { hide: {} } });
Shopware.Service('cmsService').registerCmsElement({ name: 'hero' });
Shopware.Service('cmsService').registerCmsBlock({ name: 'hero-grid' });
Shopware.Module.register('sw-catalog', {
    routes: { index: { path: 'index', component: 'sw-catalog-list' } },
});
Shopware.Service('privileges').addPrivilegeMappingEntry({
    key: 'product',
    roles: { viewer: { privileges: ['product:read'] } },
});`),
	)))
	provider := NewAdminWorkspaceSymbolProvider(adminIndex)
	tests := []struct {
		query     string
		name      string
		container string
		kind      protocol.SymbolKind
	}{
		{"acl", "acl", "Administration service", protocol.SymbolObject},
		{"session", "session", "Administration store", protocol.SymbolObject},
		{"currentUser", "currentUser", "store · session", protocol.SymbolField},
		{"locale", "locale", "store · session", protocol.SymbolProperty},
		{"login", "login", "store · session", protocol.SymbolMethod},
		{"notification", "notification", "Administration mixin", protocol.SymbolObject},
		{"tooltip", "v-tooltip", "Vue directive", protocol.SymbolFunction},
		{"currency", "currency", "Administration filter", protocol.SymbolFunction},
		{"hide", "v-hide", "sw-owner · local Vue directive", protocol.SymbolFunction},
		{"hero", "hero", "Shopware CMS element", protocol.SymbolObject},
		{"hero-grid", "hero-grid", "Shopware CMS block", protocol.SymbolObject},
		{"sw-catalog", "sw-catalog", "Administration module", protocol.SymbolModule},
		{"sw.catalog.index", "sw.catalog.index", "sw-catalog", protocol.SymbolFunction},
		{"product.viewer", "product.viewer", "ACL role", protocol.SymbolEnumMember},
		{"product:read", "product:read", "ACL permission", protocol.SymbolConstant},
	}
	for _, test := range tests {
		t.Run(test.query, func(t *testing.T) {
			symbols, symbolErr := provider.WorkspaceSymbols(
				context.Background(), test.query,
			)
			require.NoError(t, symbolErr)
			current := requireSymbol(t, symbols, test.name, test.container)
			assert.Equal(t, test.kind, current.Kind)
			assert.Equal(t, uriutil.FileURI(filePath), current.Location.URI)
		})
	}
}

func TestAdminWorkspaceSymbolsExposeComponentMarkupContracts(t *testing.T) {
	root := t.TempDir()
	adminIndex, err := admin.NewAdminComponentIndexer(filepath.Join(root, "cache"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, adminIndex.Close()) })

	componentDir := filepath.Join(
		root,
		"src/Administration/Resources/app/administration/src/app/component/sw-card",
	)
	require.NoError(t, os.MkdirAll(componentDir, 0o755))
	templatePath := filepath.Join(componentDir, "sw-card.html.twig")
	require.NoError(t, os.WriteFile(templatePath, []byte(`
<slot name="actions" :item="card"></slot>
`), 0o600))
	definitionPath := filepath.Join(componentDir, "index.js")
	require.NoError(t, adminIndex.Index(indexer.NewParsedFile(
		definitionPath,
		[]byte(`
import template from './sw-card.html.twig';

Shopware.Component.register('sw-card', {
    template,
    props: {
        helpText: { type: String },
        modelValue: { type: String },
    },
    emits: ['save', 'update:modelValue'],
});`),
	)))
	provider := NewAdminWorkspaceSymbolProvider(adminIndex)
	tests := []struct {
		query     string
		name      string
		container string
		kind      protocol.SymbolKind
		uri       string
	}{
		{"help-text", "helpText", "sw-card · component prop", protocol.SymbolProperty, uriutil.FileURI(definitionPath)},
		{"@save", "save", "sw-card · component event", protocol.SymbolEvent, uriutil.FileURI(definitionPath)},
		{"v-model", "v-model", "sw-card · component model", protocol.SymbolProperty, uriutil.FileURI(definitionPath)},
		{"#actions", "actions", "sw-card · component slot", protocol.SymbolProperty, uriutil.FileURI(templatePath)},
	}
	for _, test := range tests {
		t.Run(test.query, func(t *testing.T) {
			symbols, symbolErr := provider.WorkspaceSymbols(
				context.Background(), test.query,
			)
			require.NoError(t, symbolErr)
			current := requireSymbol(t, symbols, test.name, test.container)
			assert.Equal(t, test.kind, current.Kind)
			assert.Equal(t, test.uri, current.Location.URI)
		})
	}
}
