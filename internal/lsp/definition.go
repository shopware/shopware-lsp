package lsp

import (
	"context"
	"log"
	"time"

	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
)

// definition handles textDocument/definition requests
func (s *Server) definition(ctx context.Context, params *protocol.DefinitionParams) []protocol.Location {
	syntax, _ := s.documentManager.SyntaxContext(
		params.TextDocument.URI,
		params.Position.Line,
		params.Position.Character,
	)
	request := &DefinitionRequest{DefinitionParams: params, SyntaxContext: syntax}

	ctx = s.enrichContext(ctx, syntax)
	tracePerformance := s.traceProviders
	var requestStarted time.Time
	if tracePerformance {
		requestStarted = time.Now()
	}

	// Collect definition locations from all providers
	var locations []protocol.Location
	for _, provider := range s.definitionProviders {
		var providerStarted time.Time
		if tracePerformance {
			providerStarted = time.Now()
		}
		providerLocations := provider.GetDefinition(ctx, request)
		if tracePerformance {
			log.Printf(
				"LSP definition provider %T took %s (%d locations)",
				provider,
				time.Since(providerStarted).Round(time.Microsecond),
				len(providerLocations),
			)
		}
		locations = append(locations, providerLocations...)
	}
	if tracePerformance {
		log.Printf(
			"LSP definition request took %s (%d locations)",
			time.Since(requestStarted).Round(time.Microsecond),
			len(locations),
		)
	}

	return locations
}
