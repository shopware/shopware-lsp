package binder

import (
	"strconv"
	"strings"

	phpquery "github.com/shopware/shopware-lsp/internal/parser/php/query"
	phpsyntax "github.com/shopware/shopware-lsp/internal/parser/php/syntax"
	"github.com/shopware/shopware-lsp/internal/php/resolver"
	"github.com/shopware/shopware-lsp/internal/php/semantic"
	"github.com/shopware/shopware-lsp/internal/php/types"
	"github.com/shopware/shopware-lsp/internal/textutil"
)

func declarationVisibility(node *phpsyntax.Node) semantic.Visibility {
	if node == nil {
		return semantic.Public
	}
	for index := 0; index < node.ChildCount(); index++ {
		element := node.Child(index)
		token, ok := element.(*phpsyntax.Token)
		if !ok {
			continue
		}
		switch {
		case strings.EqualFold(token.Text(), "private"):
			return semantic.Private
		case strings.EqualFold(token.Text(), "protected"):
			return semantic.Protected
		case strings.EqualFold(token.Text(), "public"):
			return semantic.Public
		}
	}
	return semantic.Public
}

func declarationWriteVisibility(node *phpsyntax.Node) (semantic.Visibility, bool) {
	if node == nil {
		return semantic.Public, false
	}
	for index := 0; index < node.ChildCount(); index++ {
		element := node.Child(index)
		token, ok := element.(*phpsyntax.Token)
		if !ok {
			continue
		}
		var visibility semantic.Visibility
		switch {
		case strings.EqualFold(token.Text(), "private"):
			visibility = semantic.Private
		case strings.EqualFold(token.Text(), "protected"):
			visibility = semantic.Protected
		case strings.EqualFold(token.Text(), "public"):
			visibility = semantic.Public
		default:
			continue
		}
		if hasSetSuffix(node, index+1) {
			return visibility, true
		}
	}
	return semantic.Public, false
}

func hasSetSuffix(node *phpsyntax.Node, start int) bool {
	matched := 0
	expected := [...]string{"(", "set", ")"}
	for index := start; index < node.ChildCount(); index++ {
		element := node.Child(index)
		token, ok := element.(*phpsyntax.Token)
		if !ok || token.Kind().IsTrivia() {
			continue
		}
		if !strings.EqualFold(token.Text(), expected[matched]) {
			return false
		}
		matched++
		if matched == len(expected) {
			return true
		}
	}
	return false
}

func declarationFlags(node *phpsyntax.Node) semantic.Flags {
	var flags semantic.Flags
	for index := 0; index < node.ChildCount(); index++ {
		element := node.Child(index)
		token, ok := element.(*phpsyntax.Token)
		if !ok {
			continue
		}
		switch {
		case strings.EqualFold(token.Text(), "static"):
			flags |= semantic.StaticFlag
		case strings.EqualFold(token.Text(), "final"):
			flags |= semantic.FinalFlag
		case strings.EqualFold(token.Text(), "abstract"):
			flags |= semantic.AbstractFlag
		case strings.EqualFold(token.Text(), "readonly"):
			flags |= semantic.ReadonlyFlag
		case token.Kind() == phpsyntax.TkAmpersand:
			flags |= semantic.ByReferenceFlag
		}
	}
	if node.Kind() == phpsyntax.PhpParameter &&
		(hasToken(node, phpsyntax.TkKeyword, "public") ||
			hasToken(node, phpsyntax.TkKeyword, "protected") ||
			hasToken(node, phpsyntax.TkKeyword, "private")) {
		flags |= semantic.PromotedFlag
	}
	return flags
}

func hasToken(node *phpsyntax.Node, kind phpsyntax.Kind, text string) bool {
	if node == nil {
		return false
	}
	for index := 0; index < node.ChildCount(); index++ {
		element := node.Child(index)
		token, ok := element.(*phpsyntax.Token)
		if !ok || token.Kind() != kind {
			continue
		}
		if text == "" || strings.EqualFold(token.Text(), text) {
			return true
		}
	}
	return false
}

func bindAttributes(node *phpsyntax.Node, context resolver.NameContext) []semantic.Attribute {
	var result []semantic.Attribute
	if node == nil {
		return result
	}
	for index := 0; index < node.ChildCount(); index++ {
		child, ok := node.Child(index).(*phpsyntax.Node)
		if !ok || child.Kind() != phpsyntax.PhpAttributeGroup {
			continue
		}
		for attributeIndex := 0; attributeIndex < child.ChildCount(); attributeIndex++ {
			attribute, ok := child.Child(attributeIndex).(*phpsyntax.Node)
			if !ok || attribute.Kind() != phpsyntax.PhpAttribute {
				continue
			}
			nameNode := firstDirect(attribute, phpsyntax.PhpName)
			if nameNode == nil {
				continue
			}
			name := context.ResolveClass(compactName(nameNode.Text()))
			var arguments []semantic.AttributeArgument
			if retainAttributeArguments(name) {
				arguments = bindAttributeArguments(attribute, context)
			}
			result = append(result, semantic.Attribute{
				Name:      name,
				Arguments: arguments,
				Range:     attribute.RangeTrimmedTrivia(),
			})
		}
	}
	return result
}

func retainAttributeArguments(name string) bool {
	name = strings.ToLower(strings.Trim(name, "\\"))
	for _, suffix := range [...]string{
		"arrayshape",
		"deprecated",
		"expectedvalues",
		"noreturn",
		"objectshape",
		"returntypecontract",
	} {
		if name == suffix || strings.HasSuffix(name, "\\"+suffix) {
			return true
		}
	}
	return false
}

func bindAttributeArguments(
	attribute *phpsyntax.Node,
	context resolver.NameContext,
) []semantic.AttributeArgument {
	iterator := phpquery.IterateArguments(attribute)
	if iterator.Len() == 0 {
		return nil
	}
	result := make([]semantic.AttributeArgument, 0, iterator.Len())
	for iterator.Next() {
		argument := iterator.Node()
		expression := phpquery.ArgumentExpression(attribute, len(result))
		if expression == nil {
			continue
		}
		result = append(result, semantic.AttributeArgument{
			Name:  phpquery.ArgumentName(argument),
			Value: bindAttributeValue(expression, context, 0),
			Range: argument.RangeTrimmedTrivia(),
		})
	}
	return result
}

const maxAttributeValueDepth = 16

func bindAttributeValue(
	node *phpsyntax.Node,
	context resolver.NameContext,
	depth int,
) semantic.AttributeValue {
	if node == nil {
		return semantic.AttributeValue{}
	}
	expression := strings.TrimSpace(node.Text())
	result := semantic.AttributeValue{
		Kind:       semantic.AttributeValueExpression,
		Value:      expression,
		Expression: expression,
	}
	if depth >= maxAttributeValueDepth {
		return result
	}
	switch node.Kind() {
	case phpsyntax.PhpString:
		result.Kind = semantic.AttributeValueString
		result.Value = phpquery.StringValue(node)
	case phpsyntax.PhpNumber:
		result.Kind = semantic.AttributeValueInteger
		if numericLiteralIsFloat(expression) {
			result.Kind = semantic.AttributeValueFloat
		}
	case phpsyntax.PhpUnaryExpression:
		switch constantLiteralType(node).Kind() {
		case types.LiteralIntKind:
			result.Kind = semantic.AttributeValueInteger
		case types.LiteralFloatKind:
			result.Kind = semantic.AttributeValueFloat
		}
	case phpsyntax.PhpBoolean:
		result.Kind = semantic.AttributeValueBool
		result.Value = strings.ToLower(expression)
	case phpsyntax.PhpNull:
		result.Kind = semantic.AttributeValueNull
		result.Value = "null"
	case phpsyntax.PhpName:
		result.Kind = semantic.AttributeValueConstant
		name := compactName(node.Text())
		resolved := context.ResolveConstant(name)
		if len(resolved) != 0 {
			// Unqualified constants in a namespace have a global fallback. The
			// last candidate is therefore the stable identity used by stubs.
			result.Value = resolved[len(resolved)-1]
		} else {
			result.Value = strings.TrimPrefix(name, "\\")
		}
	case phpsyntax.PhpScopedAccess:
		result.Kind = semantic.AttributeValueClassConstant
		result.Value = resolveAttributeClassConstant(expression, context)
	case phpsyntax.PhpArray:
		result.Kind = semantic.AttributeValueArray
		result.Value = ""
		items := phpquery.ArrayItems(node)
		if len(items) != 0 {
			result.Items = make([]semantic.AttributeArrayItem, 0, len(items))
		}
		for _, item := range items {
			valueNode := phpquery.ArrayItemValue(item)
			if valueNode == nil {
				continue
			}
			bound := semantic.AttributeArrayItem{
				Value: bindAttributeValue(valueNode, context, depth+1),
			}
			if keyNode := phpquery.ArrayItemKey(item); keyNode != nil {
				bound.HasKey = true
				bound.Key = bindAttributeValue(keyNode, context, depth+1)
			}
			result.Items = append(result.Items, bound)
		}
	default:
		if strings.Contains(expression, "::") &&
			!strings.ContainsAny(expression, "|&.+-*/%?()[]{}'\"") {
			result.Kind = semantic.AttributeValueClassConstant
			result.Value = resolveAttributeClassConstant(expression, context)
		}
	}
	return result
}

func resolveAttributeClassConstant(
	expression string,
	context resolver.NameContext,
) string {
	separator := strings.LastIndex(expression, "::")
	if separator < 0 {
		return strings.TrimPrefix(expression, "\\")
	}
	receiver := strings.TrimSpace(expression[:separator])
	member := strings.TrimSpace(expression[separator+2:])
	if receiver == "" || member == "" {
		return strings.TrimPrefix(expression, "\\")
	}
	switch strings.ToLower(receiver) {
	case "self", "static", "parent":
		return receiver + "::" + member
	default:
		return context.ResolveClass(receiver) + "::" + member
	}
}

func applyAttributeTypeSemantics(
	symbol *semantic.Symbol,
	context resolver.NameContext,
) {
	if symbol == nil {
		return
	}
	if shape, ok := attributeShapeType(symbol.Attributes, context); ok {
		switch symbol.Kind {
		case semantic.FunctionSymbol, semantic.MethodSymbol:
			symbol.ReturnType = refineTypeWithShape(symbol.ReturnType, shape)
		default:
			symbol.Type = refineTypeWithShape(symbol.Type, shape)
		}
	}
	if noReturn, ok := semantic.AttributeNamed(symbol.Attributes, "NoReturn"); ok &&
		len(noReturn.Arguments) == 0 &&
		(symbol.Kind == semantic.FunctionSymbol ||
			symbol.Kind == semantic.MethodSymbol) {
		symbol.ReturnType = types.Never()
	}
}

func attributeRefinedType(
	fallback types.Type,
	attributes []semantic.Attribute,
	context resolver.NameContext,
) types.Type {
	if shape, ok := attributeShapeType(attributes, context); ok {
		return refineTypeWithShape(fallback, shape)
	}
	return fallback
}

func refineTypeWithShape(fallback, shape types.Type) types.Type {
	if fallback.IsUnknown() || fallback.Kind() == types.MixedKind {
		return shape
	}
	if shapeReplacesType(fallback, shape.Kind()) {
		return shape
	}
	if fallback.Kind() != types.UnionKind {
		return shape
	}
	members := fallback.Arguments()
	replaced := false
	for index := range members {
		if shapeReplacesType(members[index], shape.Kind()) {
			members[index] = shape
			replaced = true
		}
	}
	if !replaced {
		return shape
	}
	return types.Union(members...)
}

func shapeReplacesType(value types.Type, shapeKind types.Kind) bool {
	switch shapeKind {
	case types.ArrayShapeKind:
		switch value.Kind() {
		case types.ArrayKind, types.NonEmptyArrayKind,
			types.ListKind, types.NonEmptyListKind,
			types.ArrayShapeKind:
			return true
		}
	case types.ObjectShapeKind:
		return value.Kind() == types.ObjectShapeKind ||
			value.Kind() == types.ObjectKind && value.Name() == ""
	}
	return false
}

func attributeShapeType(
	attributes []semantic.Attribute,
	context resolver.NameContext,
) (types.Type, bool) {
	attribute, found := semantic.AttributeNamed(attributes, "ArrayShape")
	object := false
	if !found {
		attribute, found = semantic.AttributeNamed(attributes, "ObjectShape")
		object = found
	}
	if !found {
		return types.Type{}, false
	}
	shape, found := attribute.Argument("shape", 0)
	if !found || shape.Kind != semantic.AttributeValueArray {
		return types.Type{}, false
	}
	fields := make([]types.ShapeField, 0, len(shape.Items))
	nextNumericKey := 0
	for _, item := range shape.Items {
		name := ""
		optional := false
		if item.HasKey {
			switch item.Key.Kind {
			case semantic.AttributeValueString:
				name = item.Key.Value
				if strings.HasSuffix(name, "?") {
					name = strings.TrimSuffix(name, "?")
					optional = true
				}
			case semantic.AttributeValueInteger:
				name = strings.ReplaceAll(item.Key.Expression, "_", "")
				if parsed, err := strconv.Atoi(name); err == nil &&
					parsed >= nextNumericKey {
					nextNumericKey = parsed + 1
				}
			}
		} else if !object {
			name = strconv.Itoa(nextNumericKey)
			nextNumericKey++
		}
		if name == "" || item.Value.Kind != semantic.AttributeValueString {
			continue
		}
		fieldType, err := types.Parse(item.Value.Value)
		if err != nil || fieldType.IsUnknown() ||
			fieldType.Kind() == types.ErrorKind {
			fieldType = types.Mixed()
		} else {
			fieldType = context.ResolvePHPDocType(fieldType, nil)
		}
		fields = append(fields, types.ShapeField{
			Name:     name,
			Type:     fieldType,
			Optional: optional,
		})
	}
	if len(fields) == 0 && len(shape.Items) != 0 {
		return types.Type{}, false
	}
	if object {
		return types.ObjectShape(fields, false), true
	}
	return types.ArrayShape(fields, false), true
}

func firstDirect(node *phpsyntax.Node, kind phpsyntax.Kind) *phpsyntax.Node {
	if node == nil {
		return nil
	}
	for index := 0; index < node.ChildCount(); index++ {
		child, ok := node.Child(index).(*phpsyntax.Node)
		if ok && child.Kind() == kind {
			return child
		}
	}
	return nil
}

func compactName(source string) string {
	return textutil.CompactWhitespace(source)
}
