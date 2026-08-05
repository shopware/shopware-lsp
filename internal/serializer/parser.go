package serializer

import (
	"strings"

	"github.com/shopware/shopware-lsp/internal/parser/cst"
	phpquery "github.com/shopware/shopware-lsp/internal/parser/php/query"
	phpsyntax "github.com/shopware/shopware-lsp/internal/parser/php/syntax"
	"github.com/shopware/shopware-lsp/internal/php"
)

func UsagesInDocument(
	path string,
	root *phpsyntax.Node,
) []Usage {
	if root == nil {
		return nil
	}
	resolver := php.NewNameResolver(root)
	var result []Usage
	for _, call := range phpquery.Calls(root) {
		if !strings.EqualFold(
			phpquery.CallMethodName(call),
			"deserialize",
		) {
			continue
		}
		target := phpquery.ArgumentExpression(call, 1)
		if target == nil {
			continue
		}
		className, rng, kind := serializerTarget(target, resolver)
		if className == "" {
			continue
		}
		result = append(result, Usage{
			Class: normalizeClass(className),
			File:  path,
			Range: rng,
			Kind:  kind,
		})
	}
	return uniqueUsages(result)
}

func UsageAt(
	root *phpsyntax.Node,
	offset uint32,
) (Usage, bool) {
	for _, usage := range UsagesInDocument("", root) {
		if usage.Range.Contains(offset) ||
			offset == usage.Range.End {
			return usage, true
		}
	}
	return Usage{}, false
}

func serializerTarget(
	target *phpsyntax.Node,
	resolver *php.NameResolver,
) (string, cst.TextRange, TargetKind) {
	if className, rng := serializerClassConstantTarget(target); className != "" {
		return resolver.Resolve(className), rng, ClassConstantTarget
	}
	literal := phpquery.StringAt(target)
	if literal == nil {
		return "", cst.TextRange{}, StringTarget
	}
	value := normalizeClass(phpquery.StringValue(literal))
	if value == "" {
		return "", cst.TextRange{}, StringTarget
	}
	return value, phpStringValueRange(literal), StringTarget
}

func serializerClassConstantTarget(
	target *phpsyntax.Node,
) (string, cst.TextRange) {
	if target == nil {
		return "", cst.TextRange{}
	}
	if target.Kind() == phpsyntax.PhpScopedAccess ||
		target.Kind() == phpsyntax.PhpMemberAccess {
		className := phpquery.ClassConstantName(target)
		if className == "" {
			return "", cst.TextRange{}
		}
		return className, target.RangeTrimmedTrivia()
	}
	if target.Kind() != phpsyntax.PhpBinaryExpression {
		return "", cst.TextRange{}
	}

	var children []*phpsyntax.Node
	for child := range target.ChildNodes() {
		children = append(children, child)
	}
	if len(children) != 2 ||
		(children[0].Kind() != phpsyntax.PhpScopedAccess &&
			children[0].Kind() != phpsyntax.PhpMemberAccess) ||
		children[1].Kind() != phpsyntax.PhpString ||
		phpquery.StringValue(children[1]) != "[]" ||
		serializerBinaryOperator(target, children[0], children[1]) != "." {
		return "", cst.TextRange{}
	}
	className := phpquery.ClassConstantName(children[0])
	if className == "" {
		return "", cst.TextRange{}
	}
	return className, children[0].RangeTrimmedTrivia()
}

func serializerBinaryOperator(
	expression, left, right *phpsyntax.Node,
) string {
	expressionRange := expression.Range()
	leftRange := left.Range()
	rightRange := right.Range()
	if leftRange.End < expressionRange.Start ||
		rightRange.Start < leftRange.End ||
		rightRange.Start > expressionRange.End {
		return ""
	}
	start := int(leftRange.End - expressionRange.Start)
	end := int(rightRange.Start - expressionRange.Start)
	text := expression.Text()
	if start < 0 || end < start || end > len(text) {
		return ""
	}
	return strings.TrimSpace(text[start:end])
}

func phpStringValueRange(node *phpsyntax.Node) cst.TextRange {
	if node == nil {
		return cst.TextRange{}
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

func uniqueUsages(usages []Usage) []Usage {
	seen := make(map[string]struct{}, len(usages))
	result := make([]Usage, 0, len(usages))
	for _, usage := range usages {
		key := strings.ToLower(usage.Class) + ":" +
			usage.File + ":" + usage.Range.String()
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		result = append(result, usage)
	}
	return result
}
