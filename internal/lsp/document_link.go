package lsp

import (
	"context"
	"fmt"

	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
)

func (s *Server) documentLinks(
	ctx context.Context,
	params *protocol.DocumentLinkParams,
) ([]protocol.DocumentLink, error) {
	document, ok := s.documentManager.GetDocument(params.TextDocument.URI)
	if !ok {
		return nil, nil
	}
	request := &DocumentLinkRequest{
		DocumentLinkParams: params,
		Document:           document,
	}
	var result []protocol.DocumentLink
	for _, provider := range s.documentLinkProviders {
		links, err := provider.GetDocumentLinks(ctx, request)
		if err != nil {
			return nil, fmt.Errorf(
				"document link provider %T: %w",
				provider,
				err,
			)
		}
		result = append(result, links...)
	}
	return result, nil
}
