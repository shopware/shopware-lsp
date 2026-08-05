package lsp

import (
	"context"
	"fmt"

	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
)

type FileRenameRequest struct {
	*protocol.RenameFilesParams
	Documents []*TextDocument
}

type FileRenameProvider interface {
	WillRenameFiles(
		context.Context,
		*FileRenameRequest,
	) (*protocol.WorkspaceEdit, error)
}

func (s *Server) willRenameFiles(
	ctx context.Context,
	params *protocol.RenameFilesParams,
) (*protocol.WorkspaceEdit, error) {
	request := &FileRenameRequest{
		RenameFilesParams: params,
		Documents:         s.documentManager.Documents(),
	}
	changes := make(map[string][]protocol.TextEdit)
	for _, provider := range s.fileRenameProviders {
		edit, err := provider.WillRenameFiles(ctx, request)
		if err != nil {
			return nil, fmt.Errorf(
				"file rename provider %T: %w",
				provider,
				err,
			)
		}
		if edit == nil {
			continue
		}
		for uri, edits := range edit.Changes {
			changes[uri] = append(changes[uri], edits...)
		}
	}
	if len(changes) == 0 {
		return nil, nil
	}
	return &protocol.WorkspaceEdit{Changes: changes}, nil
}
