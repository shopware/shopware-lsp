package admin

import (
	jsquery "github.com/shopware/shopware-lsp/internal/parser/javascript/query"
	jssyntax "github.com/shopware/shopware-lsp/internal/parser/javascript/syntax"
	twigast "github.com/shopware/shopware-lsp/internal/parser/twig/ast"
	twigquery "github.com/shopware/shopware-lsp/internal/parser/twig/query"
	twigsyntax "github.com/shopware/shopware-lsp/internal/parser/twig/syntax"
)

func TwigSymbolAt(node *twigsyntax.Node) (AdminSymbolTarget, bool) {
	if node == nil {
		return AdminSymbolTarget{}, false
	}
	root := node
	for root.Parent() != nil {
		root = root.Parent()
	}
	return TwigSymbolAtOffset(root, node.Range().Start)
}

// TwigDirectiveReferences returns every custom directive attribute in one
// Administration template. Vue built-ins are excluded because they are
// language syntax rather than registry symbols.
func TwigDirectiveReferences(
	root *twigsyntax.Node,
) []VueDirectiveReference {
	if root == nil {
		return nil
	}
	var result []VueDirectiveReference
	for attributeNode := range twigquery.IterateNodes(
		root, twigsyntax.HtmlAttribute,
	) {
		attribute, ok := twigast.CastHtmlAttribute(attributeNode)
		if !ok || attribute.Name() == nil {
			continue
		}
		reference, found := VueDirectiveReferenceForAttribute(
			twigquery.HTMLAttributeName(attributeNode),
			attribute.Name().Range(),
		)
		if found {
			result = append(result, reference)
		}
	}
	return result
}

func TwigDirectiveAtOffset(
	root *twigsyntax.Node,
	offset uint32,
) (VueDirectiveReference, bool) {
	for _, reference := range TwigDirectiveReferences(root) {
		if rangeContains(reference.Range, offset) {
			return reference, true
		}
	}
	return VueDirectiveReference{}, false
}

func stringTarget(kind AdminSymbolKind, node *jssyntax.Node) (AdminSymbolTarget, bool) {
	name := jsquery.StringValue(node)
	if name == "" {
		return AdminSymbolTarget{}, false
	}
	return AdminSymbolTarget{Kind: kind, Name: name}, true
}
