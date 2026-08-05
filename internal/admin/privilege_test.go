package admin

import (
	"path/filepath"
	"testing"

	indexerpkg "github.com/shopware/shopware-lsp/internal/indexer"
	"github.com/shopware/shopware-lsp/internal/parser/javascript/syntax"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseAdminPrivilegesFromFluentMappings(t *testing.T) {
	source := `
Shopware.Service('privileges')
    .addPrivilegeMappingEntry({
        key: 'product',
        roles: {
            viewer: {
                privileges: ['product:read'],
                dependencies: [],
            },
        },
    })
    .addPrivilegeMappingEntry({
        key: 'order',
        roles: {
            editor: {
                privileges: ['order:read', 'order:update'],
            },
        },
    });`
	filePath := "/project/Resources/app/administration/src/privileges.ts"
	privileges := parseAdminPrivileges(
		parseJS(t, source), filePath, syntax.NewLineIndex(source),
	)

	byName := make(map[string]AdminPrivilege, len(privileges))
	for _, privilege := range privileges {
		byName[privilege.Name] = privilege
	}
	for _, name := range []string{
		"product.viewer", "product:read", "order.editor",
		"order:read", "order:update",
	} {
		assert.Contains(t, byName, name)
	}
	assert.Equal(t, AdminPrivilegeRole, byName["product.viewer"].Kind)
	assert.Equal(t, "product", byName["product.viewer"].MappingKey)
	assert.Equal(t, "viewer", byName["product.viewer"].Role)
	assert.Equal(t, AdminPrivilegePermission, byName["product:read"].Kind)
	assert.Equal(t, filePath, byName["product:read"].FilePath)
}

func TestAdminPrivilegeIndexReplacesStaleMappings(t *testing.T) {
	root := t.TempDir()
	idx, err := NewAdminComponentIndexer(filepath.Join(root, "cache"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, idx.Close()) })
	filePath := filepath.Join(
		root, "src/Administration/Resources/app/administration/src/privileges.ts",
	)

	require.NoError(t, idx.Index(indexerpkg.NewParsedFile(filePath, []byte(`
Shopware.Service('privileges').addPrivilegeMappingEntry({
    key: 'product',
    roles: { viewer: { privileges: ['product:read'] } },
});`))))
	values, err := idx.GetPrivilege("product.viewer")
	require.NoError(t, err)
	require.Len(t, values, 1)
	assert.Equal(t, AdminPrivilegeRole, values[0].Kind)

	require.NoError(t, idx.Index(indexerpkg.NewParsedFile(filePath, []byte(`
Shopware.Service('privileges').addPrivilegeMappingEntry({
    key: 'customer',
    roles: { editor: { privileges: ['customer:update'] } },
});`))))
	values, err = idx.GetPrivilege("product.viewer")
	require.NoError(t, err)
	assert.Empty(t, values)
	values, err = idx.GetPrivilege("customer:update")
	require.NoError(t, err)
	require.Len(t, values, 1)
	assert.Equal(t, "customer.editor", values[0].MappingKey+"."+values[0].Role)
}

func TestAdminPrivilegeIndexIncludesBuiltinAdministrator(t *testing.T) {
	root := t.TempDir()
	idx, err := NewAdminComponentIndexer(filepath.Join(root, "cache"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, idx.Close()) })

	values, err := idx.GetPrivilege(AdminPrivilegeAdministrator)
	require.NoError(t, err)
	require.Len(t, values, 1)
	assert.True(t, values[0].IsBuiltin())
	assert.Equal(t, AdminPrivilegeRole, values[0].Kind)
	assert.Empty(t, values[0].FilePath)

	all, err := idx.GetAllPrivileges()
	require.NoError(t, err)
	require.Len(t, all, 1)
	assert.Equal(t, AdminPrivilegeAdministrator, all[0].Name)
	assert.True(t, all[0].IsBuiltin())
}
