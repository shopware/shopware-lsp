package lsp

import (
	"context"
	"fmt"

	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
)

// hover handles textDocument/hover requests
func (s *Server) hover(ctx context.Context, params *protocol.HoverParams) (*protocol.Hover, error) {
	syntax, _ := s.documentManager.SyntaxContext(
		params.TextDocument.URI,
		params.Position.Line,
		params.Position.Character,
	)
	request := &HoverRequest{HoverParams: params, SyntaxContext: syntax}

	ctx = s.enrichContext(ctx, syntax)

	// Try each hover provider until one returns a result
	for _, provider := range s.hoverProviders {
		hover, err := provider.GetHover(ctx, request)
		if err != nil {
			return nil, fmt.Errorf("hover provider %T: %w", provider, err)
		}
		if hover != nil {
			return hover, nil
		}
	}

	// No hover information available
	return nil, nil
}
