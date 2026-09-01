package admin

import (
	"strings"

	"github.com/shopware/shopware-lsp/internal/parser/cst"
	twigast "github.com/shopware/shopware-lsp/internal/parser/twig/ast"
	twigquery "github.com/shopware/shopware-lsp/internal/parser/twig/query"
	twigsyntax "github.com/shopware/shopware-lsp/internal/parser/twig/syntax"
)

func CollectTwigUsages(
	root *twigsyntax.Node,
	filePath string,
	lineIndex *cst.LineIndex,
) []AdminUsageSet {
	if root == nil || lineIndex == nil {
		return nil
	}
	return parseAdminTwigUsages(root, filePath, lineIndex)
}

func staticComponentContractNames(startTag *twigsyntax.Node) []string {
	if startTag == nil {
		return nil
	}
	if name := twigquery.HTMLTagName(startTag); IsComponentTag(name) {
		return []string{name}
	}
	selector, dynamic := TwigDynamicComponentSelector(startTag)
	if !dynamic || !selector.Complete {
		return nil
	}
	var result []string
	for _, name := range selector.Names() {
		if IsComponentTag(name) {
			result = append(result, name)
		}
	}
	return result
}

func twigVueRootIdentifierIsLocal(
	root *twigsyntax.Node,
	content []byte,
	identifier TwigVueMember,
) bool {
	return twigVueRootIdentifierIsLocalIn(
		root,
		twigVueForBindings(root, content),
		TwigScopedSlots(root),
		identifier,
	)
}

// TwigVueRootIdentifierIsLocal reports whether identifier resolves to a
// lexical v-for, event, or scoped-slot binding instead of the owning component
// instance. Semantic diagnostics use it to avoid shadowing false positives.
func TwigVueRootIdentifierIsLocal(
	root *twigsyntax.Node,
	content []byte,
	identifier TwigVueMember,
) bool {
	return twigVueRootIdentifierIsLocalIn(
		root,
		twigVueForBindings(root, content),
		TwigScopedSlots(root),
		identifier,
	)
}

// TwigVueLocalRootIdentifierRanges resolves all lexical v-for, event, and
// scoped-slot identifiers with one shared scope scan. The returned set is
// keyed by the exact source range supplied in identifiers.
func TwigVueLocalRootIdentifierRanges(
	root *twigsyntax.Node,
	content []byte,
	identifiers []TwigVueMember,
) map[cst.TextRange]bool {
	if root == nil || len(identifiers) == 0 {
		return nil
	}
	bindings := twigVueForBindings(root, content)
	scopes := TwigScopedSlots(root)
	var result map[cst.TextRange]bool
	for _, identifier := range identifiers {
		if !twigVueRootIdentifierIsLocalIn(
			root, bindings, scopes, identifier,
		) {
			continue
		}
		if result == nil {
			result = make(map[cst.TextRange]bool)
		}
		result[identifier.Range] = true
	}
	return result
}

func twigVueRootIdentifierIsLocalIn(
	root *twigsyntax.Node,
	bindings []TwigVueBinding,
	scopes []TwigScopedSlot,
	identifier TwigVueMember,
) bool {
	if twigVueEventIdentifierIsLocal(root, identifier) ||
		twigVueBindingIdentifierIsLocal(bindings, identifier) {
		return true
	}
	for _, scope := range twigScopedSlotsAtOffset(
		scopes, identifier.Range.Start,
	) {
		for _, binding := range scope.Bindings {
			if binding.LocalName == identifier.Name {
				return true
			}
		}
	}
	return false
}

func twigVueEventIdentifierIsLocal(
	root *twigsyntax.Node,
	identifier TwigVueMember,
) bool {
	if root == nil || identifier.Name != "$event" {
		return false
	}
	node := root.NodeAtOffset(identifier.Range.Start)
	attribute := twigquery.HTMLAttributeAt(node)
	return attribute != nil &&
		NormalizeEventName(twigquery.HTMLAttributeName(attribute)) != ""
}

func twigVueBindingIdentifierIsLocal(
	bindings []TwigVueBinding,
	identifier TwigVueMember,
) bool {
	offset := identifier.Range.Start
	for _, binding := range bindings {
		if binding.Name != identifier.Name {
			continue
		}
		if binding.IsDeclarationOffset(offset) {
			return true
		}
		if offset < binding.ScopeRange.Start || offset > binding.ScopeRange.End {
			continue
		}
		if binding.Kind == TwigVueBindingFor &&
			offset >= binding.ExpressionRange.Start &&
			offset <= binding.ExpressionRange.End {
			continue
		}
		return true
	}
	return false
}

func collectSlotDeclaration(
	tag twigast.HtmlStartingTag,
	filePath string,
	collector *adminUsageCollector,
) {
	for attribute := range tag.Attributes() {
		if twigquery.HTMLAttributeName(attribute.Syntax()) != "name" {
			continue
		}
		value, ok := attribute.Value()
		if !ok {
			return
		}
		inner, ok := value.GetInner()
		if !ok {
			return
		}
		name := strings.TrimSpace(inner.Syntax().Text())
		if name == "" || strings.ContainsAny(name, "{}%") {
			return
		}
		collector.addRange(
			AdminSymbolComponentSlot,
			filePath,
			name,
			inner.Syntax().RangeTrimmedTrivia(),
			true,
			false,
		)
		return
	}
}

func mustVueAttributeArgumentRange(
	rangeValue cst.TextRange,
	attributeName string,
) cst.TextRange {
	resolved, found := vueAttributeArgumentRange(attributeName, rangeValue)
	if !found {
		return rangeValue
	}
	return resolved
}

func parentComponentName(node *twigsyntax.Node) string {
	for current := node.Parent(); current != nil; current = current.Parent() {
		tag, ok := twigast.CastHtmlTag(current)
		if !ok || tag.Name() == nil {
			continue
		}
		starting, startFound := tag.StartingTag()
		if !startFound {
			continue
		}
		if name, found := StaticComponentNameForTag(starting.Syntax()); found {
			return name
		}
	}
	return ""
}

// JavaScriptSymbolAt resolves registry-backed Administration symbols at a
// cursor. It is shared by references and rename so both operations use the
// same conservative context rules as completion and definition.
