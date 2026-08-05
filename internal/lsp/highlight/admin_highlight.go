package highlight

import (
	"context"
	"path/filepath"
	"strings"

	"github.com/shopware/shopware-lsp/internal/admin"
	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	"github.com/shopware/shopware-lsp/internal/lsp/reference"
	"github.com/shopware/shopware-lsp/internal/uriutil"
)

// AdminDocumentHighlightProvider projects the Administration reference graph
// onto the current open document. The reference provider owns symbol identity
// and live-CST occurrence discovery; highlights add no parallel resolver.
type AdminDocumentHighlightProvider struct {
	references *reference.AdminReferenceProvider
}

func NewAdminDocumentHighlightProvider(
	index *admin.AdminComponentIndexer,
) *AdminDocumentHighlightProvider {
	return &AdminDocumentHighlightProvider{
		references: reference.NewAdminReferenceProvider(index),
	}
}

func (p *AdminDocumentHighlightProvider) GetDocumentHighlights(
	ctx context.Context,
	request *lsp.DocumentHighlightRequest,
) ([]protocol.DocumentHighlight, error) {
	if ctx.Err() != nil || p == nil || p.references == nil ||
		request == nil || request.DocumentHighlightParams == nil ||
		request.Document == nil || request.Root == nil {
		return nil, nil
	}
	path, err := uriutil.Path(request.TextDocument.URI)
	if err != nil {
		return nil, err
	}
	extension := strings.ToLower(filepath.Ext(path))
	if extension != ".js" && extension != ".ts" && extension != ".twig" &&
		extension != ".vue" {
		return nil, nil
	}
	params := &protocol.ReferenceParams{}
	params.TextDocument.URI = request.TextDocument.URI
	params.Position = request.Position
	params.Context.IncludeDeclaration = true
	locations, err := p.references.GetReferences(
		ctx,
		&lsp.ReferenceRequest{
			ReferenceParams: params,
			SyntaxContext:   request.SyntaxContext,
		},
	)
	if err != nil {
		return nil, err
	}
	result := make([]protocol.DocumentHighlight, 0, len(locations))
	for _, location := range locations {
		candidatePath, pathErr := uriutil.Path(location.URI)
		if pathErr != nil || filepath.Clean(candidatePath) != filepath.Clean(path) {
			continue
		}
		result = append(result, protocol.DocumentHighlight{
			Range: location.Range,
			Kind:  protocol.DocumentHighlightText,
		})
	}
	return result, nil
}

var _ lsp.DocumentHighlightProvider = (*AdminDocumentHighlightProvider)(nil)
