package stimulus

import (
	"path/filepath"
	"sort"
	"strings"

	"github.com/shopware/shopware-lsp/internal/parser/cst"
	twigast "github.com/shopware/shopware-lsp/internal/parser/twig/ast"
	twigquery "github.com/shopware/shopware-lsp/internal/parser/twig/query"
	twigsyntax "github.com/shopware/shopware-lsp/internal/parser/twig/syntax"
)

func References(
	path string,
	root *twigsyntax.Node,
) []Reference {
	extension := strings.ToLower(filepath.Ext(path))
	if root == nil || extension != ".twig" && extension != ".html" {
		return nil
	}
	var result []Reference
	for _, literal := range twigquery.StringArgumentsInFunctions(
		root,
		"stimulus_controller",
	) {
		if twigquery.FunctionArgumentIndex(literal) != 0 ||
			!twigquery.StringIsStatic(literal) {
			continue
		}
		value, rng := twigStringValueRange(literal)
		if value != "" {
			result = append(result, Reference{
				Name:  value,
				Range: rng,
				Twig:  true,
			})
		}
	}
	for htmlString := range twigquery.IterateNodes(
		root,
		twigsyntax.HtmlString,
	) {
		if !isDataControllerString(htmlString) {
			continue
		}
		value, rng := htmlStringValueRange(htmlString)
		if strings.Contains(value, "{{") ||
			strings.Contains(value, "{%") {
			continue
		}
		for _, token := range controllerTokens(value, rng.Start) {
			result = append(result, Reference{
				Name:  token.Name,
				Range: token.Range,
			})
		}
	}
	sort.Slice(result, func(left, right int) bool {
		return result[left].Range.Start < result[right].Range.Start
	})
	return result
}

func ReferenceAt(
	root,
	node *twigsyntax.Node,
	offset uint32,
) (Reference, bool) {
	if root == nil || node == nil {
		return Reference{}, false
	}
	if literal := twigquery.LiteralStringAt(node); literal != nil &&
		twigquery.StringInFunction(literal, "stimulus_controller") &&
		twigquery.FunctionArgumentIndex(literal) == 0 &&
		twigquery.StringIsStatic(literal) {
		value, rng := twigStringValueRange(literal)
		if rangeContainsCursor(rng, offset) {
			return Reference{Name: value, Range: rng, Twig: true}, true
		}
	}
	htmlString := twigquery.ClosestNodeOfKind(
		node,
		twigsyntax.HtmlString,
	)
	if htmlString == nil || !isDataControllerString(htmlString) {
		return Reference{}, false
	}
	value, rng := htmlStringValueRange(htmlString)
	if strings.Contains(value, "{{") ||
		strings.Contains(value, "{%") ||
		!rangeContainsCursor(rng, offset) {
		return Reference{}, false
	}
	name, tokenRange := controllerTokenAt(value, rng.Start, offset)
	return Reference{Name: name, Range: tokenRange}, true
}

func isDataControllerString(node *twigsyntax.Node) bool {
	attribute := twigquery.HTMLAttributeAt(node)
	return attribute != nil && strings.EqualFold(
		twigquery.HTMLAttributeName(attribute),
		"data-controller",
	)
}

func twigStringValueRange(
	node *twigsyntax.Node,
) (string, cst.TextRange) {
	literal, ok := twigast.CastTwigLiteralString(node)
	if ok {
		if inner, found := literal.GetInner(); found {
			return inner.Syntax().Text(),
				inner.Syntax().RangeTrimmedTrivia()
		}
	}
	return quotedNodeValueRange(node)
}

func htmlStringValueRange(
	node *twigsyntax.Node,
) (string, cst.TextRange) {
	literal, ok := twigast.CastHtmlString(node)
	if ok {
		if inner, found := literal.GetInner(); found {
			return inner.Syntax().Text(),
				inner.Syntax().RangeTrimmedTrivia()
		}
	}
	return quotedNodeValueRange(node)
}

func quotedNodeValueRange(
	node *twigsyntax.Node,
) (string, cst.TextRange) {
	if node == nil {
		return "", cst.TextRange{}
	}
	rng := node.RangeTrimmedTrivia()
	text := strings.TrimSpace(node.Text())
	if len(text) >= 2 &&
		(text[0] == '\'' || text[0] == '"' || text[0] == '`') &&
		text[len(text)-1] == text[0] {
		return text[1 : len(text)-1], cst.TextRange{
			Start: rng.Start + 1,
			End:   rng.End - 1,
		}
	}
	return text, rng
}

func controllerTokens(
	value string,
	base uint32,
) []Reference {
	var result []Reference
	for position := 0; position < len(value); {
		for position < len(value) && isControllerSpace(value[position]) {
			position++
		}
		start := position
		for position < len(value) && !isControllerSpace(value[position]) {
			position++
		}
		if start == position {
			continue
		}
		result = append(result, Reference{
			Name: value[start:position],
			Range: cst.TextRange{
				Start: base + uint32(start),
				End:   base + uint32(position),
			},
		})
	}
	return result
}

func controllerTokenAt(
	value string,
	base,
	offset uint32,
) (string, cst.TextRange) {
	position := int(offset - base)
	if position < 0 {
		position = 0
	}
	if position > len(value) {
		position = len(value)
	}
	start := position
	for start > 0 && !isControllerSpace(value[start-1]) {
		start--
	}
	end := position
	for end < len(value) && !isControllerSpace(value[end]) {
		end++
	}
	return value[start:end], cst.TextRange{
		Start: base + uint32(start),
		End:   base + uint32(end),
	}
}

func isControllerSpace(value byte) bool {
	switch value {
	case ' ', '\t', '\r', '\n':
		return true
	default:
		return false
	}
}

func rangeContainsCursor(rng cst.TextRange, offset uint32) bool {
	return rng.Contains(offset) || offset == rng.End
}
