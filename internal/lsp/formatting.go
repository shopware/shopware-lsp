package lsp

import (
	"context"
	"fmt"

	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
)

func (s *Server) formatting(
	ctx context.Context,
	params *protocol.DocumentFormattingParams,
) ([]protocol.TextEdit, error) {
	if params == nil {
		return nil, nil
	}
	document, ok := s.documentManager.GetDocument(params.TextDocument.URI)
	if !ok {
		return nil, nil
	}
	request := &DocumentFormattingRequest{
		DocumentFormattingParams: params,
		Document:                 document,
	}
	for _, provider := range s.documentFormattingProviders {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		formatted, handled, err := provider.FormatDocument(ctx, request)
		if err != nil {
			return nil, fmt.Errorf("document formatting provider %T: %w", provider, err)
		}
		if !handled || formatted == document.Source {
			continue
		}
		endLine, endCharacter := document.LineIndex.PositionUTF16(
			uint32(len(document.Text)),
		)
		return []protocol.TextEdit{{
			Range: protocol.Range{
				Start: protocol.Position{},
				End: protocol.Position{
					Line: int(endLine), Character: int(endCharacter),
				},
			},
			NewText: formatted,
		}}, nil
	}
	return nil, nil
}
