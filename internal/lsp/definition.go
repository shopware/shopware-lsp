package lsp

import (
	"context"

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

	// Collect definition locations from all providers
	var locations []protocol.Location
	for _, provider := range s.definitionProviders {
		providerLocations := provider.GetDefinition(ctx, request)
		locations = append(locations, providerLocations...)
	}

	return locations
}
