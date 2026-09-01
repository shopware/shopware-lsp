package lsp

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
)

// references handles textDocument/references requests
func (s *Server) references(ctx context.Context, params *protocol.ReferenceParams) ([]protocol.Location, error) {
	syntax, _ := s.documentManager.SyntaxContext(
		params.TextDocument.URI,
		params.Position.Line,
		params.Position.Character,
	)
	request := &ReferenceRequest{ReferenceParams: params, SyntaxContext: syntax}
	ctx = s.enrichContext(ctx, syntax)
	tracePerformance := s.traceProviders
	var requestStarted time.Time
	if tracePerformance {
		requestStarted = time.Now()
	}

	// Collect reference locations from all providers
	var locations []protocol.Location
	for _, provider := range s.referencesProviders {
		var providerStarted time.Time
		if tracePerformance {
			providerStarted = time.Now()
		}
		providerLocations, err := provider.GetReferences(ctx, request)
		if tracePerformance {
			log.Printf(
				"LSP references provider %T took %s (%d locations)",
				provider,
				time.Since(providerStarted).Round(time.Microsecond),
				len(providerLocations),
			)
		}
		if err != nil {
			return nil, fmt.Errorf("references provider %T: %w", provider, err)
		}
		locations = append(locations, providerLocations...)
	}
	if tracePerformance {
		log.Printf(
			"LSP references request took %s (%d locations)",
			time.Since(requestStarted).Round(time.Microsecond),
			len(locations),
		)
	}

	return locations, nil
}
