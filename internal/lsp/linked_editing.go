package lsp

import (
	"context"
	"fmt"

	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
)

func (s *Server) linkedEditingRanges(
	ctx context.Context,
	params *protocol.LinkedEditingRangeParams,
) (*protocol.LinkedEditingRanges, error) {
	if params == nil {
		return nil, nil
	}
	syntax, ok := s.documentManager.SyntaxContext(
		params.TextDocument.URI,
		params.Position.Line,
		params.Position.Character,
	)
	if !ok {
		return nil, nil
	}
	request := &LinkedEditingRangeRequest{
		LinkedEditingRangeParams: params,
		SyntaxContext:            syntax,
	}
	ctx = s.enrichContext(ctx, syntax)
	for _, provider := range s.linkedEditingProviders {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		ranges, err := provider.GetLinkedEditingRanges(ctx, request)
		if err != nil {
			return nil, fmt.Errorf(
				"linked editing range provider %T: %w", provider, err,
			)
		}
		if ranges == nil || len(ranges.Ranges) < 2 {
			continue
		}
		return ranges, nil
	}
	return nil, nil
}
