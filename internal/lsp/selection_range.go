package lsp

import (
	"context"
	"fmt"

	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
)

func (s *Server) selectionRanges(
	ctx context.Context,
	params *protocol.SelectionRangeParams,
) ([]protocol.SelectionRange, error) {
	if params == nil || len(params.Positions) == 0 {
		return nil, nil
	}
	document, ok := s.documentManager.GetDocument(params.TextDocument.URI)
	if !ok {
		return nil, nil
	}
	request := &SelectionRangeRequest{
		SelectionRangeParams: params, Document: document,
	}
	for _, provider := range s.selectionRangeProviders {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		ranges, err := provider.GetSelectionRanges(ctx, request)
		if err != nil {
			return nil, fmt.Errorf("selection range provider %T: %w", provider, err)
		}
		// LSP requires one result for every input position in the same order.
		if len(ranges) != len(params.Positions) {
			continue
		}
		return ranges, nil
	}
	return nil, nil
}
