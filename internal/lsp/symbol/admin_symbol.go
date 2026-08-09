package symbol

import (
	"github.com/shopware/shopware-lsp/internal/admin"
)

// AdminWorkspaceSymbolProvider exposes the registry-like Administration
// declarations that do not have equivalents in the PHP symbol graph.
type AdminWorkspaceSymbolProvider struct {
	index *admin.AdminComponentIndexer
}

func NewAdminWorkspaceSymbolProvider(index *admin.AdminComponentIndexer) *AdminWorkspaceSymbolProvider {
	return &AdminWorkspaceSymbolProvider{index: index}
}
