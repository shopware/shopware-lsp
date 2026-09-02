package binder

import (
	"strings"

	phpquery "github.com/shopware/shopware-lsp/internal/parser/php/query"
	phpsyntax "github.com/shopware/shopware-lsp/internal/parser/php/syntax"
	"github.com/shopware/shopware-lsp/internal/php/resolver"
	"github.com/shopware/shopware-lsp/internal/php/semantic"
)

func (b *documentBuilder) bindClassClauses(
	node *phpsyntax.Node,
	scope semantic.ScopeID,
	context resolver.NameContext,
) {
	for _, clauseKind := range []phpsyntax.Kind{
		phpsyntax.PhpExtendsClause,
		phpsyntax.PhpImplementsClause,
	} {
		clause := phpquery.DirectChild(node, clauseKind)
		if clause == nil {
			continue
		}
		for index := 0; index < clause.ChildCount(); index++ {
			child, ok := clause.Child(index).(*phpsyntax.Node)
			if ok && child.Kind() == phpsyntax.PhpName {
				b.addResolvedClassReference(child, scope, context, "")
			}
		}
	}
}

func (b *documentBuilder) bindDirectTypeReferences(
	node *phpsyntax.Node,
	scope semantic.ScopeID,
	context resolver.NameContext,
	owner semantic.SymbolID,
) {
	if node == nil {
		return
	}
	for index := 0; index < node.ChildCount(); index++ {
		child, ok := node.Child(index).(*phpsyntax.Node)
		if !ok || !isTypeNode(child.Kind()) {
			continue
		}
		b.bindTypeReferences(child, scope, context, owner)
	}
}

func (b *documentBuilder) bindTypeReferences(
	node *phpsyntax.Node,
	scope semantic.ScopeID,
	context resolver.NameContext,
	owner semantic.SymbolID,
) {
	if node.Kind() == phpsyntax.PhpType {
		nameNode := phpquery.DirectChild(node, phpsyntax.PhpName)
		if nameNode != nil {
			b.addResolvedClassReference(nameNode, scope, context, owner)
		}
	}
	for index := 0; index < node.ChildCount(); index++ {
		child, ok := node.Child(index).(*phpsyntax.Node)
		if ok && isTypeNode(child.Kind()) {
			b.bindTypeReferences(child, scope, context, owner)
		}
	}
}

func (b *documentBuilder) bindAttributeReferences(
	node *phpsyntax.Node,
	scope semantic.ScopeID,
	context resolver.NameContext,
) {
	if node == nil {
		return
	}
	for groupIndex := 0; groupIndex < node.ChildCount(); groupIndex++ {
		group, ok := node.Child(groupIndex).(*phpsyntax.Node)
		if !ok || group.Kind() != phpsyntax.PhpAttributeGroup {
			continue
		}
		for attributeIndex := 0; attributeIndex < group.ChildCount(); attributeIndex++ {
			attribute, ok := group.Child(attributeIndex).(*phpsyntax.Node)
			if !ok || attribute.Kind() != phpsyntax.PhpAttribute {
				continue
			}
			nameNode := phpquery.DirectChild(attribute, phpsyntax.PhpName)
			if nameNode != nil {
				b.addResolvedClassReference(nameNode, scope, context, "")
			}
		}
	}
}

func (b *documentBuilder) addResolvedClassReference(
	nameNode *phpsyntax.Node,
	scope semantic.ScopeID,
	context resolver.NameContext,
	owner semantic.SymbolID,
) {
	name := phpquery.NameValue(nameNode)
	if name == "" {
		return
	}
	if nonClassNativeTypeName(name) {
		return
	}
	resolved := ""
	trimmed := strings.TrimPrefix(name, "\\")
	switch {
	case strings.EqualFold(trimmed, "self"),
		strings.EqualFold(trimmed, "static"):
		if class, ok := b.enclosingClass(owner); ok {
			resolved = class.FullyQualified
		}
	case strings.EqualFold(trimmed, "parent"):
		if class, ok := b.enclosingClass(owner); ok && len(class.Extends()) > 0 {
			resolved = class.Extends()[0]
		}
	default:
		resolved = context.ResolveClass(name)
	}
	if resolved == "" {
		return
	}
	b.addSingleReference(nameNode, semantic.ClassName, scope, resolved)
}

// nonClassNativeTypeName mirrors the scalar/composite names accepted by the
// shared type parser. Native CST references only need to distinguish these
// builtins from class names; constructing an immutable semantic type for every
// declaration would immediately discard it.
func nonClassNativeTypeName(name string) bool {
	name = strings.TrimPrefix(name, "\\")
	switch len(name) {
	case 3:
		return strings.EqualFold(name, "int")
	case 4:
		return strings.EqualFold(name, "void") ||
			strings.EqualFold(name, "null") ||
			strings.EqualFold(name, "bool") ||
			strings.EqualFold(name, "true") ||
			strings.EqualFold(name, "list")
	case 5:
		return strings.EqualFold(name, "error") ||
			strings.EqualFold(name, "never") ||
			strings.EqualFold(name, "mixed") ||
			strings.EqualFold(name, "false") ||
			strings.EqualFold(name, "float") ||
			strings.EqualFold(name, "array")
	case 6:
		return strings.EqualFold(name, "double") ||
			strings.EqualFold(name, "number") ||
			strings.EqualFold(name, "string") ||
			strings.EqualFold(name, "object")
	case 7:
		return strings.EqualFold(name, "unknown") ||
			strings.EqualFold(name, "boolean") ||
			strings.EqualFold(name, "integer") ||
			strings.EqualFold(name, "closure")
	case 8:
		return strings.EqualFold(name, "resource") ||
			strings.EqualFold(name, "iterable") ||
			strings.EqualFold(name, "callable")
	case 9:
		return strings.EqualFold(name, "array-key")
	case 11:
		return strings.EqualFold(name, "enum-string")
	case 12:
		return strings.EqualFold(name, "positive-int") ||
			strings.EqualFold(name, "negative-int") ||
			strings.EqualFold(name, "class-string") ||
			strings.EqualFold(name, "trait-string")
	case 14:
		return strings.EqualFold(name, "numeric-string") ||
			strings.EqualFold(name, "literal-string") ||
			strings.EqualFold(name, "non-empty-list")
	case 15:
		return strings.EqualFold(name, "non-empty-array")
	case 16:
		return strings.EqualFold(name, "non-negative-int") ||
			strings.EqualFold(name, "non-empty-string") ||
			strings.EqualFold(name, "interface-string")
	default:
		return false
	}
}

func (b *documentBuilder) enclosingClass(owner semantic.SymbolID) (semantic.Symbol, bool) {
	for owner != "" {
		symbol, ok := b.symbol(owner)
		if !ok {
			return semantic.Symbol{}, false
		}
		if symbol.IsClassLike() {
			return symbol, true
		}
		owner = symbol.Container
	}
	return semantic.Symbol{}, false
}

func isTypeNode(kind phpsyntax.Kind) bool {
	switch kind {
	case phpsyntax.PhpType, phpsyntax.PhpNullableType,
		phpsyntax.PhpUnionType, phpsyntax.PhpIntersectionType:
		return true
	default:
		return false
	}
}
