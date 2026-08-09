package hover

import (
	"context"
	"path/filepath"
	"strings"

	"github.com/shopware/shopware-lsp/internal/admin"
	"github.com/shopware/shopware-lsp/internal/language"
	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
)

// AdminHoverProvider provides hover information for Shopware Admin Vue components
type AdminHoverProvider struct {
	adminIndexer *admin.AdminComponentIndexer
	projectRoot  string
}

// NewAdminHoverProvider creates a new admin hover provider
func NewAdminHoverProvider(projectRoot string, adminIndexer *admin.AdminComponentIndexer) *AdminHoverProvider {
	return &AdminHoverProvider{
		adminIndexer: adminIndexer,
		projectRoot:  projectRoot,
	}
}

// GetHover returns hover information for Vue components
func (p *AdminHoverProvider) GetHover(ctx context.Context, params *lsp.HoverRequest) (*protocol.Hover, error) {
	ext := strings.ToLower(filepath.Ext(params.TextDocument.URI))
	languageAtCursor := lsp.EffectiveSyntaxLanguage(params.Language, params.Node)

	// Handle JS/TS files
	if ext == ".js" || ext == ".ts" ||
		ext == ".vue" && languageAtCursor == language.JavaScript {
		if params.Node == nil {
			return nil, nil
		}
		return p.jsHover(ctx, params)
	}

	// Handle Twig files (admin templates)
	if ext == ".twig" || ext == ".vue" && languageAtCursor == language.Twig {
		if params.Node == nil {
			return nil, nil
		}
		// Only process Twig files in administration directory
		if strings.Contains(params.TextDocument.URI, "Resources/app/administration") {
			return p.twigHover(ctx, params)
		}
	}

	return nil, nil
}
