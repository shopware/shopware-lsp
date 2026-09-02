package symfony

import (
	"strings"

	"github.com/shopware/shopware-lsp/internal/parser/cst"
	twigast "github.com/shopware/shopware-lsp/internal/parser/twig/ast"
	twigquery "github.com/shopware/shopware-lsp/internal/parser/twig/query"
	twigsyntax "github.com/shopware/shopware-lsp/internal/parser/twig/syntax"
)

// TwigControllerValue is a statically knowable first argument of Twig's
// controller() function. Range covers the string contents without quotes.
type TwigControllerValue struct {
	Value string
	Node  *cst.Node
	Range cst.TextRange
}

// TwigControllerReference is a parsed controller() argument and its source.
type TwigControllerReference struct {
	ControllerReference
	Node  *cst.Node
	Range cst.TextRange
}

// NormalizeTwigControllerValue mirrors Twig's escaped namespace spelling and
// the reference plugin's controller index normalization.
func NormalizeTwigControllerValue(value string) string {
	value = strings.ReplaceAll(strings.TrimSpace(value), `\\`, `\`)
	return strings.TrimLeft(value, `\`)
}

// ControllerReferenceKey is the stable, case-insensitive usage-index key.
func ControllerReferenceKey(reference ControllerReference) string {
	return strings.ToLower(
		strings.TrimLeft(strings.TrimSpace(reference.Target), `\`) +
			"::" + strings.TrimSpace(reference.Method),
	)
}

// TwigControllerValues returns direct static first arguments of controller().
func TwigControllerValues(root *twigsyntax.Node) []TwigControllerValue {
	if root == nil {
		return nil
	}
	var result []TwigControllerValue
	for call := range twigquery.IterateNodes(root, twigsyntax.TwigFunctionCall) {
		if !strings.EqualFold(twigquery.FunctionName(call), "controller") {
			continue
		}
		literal := twigquery.StringArgument(call, 0)
		if literal == nil || !twigquery.StringIsStatic(literal) {
			continue
		}
		result = append(result, TwigControllerValue{
			Value: NormalizeTwigControllerValue(
				twigquery.StringValue(literal),
			),
			Node:  literal,
			Range: twigControllerStringRange(literal),
		})
	}
	return result
}

// TwigControllerReferences returns every parseable static controller() usage.
func TwigControllerReferences(root *twigsyntax.Node) []TwigControllerReference {
	values := TwigControllerValues(root)
	result := make([]TwigControllerReference, 0, len(values))
	for _, value := range values {
		reference, ok := ParseTwigControllerReference(value.Value)
		if !ok {
			continue
		}
		result = append(result, TwigControllerReference{
			ControllerReference: reference,
			Node:                value.Node,
			Range:               value.Range,
		})
	}
	return result
}

// TwigControllerValueAt reports the controller() string containing node. It
// intentionally accepts an empty value so completion works inside controller(”).
func TwigControllerValueAt(node *twigsyntax.Node) (TwigControllerValue, bool) {
	literal := twigquery.LiteralStringAt(node)
	if literal == nil || !twigquery.StringIsStatic(literal) {
		return TwigControllerValue{}, false
	}
	call := twigquery.FunctionCallAt(literal)
	if call == nil ||
		!strings.EqualFold(twigquery.FunctionName(call), "controller") ||
		twigquery.StringArgument(call, 0) != literal {
		return TwigControllerValue{}, false
	}
	return TwigControllerValue{
		Value: NormalizeTwigControllerValue(twigquery.StringValue(literal)),
		Node:  literal,
		Range: twigControllerStringRange(literal),
	}, true
}

func TwigControllerReferenceAt(
	node *twigsyntax.Node,
) (TwigControllerReference, bool) {
	value, ok := TwigControllerValueAt(node)
	if !ok {
		return TwigControllerReference{}, false
	}
	reference, ok := ParseTwigControllerReference(value.Value)
	if !ok {
		return TwigControllerReference{}, false
	}
	return TwigControllerReference{
		ControllerReference: reference,
		Node:                value.Node,
		Range:               value.Range,
	}, true
}

// ParseTwigControllerReference additionally accepts Symfony's legacy
// Bundle:Controller:action shortcut. The generic route parser intentionally
// remains strict because resolving this spelling requires PHP bundle context.
func ParseTwigControllerReference(
	value string,
) (ControllerReference, bool) {
	if reference, ok := ParseControllerReference(value); ok {
		return reference, true
	}
	value = strings.TrimSpace(value)
	parts := strings.Split(value, ":")
	if len(parts) != 3 {
		return ControllerReference{}, false
	}
	for index := range parts {
		parts[index] = strings.TrimSpace(parts[index])
		if parts[index] == "" ||
			strings.ContainsAny(parts[index], "%${}") {
			return ControllerReference{}, false
		}
	}
	return ControllerReference{
		Value:  value,
		Target: parts[0] + ":" + parts[1],
		Method: parts[2],
	}, true
}

func twigControllerStringRange(node *twigsyntax.Node) cst.TextRange {
	if node == nil {
		return cst.TextRange{}
	}
	literal, ok := twigast.CastTwigLiteralString(node)
	if ok {
		if inner, found := literal.GetInner(); found {
			return inner.Syntax().RangeTrimmedTrivia()
		}
	}
	rng := node.RangeTrimmedTrivia()
	text := strings.TrimSpace(node.Text())
	if len(text) >= 2 &&
		(text[0] == '\'' || text[0] == '"') &&
		text[len(text)-1] == text[0] &&
		rng.End > rng.Start+1 {
		rng.Start++
		rng.End--
	}
	return rng
}
