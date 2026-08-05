package binder

import (
	"strings"

	phpquery "github.com/shopware/shopware-lsp/internal/parser/php/query"
	phpsyntax "github.com/shopware/shopware-lsp/internal/parser/php/syntax"
	"github.com/shopware/shopware-lsp/internal/php/resolver"
	"github.com/shopware/shopware-lsp/internal/php/semantic"
	"github.com/shopware/shopware-lsp/internal/php/types"
)

const (
	newOptionalParameterAttribute = "Shopware\\Core\\Framework\\Deprecation\\BCChange\\NewOptionalParameter"
	newRequiredParameterAttribute = "Shopware\\Core\\Framework\\Deprecation\\BCChange\\NewRequiredParameter"
)

// bindPlannedParameters exposes Shopware's staged signature changes to call
// resolution. During the compatibility window these parameters live in
// repeatable attributes and are read through func_get_args(), so PHP's native
// parameter list alone is intentionally incomplete.
func (b *documentBuilder) bindPlannedParameters(
	node *phpsyntax.Node,
	context resolver.NameContext,
) []semantic.Parameter {
	var result []semantic.Parameter
	for _, attribute := range phpquery.Attributes(node) {
		resolved := context.ResolveClass(phpquery.AttributeName(attribute))
		optional := false
		switch {
		case strings.EqualFold(resolved, newOptionalParameterAttribute):
			optional = true
		case strings.EqualFold(resolved, newRequiredParameterAttribute):
		default:
			continue
		}

		nameNode := namedAttributeArgument(attribute, "parameterName")
		typeNode := namedAttributeArgument(attribute, "parameterType")
		name := phpquery.StringValue(nameNode)
		typeSource, ok := plannedParameterTypeSource(typeNode)
		if name == "" || !ok {
			continue
		}
		parameterType := b.bindNativeType(typeSource, context)
		if parameterType.IsUnknown() || parameterType.Kind() == types.ErrorKind {
			parameterType = types.Mixed()
		}
		name = "$" + strings.TrimPrefix(name, "$")
		attributeRange := attribute.RangeTrimmedTrivia()
		result = append(result, semantic.Parameter{
			ID: semantic.NewSymbolID(
				semantic.ParameterSymbol,
				"planned:"+name,
				b.document.Path,
				attributeRange.Start,
			),
			Name:           name,
			Type:           parameterType,
			NativeType:     parameterType,
			Range:          attributeRange,
			SelectionRange: nameNode.RangeTrimmedTrivia(),
			Flags:          semantic.SyntheticFlag,
			Optional:       optional,
		})
	}
	return result
}

func namedAttributeArgument(
	attribute *phpsyntax.Node,
	name string,
) *phpsyntax.Node {
	for index, argument := range phpquery.Arguments(attribute) {
		if strings.EqualFold(phpquery.ArgumentName(argument), name) {
			return phpquery.ArgumentExpression(attribute, index)
		}
	}
	return nil
}

func plannedParameterTypeSource(node *phpsyntax.Node) (string, bool) {
	if node == nil {
		return "", false
	}
	switch node.Kind() {
	case phpsyntax.PhpString:
		return phpquery.StringValue(node), true
	case phpsyntax.PhpScopedAccess, phpsyntax.PhpMemberAccess:
		name := phpquery.ClassConstantName(node)
		return name, name != ""
	case phpsyntax.PhpParenthesized:
		for index := 0; index < node.ChildCount(); index++ {
			if child, ok := node.Child(index).(*phpsyntax.Node); ok {
				return plannedParameterTypeSource(child)
			}
		}
	case phpsyntax.PhpBinaryExpression:
		var builder strings.Builder
		operands := 0
		for index := 0; index < node.ChildCount(); index++ {
			switch child := node.Child(index).(type) {
			case *phpsyntax.Node:
				part, ok := plannedParameterTypeSource(child)
				if !ok {
					return "", false
				}
				builder.WriteString(part)
				operands++
			case *phpsyntax.Token:
				if child.Kind().IsTrivia() {
					continue
				}
				if child.Kind() != phpsyntax.TkOperator ||
					strings.TrimSpace(child.Text()) != "." {
					return "", false
				}
			}
		}
		return builder.String(), operands >= 2
	}
	return "", false
}
