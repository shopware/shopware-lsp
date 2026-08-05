package twig

import (
	"strings"

	"github.com/shopware/shopware-lsp/internal/parser/cst"
	twigquery "github.com/shopware/shopware-lsp/internal/parser/twig/query"
	twigsyntax "github.com/shopware/shopware-lsp/internal/parser/twig/syntax"
)

type EnumReference struct {
	Name  string
	Node  *twigsyntax.Node
	Range cst.TextRange
}

func EnumReferenceAt(node *twigsyntax.Node) (EnumReference, bool) {
	literal := twigquery.LiteralStringAt(node)
	return enumReference(literal)
}

func EnumCompletionAt(node *twigsyntax.Node) bool {
	literal := twigquery.LiteralStringAt(node)
	return literal != nil &&
		twigquery.StringInFunction(literal, "enum", "enum_cases") &&
		twigquery.FunctionArgumentIndex(literal) == 0 &&
		twigquery.StringIsStatic(literal)
}

func EnumReferences(root *twigsyntax.Node) []EnumReference {
	var result []EnumReference
	for _, literal := range twigquery.StringArgumentsInFunctions(
		root,
		"enum",
		"enum_cases",
	) {
		if reference, found := enumReference(literal); found {
			result = append(result, reference)
		}
	}
	return result
}

func enumReference(
	literal *twigsyntax.Node,
) (EnumReference, bool) {
	if !EnumCompletionAt(literal) {
		return EnumReference{}, false
	}
	raw := twigquery.StringValue(literal)
	name := unescapeTwigClassName(raw)
	if name == "" {
		return EnumReference{}, false
	}
	start := literal.RangeTrimmedTrivia().Start
	if relative := strings.Index(literal.Text(), raw); relative >= 0 {
		start = literal.Range().Start + uint32(relative)
	}
	return EnumReference{
		Name: name,
		Node: literal,
		Range: cst.TextRange{
			Start: start,
			End:   start + uint32(len(raw)),
		},
	}, true
}

func unescapeTwigClassName(value string) string {
	value = strings.TrimPrefix(strings.TrimSpace(value), `\`)
	return strings.ReplaceAll(value, `\\`, `\`)
}

func EscapeTwigClassName(value string) string {
	return strings.ReplaceAll(strings.TrimPrefix(value, `\`), `\`, `\\`)
}
