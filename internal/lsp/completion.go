package lsp

import (
	"context"

	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
)

// completion handles textDocument/completion requests
func (s *Server) completion(ctx context.Context, params *protocol.CompletionParams) *protocol.CompletionList {
	syntax, _ := s.documentManager.SyntaxContext(
		params.TextDocument.URI,
		params.Position.Line,
		params.Position.Character,
	)
	request := &CompletionRequest{CompletionParams: params, SyntaxContext: syntax}

	ctx = s.enrichContext(ctx, syntax)

	// Collect completion items from all providers
	var items []protocol.CompletionItem
	for _, provider := range s.completionProviders {
		providerItems := provider.GetCompletions(ctx, request)
		items = append(items, providerItems...)
	}
	s.filterCompletionCommandsForClient(items)

	// Return the completion list
	return &protocol.CompletionList{
		IsIncomplete: false,
		Items:        items,
	}
}
