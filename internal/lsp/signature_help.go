package lsp

import (
	"context"
	"fmt"

	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
)

func (s *Server) signatureHelp(
	ctx context.Context,
	params *protocol.SignatureHelpParams,
) (*protocol.SignatureHelp, error) {
	syntax, _ := s.documentManager.SyntaxContext(
		params.TextDocument.URI,
		params.Position.Line,
		params.Position.Character,
	)
	request := &SignatureHelpRequest{
		SignatureHelpParams: params,
		SyntaxContext:       syntax,
	}
	ctx = s.enrichContext(ctx, syntax)
	for _, provider := range s.signatureProviders {
		result, err := provider.GetSignatureHelp(ctx, request)
		if err != nil {
			return nil, fmt.Errorf("signature help provider %T: %w", provider, err)
		}
		if result != nil {
			return result, nil
		}
	}
	return nil, nil
}
