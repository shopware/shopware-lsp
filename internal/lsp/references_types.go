package lsp

import (
	"context"

	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
)

// ReferencesProvider is an interface for providing reference locations
type ReferencesProvider interface {
	// GetReferences returns location(s) for all references to the symbol at the given position
	GetReferences(ctx context.Context, request *ReferenceRequest) ([]protocol.Location, error)
}
