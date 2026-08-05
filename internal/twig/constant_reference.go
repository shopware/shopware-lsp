package twig

import (
	"sort"
	"strings"

	"github.com/shopware/shopware-lsp/internal/parser/cst"
	twigquery "github.com/shopware/shopware-lsp/internal/parser/twig/query"
	twigsyntax "github.com/shopware/shopware-lsp/internal/parser/twig/syntax"
	"github.com/shopware/shopware-lsp/internal/php/types"
)

// ConstantReference identifies one static PHP constant used through Twig's
// constant() function. Class is empty for global constants.
type ConstantReference struct {
	Class    string
	Name     string
	FilePath string
	Range    cst.TextRange
}

// ConstantUsageCatalog groups references under a directly queryable semantic
// key in the persistent Twig index.
type ConstantUsageCatalog struct {
	Key        string
	References []ConstantReference
}

// ConstantCompletionContext describes the first argument of constant().
type ConstantCompletionContext struct {
	Value           string
	Class           string
	ReceiverClasses []string
	ObjectArgument  bool
	ContentRange    cst.TextRange
}

func ConstantReferenceKey(reference ConstantReference) string {
	name := strings.ToLower(strings.TrimSpace(reference.Name))
	if name == "" {
		return ""
	}
	class := strings.ToLower(normalizeTwigConstantClass(reference.Class))
	if class == "" {
		return "global\x00" + name
	}
	return "class\x00" + class + "\x00" + name
}

func ConstantCompletionContextAt(
	templatePath string,
	root,
	node *twigsyntax.Node,
	resolver PHPAccessResolver,
) (ConstantCompletionContext, bool) {
	literal := twigquery.LiteralStringAt(node)
	if !constantStringLiteral(literal) {
		return ConstantCompletionContext{}, false
	}
	value := unescapeTwigConstant(twigquery.StringValue(literal))
	context := ConstantCompletionContext{
		Value:        value,
		ContentRange: twigStringContentRange(literal),
	}
	if separator := strings.LastIndex(value, "::"); separator >= 0 {
		context.Class = normalizeTwigConstantClass(value[:separator])
		return context, true
	}
	context.ObjectArgument =
		twigquery.FunctionArgument(
			twigquery.FunctionCallAt(literal),
			1,
		) != nil
	context.ReceiverClasses = constantReceiverClasses(
		templatePath,
		root,
		literal,
		resolver,
	)
	return context, true
}

func ConstantReferencesAt(
	templatePath string,
	root,
	node *twigsyntax.Node,
	resolver PHPAccessResolver,
) []ConstantReference {
	literal := twigquery.LiteralStringAt(node)
	if !constantStringLiteral(literal) {
		return nil
	}
	return constantReferencesForLiteral(
		templatePath,
		root,
		literal,
		resolver,
	)
}

func ConstantReferencesInDocument(
	templatePath string,
	root *twigsyntax.Node,
	resolver PHPAccessResolver,
) []ConstantReference {
	if root == nil {
		return nil
	}
	resolver = resolver.forDocument(root)
	var result []ConstantReference
	for _, literal := range twigquery.StringArgumentsInFunctions(
		root,
		"constant",
	) {
		result = append(
			result,
			constantReferencesForLiteral(
				templatePath,
				root,
				literal,
				resolver,
			)...,
		)
	}
	return uniqueConstantReferences(result)
}

func constantReferencesForLiteral(
	templatePath string,
	root,
	literal *twigsyntax.Node,
	resolver PHPAccessResolver,
) []ConstantReference {
	if !constantStringLiteral(literal) {
		return nil
	}
	value := unescapeTwigConstant(twigquery.StringValue(literal))
	if value == "" {
		return nil
	}
	rng := twigStringContentRange(literal)
	if separator := strings.LastIndex(value, "::"); separator >= 0 {
		class := normalizeTwigConstantClass(value[:separator])
		name := strings.TrimSpace(value[separator+2:])
		if class == "" || name == "" {
			return nil
		}
		return []ConstantReference{{
			Class:    class,
			Name:     name,
			FilePath: templatePath,
			Range:    rng,
		}}
	}
	classes := constantReceiverClasses(
		templatePath,
		root,
		literal,
		resolver,
	)
	if len(classes) != 0 {
		result := make([]ConstantReference, 0, len(classes))
		for _, class := range classes {
			result = append(result, ConstantReference{
				Class:    class,
				Name:     value,
				FilePath: templatePath,
				Range:    rng,
			})
		}
		return result
	}
	if twigquery.FunctionArgument(
		twigquery.FunctionCallAt(literal),
		1,
	) != nil {
		return nil
	}
	return []ConstantReference{{
		Name:     normalizeTwigConstantClass(value),
		FilePath: templatePath,
		Range:    rng,
	}}
}

func constantStringLiteral(literal *twigsyntax.Node) bool {
	return literal != nil &&
		twigquery.StringIsStatic(literal) &&
		twigquery.StringInFunction(literal, "constant") &&
		twigquery.FunctionArgumentIndex(literal) == 0
}

func constantReceiverClasses(
	templatePath string,
	root,
	literal *twigsyntax.Node,
	resolver PHPAccessResolver,
) []string {
	call := twigquery.FunctionCallAt(literal)
	argument := twigquery.FunctionArgument(call, 1)
	if !constantObjectExpressionAllowed(argument) {
		return nil
	}
	value := resolver.expressionType(templatePath, root, argument)
	seen := make(map[string]struct{})
	var result []string
	var collect func(types.Type)
	collect = func(current types.Type) {
		switch current.Kind() {
		case types.ObjectKind:
			name := normalizeTwigConstantClass(current.Name())
			key := strings.ToLower(name)
			if name == "" {
				return
			}
			if _, exists := seen[key]; exists {
				return
			}
			seen[key] = struct{}{}
			result = append(result, name)
		case types.UnionKind, types.IntersectionKind:
			for index := 0; index < current.ArgumentCount(); index++ {
				collect(current.Argument(index))
			}
		}
	}
	collect(value)
	sort.Slice(result, func(left, right int) bool {
		return compareFold(result[left], result[right]) < 0
	})
	return result
}

func constantObjectExpressionAllowed(node *twigsyntax.Node) bool {
	for node != nil {
		switch node.Kind() {
		case twigsyntax.TwigExpression, twigsyntax.TwigOperand,
			twigsyntax.TwigParenthesesExpression:
			node = firstTwigChild(node)
		case twigsyntax.TwigLiteralName:
			return true
		case twigsyntax.TwigAccessor:
			operands := directTwigChildren(
				node,
				twigsyntax.TwigOperand,
			)
			if len(operands) == 0 {
				return false
			}
			node = firstTwigChild(operands[0])
		default:
			return false
		}
	}
	return false
}

func normalizeTwigConstantClass(value string) string {
	return strings.Trim(strings.TrimSpace(value), `\`)
}

func unescapeTwigConstant(value string) string {
	value = strings.TrimSpace(value)
	return strings.ReplaceAll(value, `\\`, `\`)
}

func twigStringContentRange(
	literal *twigsyntax.Node,
) cst.TextRange {
	if literal == nil {
		return cst.TextRange{}
	}
	raw := twigquery.StringValue(literal)
	rng := literal.RangeTrimmedTrivia()
	if relative := strings.Index(literal.Text(), raw); relative >= 0 {
		rng.Start = literal.Range().Start + uint32(relative)
		rng.End = rng.Start + uint32(len(raw))
	}
	return rng
}

func uniqueConstantReferences(
	references []ConstantReference,
) []ConstantReference {
	seen := make(map[string]struct{}, len(references))
	result := make([]ConstantReference, 0, len(references))
	for _, reference := range references {
		key := ConstantReferenceKey(reference) + "\x00" +
			reference.FilePath + "\x00" + reference.Range.String()
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		result = append(result, reference)
	}
	sort.Slice(result, func(left, right int) bool {
		if result[left].FilePath != result[right].FilePath {
			return result[left].FilePath < result[right].FilePath
		}
		if result[left].Range.Start != result[right].Range.Start {
			return result[left].Range.Start < result[right].Range.Start
		}
		return ConstantReferenceKey(result[left]) <
			ConstantReferenceKey(result[right])
	})
	return result
}
