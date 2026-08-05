package inference

import (
	"strings"

	phpquery "github.com/shopware/shopware-lsp/internal/parser/php/query"
	phpsyntax "github.com/shopware/shopware-lsp/internal/parser/php/syntax"
	"github.com/shopware/shopware-lsp/internal/php/resolver"
	"github.com/shopware/shopware-lsp/internal/php/semantic"
	"github.com/shopware/shopware-lsp/internal/php/types"
)

// Builtins covers stable core PHP signatures that are useful even before a
// versioned stub package has been loaded.
var Builtins Extension = ExtensionFunc(func(context CallContext) (semantic.TypeFact, bool) {
	name := strings.ToLower(strings.TrimPrefix(context.Name, "\\"))
	relations := types.Relations{}
	if context.Snapshot != nil {
		relations = context.Snapshot.Relations()
	}
	var value types.Type
	switch name {
	case "count", "strlen", "mb_strlen", "array_push", "array_unshift":
		value = types.Int()
	case "strtolower", "strtoupper", "mb_strtolower", "mb_strtoupper":
		value = types.String()
	case "tempnam":
		// Internal false-return unions are benevolent in PHPStan. Consumers may
		// use the successful scalar arm without an argument/return diagnostic.
		value = types.String()
	case "is_string", "is_int", "is_integer", "is_float", "is_double",
		"is_bool", "is_array", "is_callable", "is_iterable", "is_object",
		"is_resource", "is_null", "isset", "empty":
		value = types.Bool()
	case "array_values":
		if len(context.Arguments) > 0 {
			_, element := iterableTypes(context.Arguments[0].Type, relations)
			if arrayTypeKnownNonEmpty(context.Arguments[0].Type) {
				value = types.NonEmptyList(element)
			} else {
				value = types.List(element)
			}
		}
	case "array_reverse":
		if len(context.Arguments) > 0 {
			value = context.Arguments[0].Type
		}
	case "array_column":
		if len(context.Arguments) > 1 {
			_, row := iterableTypes(context.Arguments[0].Type, relations)
			column, alwaysPresent := arrayColumnType(
				row,
				context.Arguments[1].Type,
				relations,
			)
			if !column.IsUnknown() {
				if len(context.Arguments) == 2 {
					if alwaysPresent && arrayTypeKnownNonEmpty(context.Arguments[0].Type) {
						value = types.NonEmptyList(column)
					} else {
						value = types.List(column)
					}
				} else {
					value = types.Array(types.ArrayKey(), column)
				}
			}
		}
	case "array_keys":
		if len(context.Arguments) > 0 {
			key, _ := iterableTypes(context.Arguments[0].Type, relations)
			if arrayTypeKnownNonEmpty(context.Arguments[0].Type) {
				value = types.NonEmptyList(key)
			} else {
				value = types.List(key)
			}
		}
	case "array_flip":
		if len(context.Arguments) > 0 {
			key, element := iterableTypes(context.Arguments[0].Type, relations)
			if !key.IsUnknown() && !element.IsUnknown() &&
				relations.IsSubtype(element, types.ArrayKey()) {
				value = types.Array(element, key)
			}
		}
	case "iterator_to_array":
		if len(context.Arguments) > 0 {
			key, element := iterableTypes(context.Arguments[0].Type, relations)
			preserveKeys := true
			if len(context.Arguments) > 1 &&
				context.Arguments[1].Type.Kind() == types.FalseKind {
				preserveKeys = false
			}
			if preserveKeys {
				value = types.Array(key, element)
			} else {
				value = types.List(element)
			}
		}
	case "array_map":
		if len(context.Arguments) > 1 {
			mapped := arrayMapResultType(context)
			if !mapped.IsUnknown() {
				if len(context.Arguments) > 2 {
					value = types.List(mapped)
				} else {
					key, _ := iterableTypes(context.Arguments[1].Type, relations)
					if !key.IsUnknown() && relations.IsSubtype(key, types.Int()) {
						value = types.List(mapped)
					} else {
						value = types.Array(key, mapped)
					}
				}
			}
		}
	case "array_sum":
		if len(context.Arguments) > 0 {
			_, element := iterableTypes(context.Arguments[0].Type, relations)
			if !element.IsUnknown() && relations.IsSubtype(element, types.Int()) {
				value = types.Int()
			}
		}
	case "array_reduce":
		if len(context.Arguments) > 1 &&
			context.Arguments[1].Type.Kind() == types.CallableKind {
			value = context.Arguments[1].Type.Result()
			if len(context.Arguments) > 2 {
				value = relations.Join(value, context.Arguments[2].Type)
			} else {
				value = types.Nullable(value)
			}
		}
	case "array_search":
		if len(context.Arguments) > 1 {
			key, _ := iterableTypes(context.Arguments[1].Type, relations)
			value = types.Union(key, types.False())
		}
	case "explode":
		if len(context.Arguments) > 1 {
			value = types.List(types.String())
		}
	case "ini_get":
		if len(context.Arguments) > 0 &&
			knownCoreINIOption(context.Arguments[0].Type) {
			value = types.String()
		}
	case "parse_url":
		value = parseURLComponentReturnType(context.Node)
	case "pathinfo":
		if pathinfoReturnsString(context.Node) {
			value = types.String()
		}
	case "array_first", "array_last":
		if len(context.Arguments) > 0 {
			_, element := iterableTypes(context.Arguments[0].Type, relations)
			if !element.IsUnknown() {
				if context.Arguments[0].Type.Kind() == types.NonEmptyListKind ||
					arraySourceKnownNonEmpty(context.Node) {
					value = element
				} else {
					value = types.Nullable(element)
				}
			}
		}
	case "array_shift", "array_pop":
		if len(context.Arguments) > 0 {
			_, element := iterableTypes(context.Arguments[0].Type, relations)
			if !element.IsUnknown() {
				if context.Arguments[0].Type.Kind() == types.NonEmptyListKind ||
					arraySourceKnownNonEmpty(context.Node) {
					value = element
				} else {
					value = types.Nullable(element)
				}
			}
		}
	case "version_compare":
		if len(context.Arguments) >= 3 {
			value = types.Bool()
		} else {
			value = types.Int()
		}
	case "str_replace", "str_ireplace", "substr_replace":
		subject := 2
		if name == "substr_replace" {
			subject = 0
		}
		if len(context.Arguments) > subject {
			value = replacementSubjectType(
				context.Arguments[subject].Type,
				relations,
			)
		}
	case "preg_replace", "preg_filter", "preg_replace_callback":
		if len(context.Arguments) > 2 {
			result := replacementSubjectType(
				context.Arguments[2].Type,
				relations,
			)
			if !result.IsUnknown() {
				value = types.Nullable(result)
			}
		}
	case "preg_split":
		value = types.Union(types.List(types.String()), types.False())
	case "preg_replace_callback_array":
		if len(context.Arguments) > 1 {
			result := replacementSubjectType(
				context.Arguments[1].Type,
				relations,
			)
			if !result.IsUnknown() {
				value = types.Nullable(result)
			}
		}
	case "array_filter":
		if len(context.Arguments) > 0 {
			callback := phpquery.ArgumentExpression(
				context.Node,
				1,
			)
			if shape, ok := arrayFilterShapeResult(
				context,
				callback,
				relations,
			); ok {
				value = shape
				break
			}
			key, element := iterableTypes(context.Arguments[0].Type, relations)
			if callback == nil || callback.Kind() == phpsyntax.PhpNull {
				element = withoutFalsyLiterals(relations, element)
			} else if callback.Kind() == phpsyntax.PhpFunctionCall &&
				isFirstClassCallable(callback) {
				constraint := predicateType(
					strings.ToLower(strings.TrimPrefix(
						phpquery.CallMethodName(callback),
						"\\",
					)),
				)
				if !constraint.IsUnknown() {
					element = (types.Relations{}).Narrow(
						element,
						constraint,
					)
				}
			} else if constraint := arrayFilterArrowConstraint(
				context,
				callback,
			); !constraint.IsUnknown() {
				element = relations.Narrow(element, constraint)
			}
			value = types.Array(key, element)
		}
	case "filter":
		if context.Receiver.Kind() == types.ObjectKind &&
			context.Receiver.ArgumentCount() > 0 &&
			relations.IsSubtype(
				context.Receiver,
				types.Named("Shopware\\Core\\Framework\\Struct\\Collection"),
			) {
			callback := phpquery.ArgumentExpression(context.Node, 0)
			constraint := arrayFilterArrowConstraint(context, callback)
			if !constraint.IsUnknown() {
				arguments := context.Receiver.Arguments()
				if types.ContainsUncertain(arguments[0]) {
					arguments[0] = constraint
				} else {
					arguments[0] = relations.Narrow(arguments[0], constraint)
				}
				value = types.Named(context.Receiver.Name(), arguments...)
			}
		}
	case "json_encode":
		flags := phpquery.ArgumentExpression(context.Node, 1)
		if flags == nil || !strings.Contains(
			strings.ToUpper(compact(flags.Text())),
			"JSON_THROW_ON_ERROR",
		) {
			return semantic.TypeFact{}, false
		}
		value = types.String()
	case "var_export", "print_r":
		if len(context.Arguments) > 1 &&
			context.Arguments[1].Type.Kind() == types.TrueKind {
			value = types.String()
		}
	case "modify":
		if dateTimeReceiver(context.Receiver) {
			value = context.Receiver
		}
	case "getrealpath":
		// Finder yields file entries that were resolved while traversing the
		// filesystem. PHPStan likewise treats their inherited getRealPath()
		// result benevolently, despite the broader SPL string|false stub.
		if relations.IsSubtype(context.Receiver, types.Named("SplFileInfo")) ||
			relations.IsSubtype(
				context.Receiver,
				types.Named("Symfony\\Component\\Finder\\SplFileInfo"),
			) {
			value = types.String()
		}
	case "get_class":
		value = types.ClassString(types.Object())
	default:
		return semantic.TypeFact{}, false
	}
	if value.IsUnknown() {
		return semantic.TypeFact{}, false
	}
	return semantic.TypeFact{
		Type:       value,
		Confidence: semantic.DeclaredConfidence,
		Source:     semantic.SignatureSource,
		Reason:     "PHP builtin",
	}, true
})

func arrayColumnType(
	row,
	columnKey types.Type,
	relations types.Relations,
) (types.Type, bool) {
	if row.Kind() == types.UnionKind {
		values := types.NewJoiner(relations, types.Never())
		alwaysPresent := true
		for index := 0; index < row.ArgumentCount(); index++ {
			value, present := arrayColumnType(
				row.Argument(index),
				columnKey,
				relations,
			)
			if value.IsUnknown() {
				return types.Unknown(), false
			}
			values.Add(value)
			alwaysPresent = alwaysPresent && present
		}
		return values.Value(), alwaysPresent
	}
	if row.Kind() != types.ArrayShapeKind ||
		(columnKey.Kind() != types.LiteralStringKind &&
			columnKey.Kind() != types.LiteralIntKind) {
		return types.Unknown(), false
	}
	for index := 0; index < row.FieldCount(); index++ {
		field := row.Field(index)
		if strings.Trim(field.Name, `"'`) == columnKey.Name() {
			return field.Type, !field.Optional
		}
	}
	return types.Unknown(), false
}

func arrayFilterShapeResult(
	context CallContext,
	callback *phpsyntax.Node,
	relations types.Relations,
) (types.Type, bool) {
	source := context.Arguments[0].Type
	if source.Kind() != types.ArrayShapeKind {
		return types.Type{}, false
	}
	constraint := types.Unknown()
	removeFalsy := callback == nil || callback.Kind() == phpsyntax.PhpNull
	removeNull := false
	if !removeFalsy {
		if callback.Kind() == phpsyntax.PhpFunctionCall && isFirstClassCallable(callback) {
			constraint = predicateType(strings.ToLower(strings.TrimPrefix(
				phpquery.CallMethodName(callback),
				"\\",
			)))
		} else {
			constraint = arrayFilterArrowConstraint(context, callback)
			removeNull = arrayFilterArrowExcludesNull(callback)
		}
		if constraint.IsUnknown() && !removeNull {
			return types.Type{}, false
		}
	}

	fields := make([]types.ShapeField, 0, source.FieldCount())
	for index := 0; index < source.FieldCount(); index++ {
		field := source.Field(index)
		narrowed := field.Type
		switch {
		case removeFalsy:
			narrowed = withoutFalsyLiterals(relations, narrowed)
		case removeNull:
			narrowed = relations.Without(narrowed, types.Null())
		default:
			narrowed = relations.Narrow(narrowed, constraint)
		}
		if narrowed.Kind() == types.NeverKind {
			continue
		}
		mayRemove := false
		switch {
		case removeFalsy:
			mayRemove = !typeDefinitelyTruthy(field.Type)
		case removeNull:
			mayRemove = !relations.Without(
				field.Type,
				types.Null(),
			).Equal(field.Type)
		default:
			mayRemove = !relations.IsSubtype(field.Type, constraint)
		}
		if mayRemove {
			field.Optional = true
		}
		field.Type = narrowed
		fields = append(fields, field)
	}
	return types.ArrayShapeOwned(fields, source.IsOpenShape()), true
}

func typeDefinitelyTruthy(value types.Type) bool {
	switch value.Kind() {
	case types.TrueKind, types.ObjectKind, types.ResourceKind,
		types.NonEmptyArrayKind, types.NonEmptyListKind:
		return true
	case types.LiteralStringKind:
		return value.Name() != "" && value.Name() != "0"
	case types.LiteralIntKind, types.LiteralFloatKind:
		return value.Name() != "0" && value.Name() != "0.0"
	case types.ArrayShapeKind:
		return arrayTypeKnownNonEmpty(value)
	case types.UnionKind, types.IntersectionKind:
		if value.ArgumentCount() == 0 {
			return false
		}
		for index := 0; index < value.ArgumentCount(); index++ {
			if !typeDefinitelyTruthy(value.Argument(index)) {
				return false
			}
		}
		return true
	}
	return false
}

func arrayTypeKnownNonEmpty(value types.Type) bool {
	switch value.Kind() {
	case types.NonEmptyArrayKind, types.NonEmptyListKind:
		return true
	case types.ArrayShapeKind:
		for index := 0; index < value.FieldCount(); index++ {
			if !value.Field(index).Optional {
				return true
			}
		}
	case types.UnionKind:
		if value.ArgumentCount() == 0 {
			return false
		}
		for index := 0; index < value.ArgumentCount(); index++ {
			if !arrayTypeKnownNonEmpty(value.Argument(index)) {
				return false
			}
		}
		return true
	}
	return false
}

func arrayFilterArrowExcludesNull(callback *phpsyntax.Node) bool {
	if callback == nil || callback.Kind() != phpsyntax.PhpArrowFunction {
		return false
	}
	parameters := phpquery.Parameters(callback)
	if len(parameters) == 0 {
		return false
	}
	predicate := lastDirectNode(callback)
	if predicate == nil || predicate.Kind() != phpsyntax.PhpBinaryExpression {
		return false
	}
	operator := directOperator(predicate)
	if operator != "!==" && operator != "!=" {
		return false
	}
	operands := directNodes(predicate)
	if operands.Len() < 2 {
		return false
	}
	parameter := phpquery.ParameterName(parameters[0])
	left := operands.At(0)
	right := operands.At(operands.Len() - 1)
	return left.Kind() == phpsyntax.PhpVariable &&
		phpquery.VariableKey(left) == parameter && isNullNode(right) ||
		right.Kind() == phpsyntax.PhpVariable &&
			phpquery.VariableKey(right) == parameter && isNullNode(left)
}

func arrayMapResultType(context CallContext) types.Type {
	callback := context.Arguments[0].Type
	if callback.Kind() == types.CallableKind {
		return callback.Result()
	}
	expression := phpquery.ArgumentExpression(context.Node, 0)
	if expression == nil || expression.Kind() != phpsyntax.PhpString {
		return types.Unknown()
	}
	switch strings.ToLower(diagnosticStringValue(expression)) {
	case "count", "strlen", "mb_strlen":
		return types.Int()
	default:
		return types.Unknown()
	}
}

func dateTimeReceiver(value types.Type) bool {
	switch value.Kind() {
	case types.ObjectKind:
		name := strings.TrimPrefix(value.Name(), "\\")
		return strings.EqualFold(name, "DateTime") ||
			strings.EqualFold(name, "DateTimeImmutable")
	case types.UnionKind, types.IntersectionKind:
		if value.ArgumentCount() == 0 {
			return false
		}
		for _, alternative := range value.Arguments() {
			if !dateTimeReceiver(alternative) {
				return false
			}
		}
		return true
	default:
		return false
	}
}

func diagnosticStringValue(node *phpsyntax.Node) string {
	value := phpquery.StringValue(node)
	text := strings.TrimSpace(node.Text())
	if len(text) < 2 {
		return value
	}
	switch text[0] {
	case '\'':
		return strings.NewReplacer(`\\`, `\`, `\'`, `'`).Replace(value)
	case '"':
		return strings.NewReplacer(`\\`, `\`, `\"`, `"`).Replace(value)
	default:
		return value
	}
}

func arrayRemovalIsGuardedNonEmpty(call *phpsyntax.Node) bool {
	argument := phpquery.ArgumentExpression(call, 0)
	key := flowExpressionKey(argument)
	if argument == nil || key == "" {
		return false
	}
	for current := call; current != nil; current = current.Parent() {
		if current.Kind() == phpsyntax.PhpTernaryExpression {
			parts := directNodes(current)
			if parts.Len() >= 3 {
				condition := parts.At(0)
				trueBranch := parts.At(parts.Len() - 2)
				falseBranch := parts.At(parts.Len() - 1)
				if trueBranch.Range().Contains(call.Range().Start) &&
					conditionGuaranteesNonEmptyArray(condition, key, true) {
					return true
				}
				if falseBranch.Range().Contains(call.Range().Start) &&
					conditionGuaranteesNonEmptyArray(condition, key, false) {
					return true
				}
			}
		}
		if current.Kind() == phpsyntax.PhpBlock && current.Parent() != nil &&
			current.Parent().Kind() == phpsyntax.PhpIfStatement {
			parts := directNodes(current.Parent())
			if parts.Len() >= 2 && parts.At(1) == current &&
				conditionGuaranteesNonEmptyArray(parts.At(0), key, true) {
				return true
			}
		}
		if current.Kind() == phpsyntax.PhpWhileStatement {
			nodes := directNodes(current)
			if nodes.Len() >= 2 &&
				nodes.At(nodes.Len()-1).Range().Contains(call.Range().Start) &&
				conditionGuaranteesNonEmptyArray(nodes.At(0), key, true) {
				return true
			}
		}
	}
	return false
}

func arraySourceKnownNonEmpty(call *phpsyntax.Node) bool {
	return arrayRemovalIsGuardedNonEmpty(call) ||
		arrayRemovalSourceKnownNonEmpty(call) ||
		arrayCallFollowsTerminatingEmptyGuard(call)
}

func arrayCallFollowsTerminatingEmptyGuard(call *phpsyntax.Node) bool {
	argument := phpquery.ArgumentExpression(call, 0)
	key := flowExpressionKey(argument)
	if key == "" {
		return false
	}
	statement := call
	for statement.Parent() != nil &&
		statement.Parent().Kind() != phpsyntax.PhpBlock {
		statement = statement.Parent()
	}
	block := statement.Parent()
	if block == nil {
		return false
	}
	statements := directNodes(block)
	for index := 1; index < statements.Len(); index++ {
		if statements.At(index) != statement {
			continue
		}
		guard := statements.At(index - 1)
		if guard.Kind() != phpsyntax.PhpIfStatement {
			return false
		}
		parts := directNodes(guard)
		if parts.Len() < 2 {
			return false
		}
		condition := parts.At(0)
		body := parts.At(1)
		return body.Kind() == phpsyntax.PhpBlock &&
			blockEndsExecution(body) &&
			conditionGuaranteesNonEmptyArray(condition, key, false)
	}
	return false
}

func blockEndsExecution(block *phpsyntax.Node) bool {
	statements := directNodes(block)
	if statements.Len() == 0 {
		return false
	}
	switch statements.At(statements.Len() - 1).Kind() {
	case phpsyntax.PhpReturnStatement, phpsyntax.PhpThrowStatement:
		return true
	default:
		return false
	}
}

func arrayRemovalSourceKnownNonEmpty(call *phpsyntax.Node) bool {
	argument := phpquery.ArgumentExpression(call, 0)
	if nonEmptyArraySource(argument) {
		return true
	}
	key := flowExpressionKey(argument)
	if key == "" {
		return false
	}
	statement := call
	for statement.Parent() != nil &&
		statement.Parent().Kind() != phpsyntax.PhpBlock {
		statement = statement.Parent()
	}
	block := statement.Parent()
	if block == nil {
		return false
	}
	statements := directNodes(block)
	for index := 0; index < statements.Len(); index++ {
		if statements.At(index) != statement {
			continue
		}
		for previous := index - 1; previous >= 0; previous-- {
			assignment := assignmentToExpression(statements.At(previous), key)
			if assignment != nil {
				return nonEmptyArraySource(assignment)
			}
		}
		break
	}
	return false
}

func assignmentToExpression(node *phpsyntax.Node, key string) *phpsyntax.Node {
	if node == nil || key == "" {
		return nil
	}
	for _, assignment := range phpquery.Nodes(node, phpsyntax.PhpAssignmentExpression) {
		nodes := directNodes(assignment)
		if nodes.Len() >= 2 && flowExpressionKey(nodes.At(0)) == key {
			return nodes.At(nodes.Len() - 1)
		}
	}
	return nil
}

func nonEmptyArraySource(node *phpsyntax.Node) bool {
	if node == nil {
		return false
	}
	if node.Kind() == phpsyntax.PhpArray {
		return len(phpquery.ArrayItems(node)) > 0
	}
	return node.Kind() == phpsyntax.PhpFunctionCall && strings.EqualFold(
		strings.TrimPrefix(phpquery.CallMethodName(node), "\\"),
		"explode",
	)
}

func conditionGuaranteesNonEmptyArray(
	node *phpsyntax.Node,
	key string,
	truth bool,
) bool {
	if node == nil || key == "" {
		return false
	}
	if node.Kind() == phpsyntax.PhpParenthesized {
		nodes := directNodes(node)
		return nodes.Len() > 0 && conditionGuaranteesNonEmptyArray(
			nodes.At(0), key, truth,
		)
	}
	if node.Kind() == phpsyntax.PhpUnaryExpression && directOperator(node) == "!" {
		nodes := directNodes(node)
		return nodes.Len() > 0 && conditionGuaranteesNonEmptyArray(
			nodes.At(nodes.Len()-1), key, !truth,
		)
	}
	if flowExpressionKey(node) == key {
		return truth
	}
	if node.Kind() != phpsyntax.PhpBinaryExpression {
		return false
	}
	nodes := directNodes(node)
	if nodes.Len() < 2 {
		return false
	}
	left := nodes.At(0)
	right := nodes.At(nodes.Len() - 1)
	operator := strings.ToLower(directOperator(node))
	switch operator {
	case "&&", "and":
		leftGuarantees := conditionGuaranteesNonEmptyArray(left, key, truth)
		rightGuarantees := conditionGuaranteesNonEmptyArray(right, key, truth)
		if truth {
			return leftGuarantees || rightGuarantees
		}
		return leftGuarantees && rightGuarantees
	case "||", "or":
		leftGuarantees := conditionGuaranteesNonEmptyArray(left, key, truth)
		rightGuarantees := conditionGuaranteesNonEmptyArray(right, key, truth)
		if truth {
			return leftGuarantees && rightGuarantees
		}
		return leftGuarantees || rightGuarantees
	case "!==", "!=", "===", "==":
		if comparedCountIsPositive(left, right, key) ||
			comparedCountIsPositive(right, left, key) {
			if operator == "!==" || operator == "!=" {
				return !truth
			}
			return truth
		}
		comparesEmpty := flowExpressionKey(left) == key && emptyArrayLiteral(right) ||
			flowExpressionKey(right) == key && emptyArrayLiteral(left)
		if !comparesEmpty {
			return false
		}
		if operator == "!==" || operator == "!=" {
			return truth
		}
		return !truth
	default:
		return false
	}
}

func comparedCountIsPositive(call, literal *phpsyntax.Node, key string) bool {
	if call == nil || call.Kind() != phpsyntax.PhpFunctionCall ||
		!strings.EqualFold(
			strings.TrimPrefix(phpquery.CallMethodName(call), "\\"),
			"count",
		) {
		return false
	}
	argument := phpquery.ArgumentExpression(call, 0)
	if flowExpressionKey(argument) != key {
		return false
	}
	text := strings.TrimSpace(literal.Text())
	return text != "" && text != "0" && text[0] >= '1' && text[0] <= '9'
}

func emptyArrayLiteral(node *phpsyntax.Node) bool {
	return node != nil && node.Kind() == phpsyntax.PhpArray &&
		len(phpquery.ArrayItems(node)) == 0
}

func arrayFilterArrowConstraint(
	context CallContext,
	callback *phpsyntax.Node,
) types.Type {
	if callback == nil || callback.Kind() != phpsyntax.PhpArrowFunction {
		return types.Unknown()
	}
	parameters := phpquery.Parameters(callback)
	if len(parameters) == 0 {
		return types.Unknown()
	}
	nameContext := resolver.NameContext{}
	if context.Document != nil {
		if scope, found := context.Document.ScopeAt(callback.Range().Start); found {
			nameContext.Namespace = scope.Namespace
			nameContext.Imports = scope.Imports
		}
	}
	return arrowPredicateConstraint(
		lastDirectNode(callback),
		phpquery.ParameterName(parameters[0]),
		nameContext,
	)
}

func arrowPredicateConstraint(
	predicate *phpsyntax.Node,
	parameter string,
	nameContext resolver.NameContext,
) types.Type {
	if predicate == nil {
		return types.Unknown()
	}
	if predicate.Kind() == phpsyntax.PhpParenthesized {
		return arrowPredicateConstraint(
			firstDirectNode(predicate),
			parameter,
			nameContext,
		)
	}
	if predicate.Kind() != phpsyntax.PhpBinaryExpression {
		return types.Unknown()
	}
	operands := directNodes(predicate)
	if operands.Len() < 2 {
		return types.Unknown()
	}
	switch strings.ToLower(directOperator(predicate)) {
	case "instanceof":
		if operands.At(0).Kind() != phpsyntax.PhpVariable ||
			phpquery.VariableKey(operands.At(0)) != parameter ||
			operands.At(operands.Len()-1).Kind() != phpsyntax.PhpName {
			return types.Unknown()
		}
		return types.Named(nameContext.ResolveClass(
			phpquery.NameValue(operands.At(operands.Len() - 1)),
		))
	case "||", "or":
		left := arrowPredicateConstraint(operands.At(0), parameter, nameContext)
		right := arrowPredicateConstraint(
			operands.At(operands.Len()-1),
			parameter,
			nameContext,
		)
		if left.IsUnknown() || right.IsUnknown() {
			return types.Unknown()
		}
		return types.Union(left, right)
	case "&&", "and":
		left := arrowPredicateConstraint(operands.At(0), parameter, nameContext)
		right := arrowPredicateConstraint(
			operands.At(operands.Len()-1),
			parameter,
			nameContext,
		)
		if left.IsUnknown() || right.IsUnknown() {
			return types.Unknown()
		}
		return types.Intersection(left, right)
	default:
		return types.Unknown()
	}
}

func pathinfoReturnsString(call *phpsyntax.Node) bool {
	option := phpquery.ArgumentExpression(call, 1)
	if option == nil {
		return false
	}
	name := strings.ToUpper(strings.TrimPrefix(compact(option.Text()), `\`))
	switch name {
	case "PATHINFO_DIRNAME", "PATHINFO_BASENAME", "PATHINFO_EXTENSION",
		"PATHINFO_FILENAME", "1", "2", "4", "8":
		return true
	default:
		return false
	}
}

func knownCoreINIOption(value types.Type) bool {
	if value.Kind() != types.LiteralStringKind {
		return false
	}
	switch strings.ToLower(value.Name()) {
	case "default_charset",
		"max_execution_time",
		"max_input_nesting_level",
		"max_input_time",
		"max_input_vars",
		"memory_limit",
		"post_max_size",
		"upload_max_filesize":
		return true
	default:
		return false
	}
}

func parseURLComponentReturnType(call *phpsyntax.Node) types.Type {
	component := phpquery.ArgumentExpression(call, 1)
	if component == nil {
		return types.Unknown()
	}
	name := strings.ToUpper(strings.TrimPrefix(compact(component.Text()), `\`))
	switch name {
	case "PHP_URL_PORT", "2":
		return types.Union(types.Int(), types.Null(), types.False())
	case "PHP_URL_SCHEME", "PHP_URL_HOST", "PHP_URL_USER", "PHP_URL_PASS",
		"PHP_URL_PATH", "PHP_URL_QUERY", "PHP_URL_FRAGMENT",
		"0", "1", "3", "4", "5", "6", "7":
		return types.Union(types.String(), types.Null(), types.False())
	default:
		return types.Unknown()
	}
}

func replacementSubjectType(value types.Type, relations types.Relations) types.Type {
	switch value.Kind() {
	case types.MixedKind:
		return types.Mixed()
	case types.StringKind, types.LiteralStringKind, types.ClassStringKind,
		types.NullKind, types.ArrayKeyKind:
		return types.String()
	case types.ListKind:
		return types.List(types.String())
	case types.NonEmptyListKind:
		return types.NonEmptyList(types.String())
	case types.ArrayKind, types.ArrayShapeKind:
		key, _ := iterableTypes(value, relations)
		return types.Array(key, types.String())
	case types.NonEmptyArrayKind:
		key, _ := iterableTypes(value, relations)
		return types.NonEmptyArray(key, types.String())
	case types.UnionKind:
		result := types.Never()
		for index := 0; index < value.ArgumentCount(); index++ {
			mapped := replacementSubjectType(value.Argument(index), relations)
			if mapped.IsUnknown() {
				return types.Unknown()
			}
			result = relations.Join(result, mapped)
		}
		return result
	default:
		return types.Unknown()
	}
}

func withoutFalsyLiterals(
	relations types.Relations,
	value types.Type,
) types.Type {
	for _, falsy := range []types.Type{
		types.Null(),
		types.False(),
		types.LiteralInt("0"),
		types.LiteralFloat("0"),
		types.LiteralString(""),
	} {
		value = relations.Without(value, falsy)
	}
	return value
}
