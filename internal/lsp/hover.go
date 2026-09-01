package lsp

import (
	"context"
	"fmt"
	"log"
	"time"

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
	tracePerformance := s.traceProviders
	var requestStarted time.Time
	if tracePerformance {
		requestStarted = time.Now()
	}

	// Try each hover provider until one returns a result
	for _, provider := range s.hoverProviders {
		var providerStarted time.Time
		if tracePerformance {
			providerStarted = time.Now()
		}
		hover, err := provider.GetHover(ctx, request)
		if tracePerformance {
			log.Printf(
				"LSP hover provider %T took %s (result=%t)",
				provider,
				time.Since(providerStarted).Round(time.Microsecond),
				hover != nil,
			)
		}
		if err != nil {
			return nil, fmt.Errorf("hover provider %T: %w", provider, err)
		}
		if hover != nil {
			if tracePerformance {
				log.Printf(
					"LSP hover request took %s",
					time.Since(requestStarted).Round(time.Microsecond),
				)
			}
			return hover, nil
		}
	}

	// No hover information available
	if tracePerformance {
		log.Printf(
			"LSP hover request took %s",
			time.Since(requestStarted).Round(time.Microsecond),
		)
	}
	return nil, nil
}
