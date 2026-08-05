package lsp

import (
	"context"
	"fmt"

	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
)

func (s *Server) inlayHints(
	ctx context.Context,
	params *protocol.InlayHintParams,
) ([]protocol.InlayHint, error) {
	document, ok := s.documentManager.GetDocument(params.TextDocument.URI)
	if !ok {
		return nil, nil
	}
	request := &InlayHintRequest{
		InlayHintParams: params,
		Document:        document,
	}
	var result []protocol.InlayHint
	for _, provider := range s.inlayHintProviders {
		hints, err := provider.GetInlayHints(ctx, request)
		if err != nil {
			return nil, fmt.Errorf(
				"inlay hint provider %T: %w",
				provider,
				err,
			)
		}
		result = append(result, hints...)
	}
	return result, nil
}
