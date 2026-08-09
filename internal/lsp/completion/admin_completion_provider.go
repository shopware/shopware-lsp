package completion

import (
	"context"
	"path/filepath"
	"strings"

	"github.com/shopware/shopware-lsp/internal/admin"
	"github.com/shopware/shopware-lsp/internal/language"
	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
)

// AdminCompletionProvider provides completions for Shopware Admin Vue components
type AdminCompletionProvider struct {
	adminIndexer *admin.AdminComponentIndexer
}

// NewAdminCompletionProvider creates a new admin completion provider
func NewAdminCompletionProvider(adminIndexer *admin.AdminComponentIndexer) *AdminCompletionProvider {
	return &AdminCompletionProvider{adminIndexer: adminIndexer}
}

// GetCompletions returns completion items for admin components
func (p *AdminCompletionProvider) GetCompletions(ctx context.Context, params *lsp.CompletionRequest) []protocol.CompletionItem {
	ext := strings.ToLower(filepath.Ext(params.TextDocument.URI))
	languageAtCursor := lsp.EffectiveSyntaxLanguage(params.Language, params.Node)

	// Handle JS/TS files
	if ext == ".js" || ext == ".ts" ||
		ext == ".vue" && languageAtCursor == language.JavaScript {
		if params.Node == nil {
			return []protocol.CompletionItem{}
		}
		return p.jsCompletions(ctx, params)
	}

	// Handle Twig files (admin templates)
	if ext == ".twig" || ext == ".vue" && languageAtCursor == language.Twig {
		if params.Node == nil {
			return []protocol.CompletionItem{}
		}
		// Only process Twig files in administration directory
		if strings.Contains(params.TextDocument.URI, "Resources/app/administration") {
			return p.twigCompletions(ctx, params)
		}
	}

	return []protocol.CompletionItem{}
}

// jsCompletions handles completions in JS/TS files
