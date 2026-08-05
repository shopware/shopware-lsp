package lsp

import (
	"context"
	"fmt"

	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
)

// codeLens handles textDocument/codeLens requests
func (s *Server) codeLens(ctx context.Context, params *protocol.CodeLensParams) ([]protocol.CodeLens, error) {
	document, ok := s.documentManager.GetDocument(params.TextDocument.URI)
	if !ok {
		return nil, nil
	}
	request := &CodeLensRequest{CodeLensParams: params, Document: document}

	// Collect code lenses from all providers
	var lenses []protocol.CodeLens
	for _, provider := range s.codeLensProviders {
		providerLenses, err := provider.GetCodeLenses(ctx, request)
		if err != nil {
			return nil, fmt.Errorf("code lens provider %T: %w", provider, err)
		}
		lenses = append(lenses, providerLenses...)
	}

	return lenses, nil
}

// resolveCodeLens handles codeLens/resolve requests
func (s *Server) resolveCodeLens(ctx context.Context, codeLens *protocol.CodeLens) (*protocol.CodeLens, error) {
	// Find a provider that can resolve this code lens
	for _, provider := range s.codeLensProviders {
		resolved, err := provider.ResolveCodeLens(ctx, codeLens)
		if err != nil {
			return nil, err
		}
		if resolved != nil {
			return resolved, nil
		}
	}

	// If no provider could resolve it, return the original
	return codeLens, nil
}
