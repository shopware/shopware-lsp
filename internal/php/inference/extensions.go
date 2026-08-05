// Package inference performs local expression and control-flow type analysis
// over bound PHP semantic documents.
package inference

import (
	phpsyntax "github.com/shopware/shopware-lsp/internal/parser/php/syntax"
	"github.com/shopware/shopware-lsp/internal/php/resolver"
	"github.com/shopware/shopware-lsp/internal/php/semantic"
	"github.com/shopware/shopware-lsp/internal/php/types"
)

// CallArgument is shared with signature resolution so call analysis only has
// to materialize one argument slice.
type CallArgument = resolver.Argument

type CallContext struct {
	Snapshot     *semantic.Snapshot
	Document     *semantic.Document
	Node         *phpsyntax.Node
	Name         string
	Receiver     types.Type
	Arguments    []CallArgument
	CurrentClass types.Type
	Static       bool
}

// Extension supplies domain-specific return types without coupling the core
// evaluator to Symfony or Shopware.
type Extension interface {
	InferCall(CallContext) (semantic.TypeFact, bool)
}

type ExtensionFunc func(CallContext) (semantic.TypeFact, bool)

func (f ExtensionFunc) InferCall(context CallContext) (semantic.TypeFact, bool) {
	return f(context)
}
