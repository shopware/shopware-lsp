package admin

import (
	"bytes"
	"sort"

	"github.com/shopware/shopware-lsp/internal/parser/cst"
	twigast "github.com/shopware/shopware-lsp/internal/parser/twig/ast"
	twigquery "github.com/shopware/shopware-lsp/internal/parser/twig/query"
	twigsyntax "github.com/shopware/shopware-lsp/internal/parser/twig/syntax"
)

func TwigVueBindings(
	root *twigsyntax.Node,
	content []byte,
) []TwigVueBinding {
	if root == nil {
		return nil
	}
	var result []TwigVueBinding
	for _, node := range twigquery.Nodes(root, twigsyntax.HtmlTag) {
		tag, ok := twigast.CastHtmlTag(node)
		if !ok || tag.Name() == nil {
			continue
		}
		startingTag, ok := tag.StartingTag()
		if !ok {
			continue
		}
		for _, attribute := range startingTag.Attributes() {
			name := twigquery.HTMLAttributeName(attribute.Syntax())
			value, hasValue := attribute.Value()
			if !hasValue {
				continue
			}
			inner, hasInner := value.GetInner()
			if !hasInner {
				continue
			}
			expressionRange := inner.Syntax().RangeTrimmedTrivia()
			if expressionRange.End > uint32(len(content)) {
				continue
			}
			switch {
			case name == "v-for":
				result = append(result, parseTwigVueForBindings(
					inner.Syntax().Text(), expressionRange.Start,
					node.RangeTrimmedTrivia(), expressionRange,
				)...)
			case NormalizeEventName(name) != "":
				result = append(result, TwigVueBinding{
					Name: "$event", Kind: TwigVueBindingEvent,
					ScopeRange: expressionRange, ExpressionRange: expressionRange,
					ComponentName: tag.Name().Text(),
					EventName:     NormalizeEventName(name),
				})
			}
		}
	}
	return result
}

// TwigVueBindingsAtOffset returns the effective lexical Vue bindings at an
// offset. Innermost declarations replace outer declarations of the same name.
// A v-for alias is not visible in its own iterable expression.
func TwigVueBindingsAtOffset(
	root *twigsyntax.Node,
	content []byte,
	offset uint32,
) []TwigVueBinding {
	return twigVueBindingsAtOffset(TwigVueBindings(root, content), offset)
}

func twigVueBindingsAtOffset(
	bindings []TwigVueBinding,
	offset uint32,
) []TwigVueBinding {
	visible := make([]TwigVueBinding, 0)
	for _, binding := range bindings {
		if offset < binding.ScopeRange.Start || offset > binding.ScopeRange.End {
			continue
		}
		if binding.Kind == TwigVueBindingFor &&
			offset >= binding.ExpressionRange.Start &&
			offset <= binding.ExpressionRange.End &&
			!binding.IsDeclarationOffset(offset) {
			continue
		}
		visible = append(visible, binding)
	}
	// Apply outer scopes first and innermost scopes last. Stable ordering keeps
	// tuple bindings in their source order.
	sort.SliceStable(visible, func(left, right int) bool {
		return visible[left].ScopeRange.Len() > visible[right].ScopeRange.Len()
	})
	positions := make(map[string]int, len(visible))
	result := make([]TwigVueBinding, 0, len(visible))
	for _, binding := range visible {
		if index, exists := positions[binding.Name]; exists {
			result[index] = binding
			continue
		}
		positions[binding.Name] = len(result)
		result = append(result, binding)
	}
	return result
}

// TwigVueBindingAtOffset resolves the Vue lexical variable touching offset.
func TwigVueBindingAtOffset(
	root *twigsyntax.Node,
	content []byte,
	offset uint32,
) (*TwigVueBinding, bool) {
	return twigVueBindingAtOffset(
		root, content, TwigVueBindings(root, content), offset,
	)
}

func twigVueBindingAtOffset(
	root *twigsyntax.Node,
	content []byte,
	bindings []TwigVueBinding,
	offset uint32,
) (*TwigVueBinding, bool) {
	for _, binding := range bindings {
		if binding.IsDeclarationOffset(offset) {
			resolved := binding
			return &resolved, true
		}
	}
	name, _, found := TwigVueExpressionRootIdentifierAtOffset(
		root, content, offset,
	)
	if !found {
		return nil, false
	}
	for _, binding := range twigVueBindingsAtOffset(bindings, offset) {
		if binding.Name == name {
			resolved := binding
			return &resolved, true
		}
	}
	return nil, false
}

// TwigVueBindingReferences finds a declaration and all root-identifier usages
// resolving to the same lexical binding. Strings, member property names and
// object literal keys are excluded by the expression-aware resolver.
func TwigVueBindingReferences(
	root *twigsyntax.Node,
	content []byte,
	target TwigVueBinding,
) []cst.TextRange {
	if root == nil || target.Name == "" {
		return nil
	}
	bindings := TwigVueBindings(root, content)
	var result []cst.TextRange
	seen := make(map[cst.TextRange]bool)
	add := func(value cst.TextRange) {
		if value.Len() == 0 || seen[value] {
			return
		}
		seen[value] = true
		result = append(result, value)
	}
	add(target.DeclarationRange)
	for start := 0; start < len(content); {
		relative := bytes.Index(content[start:], []byte(target.Name))
		if relative < 0 {
			break
		}
		position := start + relative
		end := position + len(target.Name)
		start = end
		if position > 0 && isSlotIdentifierContinue(content[position-1]) ||
			end < len(content) && isSlotIdentifierContinue(content[end]) {
			continue
		}
		name, rangeValue, found := TwigVueExpressionRootIdentifierAtOffset(
			root, content, uint32(position),
		)
		if !found || name != target.Name {
			continue
		}
		for _, candidate := range twigVueBindingsAtOffset(
			bindings, uint32(position),
		) {
			if candidate.Name == name && candidate.sameIdentity(target) {
				add(rangeValue)
				break
			}
		}
	}
	sort.Slice(result, func(left, right int) bool {
		return result[left].Start < result[right].Start
	})
	return result
}

// TwigVueExpressionMemberAtOffset resolves a statically inspectable property
// chain rooted in one identifier. Calls and bracket access are retained as
// receiver operations while literal/comment text remains excluded. Resolution
// still rejects computed access unless the indexed receiver has a sound typed
// contract such as an array, tuple, or Record.
