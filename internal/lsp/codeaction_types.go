package lsp

import (
	"context"

	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
)

// ActionProvider is an interface for providing code actions
type ActionProvider interface {
	// GetCodeActions returns code actions for the given parameters
	GetCodeActions(ctx context.Context, request *CodeActionRequest) []protocol.CodeAction
	// GetCodeActionKinds returns the kinds of code actions this provider can provide
	GetCodeActionKinds() []protocol.CodeActionKind
}
