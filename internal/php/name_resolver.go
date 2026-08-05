package php

import (
	phpquery "github.com/shopware/shopware-lsp/internal/parser/php/query"
	phpsyntax "github.com/shopware/shopware-lsp/internal/parser/php/syntax"
	"github.com/shopware/shopware-lsp/internal/php/resolver"
)

// NameResolver is the compatibility-free project-facing wrapper around the
// semantic PHP name resolver. Symfony configuration indexing uses it without
// depending on binder internals.
type NameResolver struct {
	context resolver.NameContext
}

func NewNameResolver(root *phpsyntax.Node) *NameResolver {
	context := resolver.NewNameContext(phpquery.Namespace(root))
	phpquery.Visit(
		root,
		func(declaration *phpsyntax.Node) bool {
			context.AddUseDeclaration(declaration.Text())
			return true
		},
		phpsyntax.PhpUseDeclaration,
	)
	return &NameResolver{context: context}
}

func (r *NameResolver) Namespace() string {
	if r == nil {
		return ""
	}
	return r.context.Namespace
}

func (r *NameResolver) Resolve(name string) string {
	if r == nil {
		return name
	}
	return r.context.ResolveClass(name)
}
