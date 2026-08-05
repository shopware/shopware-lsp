package lsp

import (
	"context"
	"fmt"

	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
)

func (s *Server) rename(
	ctx context.Context,
	params *protocol.RenameParams,
) (*protocol.WorkspaceEdit, error) {
	syntax, _ := s.documentManager.SyntaxContext(
		params.TextDocument.URI,
		params.Position.Line,
		params.Position.Character,
	)
	request := &RenameRequest{RenameParams: params, SyntaxContext: syntax}
	ctx = s.enrichContext(ctx, syntax)
	for _, provider := range s.renameProviders {
		edit, err := provider.Rename(ctx, request)
		if err != nil {
			return nil, fmt.Errorf("rename provider %T: %w", provider, err)
		}
		if edit != nil {
			return edit, nil
		}
	}
	return nil, nil
}
