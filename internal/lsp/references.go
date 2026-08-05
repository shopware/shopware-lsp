package lsp

import (
	"context"
	"fmt"

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

	// Collect reference locations from all providers
	var locations []protocol.Location
	for _, provider := range s.referencesProviders {
		providerLocations, err := provider.GetReferences(ctx, request)
		if err != nil {
			return nil, fmt.Errorf("references provider %T: %w", provider, err)
		}
		locations = append(locations, providerLocations...)
	}

	return locations, nil
}
