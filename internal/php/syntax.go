package php

import (
	"context"
	"slices"

	phpquery "github.com/shopware/shopware-lsp/internal/parser/php/query"
	phpsyntax "github.com/shopware/shopware-lsp/internal/parser/php/syntax"
	"github.com/shopware/shopware-lsp/internal/php/types"
)

func (s *PHPIndex) IsMethodCalledName(ctx context.Context, node *phpsyntax.Node, content []byte, methodNames ...string) bool {
	call := phpquery.CallAt(node)
	if call == nil {
		return false
	}
	return slices.Contains(methodNames, phpquery.CallMethodName(call))
}

func (s *PHPIndex) IsMethodCalledOnClass(ctx context.Context, node *phpsyntax.Node, content []byte, className string) bool {
	call := phpquery.CallAt(node)
	if call == nil {
		return false
	}

	phpContext, ok := ctx.Value(PHPContextKey).(*PHPContext)
	if ok && phpContext != nil && phpContext.Document != nil {
		var receiver *phpsyntax.Node
		for child := range call.ChildNodes() {
			receiver = child
			break
		}
		if receiver != nil {
			receiverType := phpContext.Document.TypeOf(receiver).Type
			if !receiverType.IsUnknown() {
				snapshot := phpContext.Snapshot
				if snapshot == nil {
					snapshot = s.SemanticSnapshot()
				}
				return snapshot.Relations().IsSubtype(receiverType, types.Named(className))
			}
		}
	}

	return false
}
