package lsp

import (
	"context"

	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
)

// GotoDefinitionProvider is an interface for providing definition locations
type GotoDefinitionProvider interface {
	// GetDefinition returns location(s) for the definition of the symbol at the given position
	GetDefinition(ctx context.Context, request *DefinitionRequest) []protocol.Location
}
