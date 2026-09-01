package inference

import (
	"strconv"
	"strings"

	phpquery "github.com/shopware/shopware-lsp/internal/parser/php/query"
	phpsyntax "github.com/shopware/shopware-lsp/internal/parser/php/syntax"
	"github.com/shopware/shopware-lsp/internal/php/types"
	"github.com/shopware/shopware-lsp/internal/textutil"
)

type directNodeList struct {
	node *phpsyntax.Node
}

func directNodes(node *phpsyntax.Node) directNodeList {
	return directNodeList{node: node}
}

func (nodes directNodeList) Len() int {
	if nodes.node == nil {
		return 0
	}
	count := 0
	for index := 0; index < nodes.node.ChildCount(); index++ {
		if _, ok := nodes.node.Child(index).(*phpsyntax.Node); ok {
			count++
		}
	}
	return count
}

func (nodes directNodeList) At(ordinal int) *phpsyntax.Node {
	if nodes.node == nil || ordinal < 0 {
		return nil
	}
	for index := 0; index < nodes.node.ChildCount(); index++ {
		child, ok := nodes.node.Child(index).(*phpsyntax.Node)
		if !ok {
			continue
		}
		if ordinal == 0 {
			return child
		}
		ordinal--
	}
	return nil
}

func (nodes directNodeList) Cursor() directNodeCursor {
	return directNodeCursor{node: nodes.node}
}

type directNodeCursor struct {
	node    *phpsyntax.Node
	index   int
	current *phpsyntax.Node
}

func (cursor *directNodeCursor) Next() bool {
	if cursor.node == nil {
		return false
	}
	for cursor.index < cursor.node.ChildCount() {
		element := cursor.node.Child(cursor.index)
		cursor.index++
		if child, ok := element.(*phpsyntax.Node); ok {
			cursor.current = child
			return true
		}
	}
	cursor.current = nil
	return false
}

func (cursor *directNodeCursor) Node() *phpsyntax.Node {
	return cursor.current
}

func firstDirectNode(node *phpsyntax.Node) *phpsyntax.Node {
	if node == nil {
		return nil
	}
	for index := 0; index < node.ChildCount(); index++ {
		element := node.Child(index)
		if child, ok := element.(*phpsyntax.Node); ok {
			return child
		}
	}
	return nil
}

func lastDirectNode(node *phpsyntax.Node) *phpsyntax.Node {
	if node == nil {
		return nil
	}
	for index := node.ChildCount() - 1; index >= 0; index-- {
		if child, ok := node.Child(index).(*phpsyntax.Node); ok {
			return child
		}
	}
	return nil
}

func directOperator(node *phpsyntax.Node) string {
	if node == nil {
		return ""
	}
	for index := 0; index < node.ChildCount(); index++ {
		element := node.Child(index)
		token, ok := element.(*phpsyntax.Token)
		if !ok || token.Kind().IsTrivia() {
			continue
		}
		switch token.Kind() {
		case phpsyntax.TkEquals, phpsyntax.TkOperator, phpsyntax.TkPipe,
			phpsyntax.TkAmpersand, phpsyntax.TkQuestion:
			return token.Text()
		case phpsyntax.TkKeyword:
			switch strings.ToLower(token.Text()) {
			case "instanceof", "and", "or", "xor":
				return token.Text()
			}
		}
	}
	return ""
}

func hasDirectToken(node *phpsyntax.Node, kind phpsyntax.Kind) bool {
	if node == nil {
		return false
	}
	for index := 0; index < node.ChildCount(); index++ {
		element := node.Child(index)
		token, ok := element.(*phpsyntax.Token)
		if ok && token.Kind() == kind {
			return true
		}
	}
	return false
}

func hasDirectTokenText(node *phpsyntax.Node, text string) bool {
	if node == nil {
		return false
	}
	for index := 0; index < node.ChildCount(); index++ {
		element := node.Child(index)
		token, ok := element.(*phpsyntax.Token)
		if ok && strings.EqualFold(token.Text(), text) {
			return true
		}
	}
	return false
}

func isExpressionKind(kind phpsyntax.Kind) bool {
	switch kind {
	case phpsyntax.PhpAssignmentExpression, phpsyntax.PhpBinaryExpression,
		phpsyntax.PhpUnaryExpression, phpsyntax.PhpTernaryExpression,
		phpsyntax.PhpMemberCall, phpsyntax.PhpScopedCall,
		phpsyntax.PhpFunctionCall, phpsyntax.PhpMemberAccess,
		phpsyntax.PhpScopedAccess, phpsyntax.PhpArrayAccess,
		phpsyntax.PhpObjectCreation, phpsyntax.PhpClosure,
		phpsyntax.PhpArrowFunction, phpsyntax.PhpMatchExpression,
		phpsyntax.PhpThrowExpression, phpsyntax.PhpYieldExpression,
		phpsyntax.PhpCloneExpression, phpsyntax.PhpCastExpression,
		phpsyntax.PhpArray, phpsyntax.PhpString, phpsyntax.PhpName,
		phpsyntax.PhpVariable, phpsyntax.PhpNumber, phpsyntax.PhpBoolean,
		phpsyntax.PhpNull, phpsyntax.PhpParenthesized:
		return true
	default:
		return false
	}
}

func isTypeKind(kind phpsyntax.Kind) bool {
	switch kind {
	case phpsyntax.PhpType, phpsyntax.PhpNullableType,
		phpsyntax.PhpUnionType, phpsyntax.PhpIntersectionType:
		return true
	default:
		return false
	}
}

func compact(source string) string {
	return textutil.CompactWhitespace(source)
}

func isIntLike(value types.Type) bool {
	return value.Kind() == types.IntKind || value.Kind() == types.LiteralIntKind
}

func isFloatLike(value types.Type) bool {
	return value.Kind() == types.FloatKind || value.Kind() == types.LiteralFloatKind
}

func iterableTypes(
	value types.Type,
	relations types.Relations,
) (types.Type, types.Type) {
	switch value.Kind() {
	case types.ArrayKind, types.NonEmptyArrayKind, types.IterableKind:
		if value.ArgumentCount() == 2 {
			if value.Kind() != types.IterableKind &&
				value.Argument(1).Kind() == types.NeverKind {
				return types.Never(), types.Never()
			}
			return value.Argument(0), value.Argument(1)
		}
	case types.ListKind, types.NonEmptyListKind:
		if value.ArgumentCount() == 1 {
			return types.Int(), value.Argument(0)
		}
	case types.ArrayShapeKind:
		return arrayShapeIterableTypes(value, relations)
	case types.UnionKind:
		keys := types.NewJoiner(relations, types.Never())
		values := types.NewJoiner(relations, types.Never())
		for index := 0; index < value.ArgumentCount(); index++ {
			key, element := iterableTypes(value.Argument(index), relations)
			if key.IsUnknown() || element.IsUnknown() {
				return types.Unknown(), types.Unknown()
			}
			keys.Add(key)
			values.Add(element)
		}
		return keys.Value(), values.Value()
	case types.ObjectKind:
		if value.ArgumentCount() == 1 {
			name := strings.ToLower(strings.TrimPrefix(value.Name(), "\\"))
			switch name {
			case "iterator", "iteratoraggregate", "traversable":
				return types.ArrayKey(), value.Argument(0)
			}
		}
		if hierarchy, ok := relations.Hierarchy.(types.SupertypeHierarchy); ok {
			for _, target := range []string{"IteratorAggregate", "Traversable"} {
				projected, found := hierarchy.AsSupertype(value, target)
				if !found {
					continue
				}
				switch projected.ArgumentCount() {
				case 1:
					return types.ArrayKey(), projected.Argument(0)
				case 2:
					return projected.Argument(0), projected.Argument(1)
				}
			}
		}
	}
	return types.Unknown(), types.Unknown()
}

func arrayShapeIterableTypes(
	value types.Type,
	relations types.Relations,
) (types.Type, types.Type) {
	keys := types.NewJoiner(relations, types.Never())
	values := types.NewJoiner(relations, types.Never())
	for fieldIndex := 0; fieldIndex < value.FieldCount(); fieldIndex++ {
		field := value.Field(fieldIndex)
		name := strings.Trim(field.Name, `"'`)
		if numeric, err := strconv.Atoi(name); err == nil {
			keys.Add(types.LiteralInt(strconv.Itoa(numeric)))
		} else {
			keys.Add(types.LiteralString(name))
		}
		values.Add(field.Type)
	}
	return keys.Value(), values.Value()
}

func arrayShapeIsList(value types.Type) bool {
	if value.Kind() != types.ArrayShapeKind || value.IsOpenShape() {
		return false
	}
	fields := make(map[string]struct{}, value.FieldCount())
	for fieldIndex := 0; fieldIndex < value.FieldCount(); fieldIndex++ {
		field := value.Field(fieldIndex)
		if field.Optional {
			return false
		}
		fields[strings.Trim(field.Name, `"'`)] = struct{}{}
	}
	for index := 0; index < value.FieldCount(); index++ {
		if _, exists := fields[strconv.Itoa(index)]; !exists {
			return false
		}
	}
	return true
}

func resolveSpecialType(
	value,
	receiver,
	currentClass types.Type,
) types.Type {
	return resolveSpecialTypeAtDepth(value, receiver, currentClass, 0)
}

// Fluent builders can carry a parent stack in nested generic arguments. Keep
// enough headroom for valid chains while bounding adversarial type graphs.
const maxSpecialTypeDepth = 128

func resolveSpecialTypeAtDepth(
	value,
	receiver,
	currentClass types.Type,
	depth int,
) types.Type {
	if depth >= maxSpecialTypeDepth {
		return types.Unknown()
	}
	nextDepth := depth + 1
	switch value.Kind() {
	case types.SelfKind:
		return applySpecialTypeArguments(currentClass, value)
	case types.StaticKind:
		return applySpecialTypeArguments(receiver, value)
	case types.ParentKind:
		return types.Unknown()
	case types.UnionKind:
		members, changed, _ := resolveSpecialTypeArguments(
			value, receiver, currentClass, nextDepth, false,
		)
		if !changed {
			return value
		}
		return types.Union(members...)
	case types.IntersectionKind:
		members, changed, _ := resolveSpecialTypeArguments(
			value, receiver, currentClass, nextDepth, false,
		)
		if !changed {
			return value
		}
		return types.Intersection(members...)
	case types.ObjectKind:
		if value.Name() == "" {
			return value
		}
		arguments, changed, valid := resolveSpecialTypeArguments(
			value, receiver, currentClass, nextDepth, true,
		)
		if !valid {
			// A concrete nested type was truncated by the depth bound.
			// Keeping only the outer nominal shell would make later
			// parent-return calls look definite when they are not.
			return types.Unknown()
		}
		if !changed {
			return value
		}
		return types.Named(value.Name(), arguments...)
	case types.ArrayKind:
		if value.ArgumentCount() != 2 {
			return value
		}
		arguments, changed, _ := resolveSpecialTypeArguments(
			value, receiver, currentClass, nextDepth, false,
		)
		if !changed {
			return value
		}
		return types.Array(arguments[0], arguments[1])
	case types.NonEmptyArrayKind:
		if value.ArgumentCount() != 2 {
			return value
		}
		arguments, changed, _ := resolveSpecialTypeArguments(
			value, receiver, currentClass, nextDepth, false,
		)
		if !changed {
			return value
		}
		return types.NonEmptyArray(arguments[0], arguments[1])
	case types.ListKind:
		if value.ArgumentCount() != 1 {
			return value
		}
		arguments, changed, _ := resolveSpecialTypeArguments(
			value, receiver, currentClass, nextDepth, false,
		)
		if !changed {
			return value
		}
		return types.List(arguments[0])
	case types.NonEmptyListKind:
		if value.ArgumentCount() != 1 {
			return value
		}
		arguments, changed, _ := resolveSpecialTypeArguments(
			value, receiver, currentClass, nextDepth, false,
		)
		if !changed {
			return value
		}
		return types.NonEmptyList(arguments[0])
	case types.IterableKind:
		if value.ArgumentCount() != 2 {
			return value
		}
		arguments, changed, _ := resolveSpecialTypeArguments(
			value, receiver, currentClass, nextDepth, false,
		)
		if !changed {
			return value
		}
		return types.Iterable(arguments[0], arguments[1])
	case types.ClassStringKind:
		if value.ArgumentCount() != 1 {
			return value
		}
		arguments, changed, _ := resolveSpecialTypeArguments(
			value, receiver, currentClass, nextDepth, false,
		)
		if !changed {
			return value
		}
		return types.ClassString(arguments[0])
	case types.CallableKind:
		return resolveSpecialCallableType(
			value, receiver, currentClass, nextDepth,
		)
	case types.ArrayShapeKind, types.ObjectShapeKind:
		return resolveSpecialShapeType(
			value, receiver, currentClass, nextDepth,
		)
	case types.ConditionalKind:
		if value.ArgumentCount() != 4 {
			return value
		}
		arguments, changed, _ := resolveSpecialTypeArguments(
			value, receiver, currentClass, nextDepth, false,
		)
		if !changed {
			return value
		}
		return types.Conditional(
			arguments[0],
			arguments[1],
			arguments[2],
			arguments[3],
		)
	default:
		return value
	}
}

func resolveSpecialTypeArguments(
	value,
	receiver,
	currentClass types.Type,
	depth int,
	rejectTruncatedConcrete bool,
) ([]types.Type, bool, bool) {
	count := value.ArgumentCount()
	var result []types.Type
	for index := 0; index < count; index++ {
		original := value.Argument(index)
		resolved := resolveSpecialTypeAtDepth(
			original, receiver, currentClass, depth,
		)
		if rejectTruncatedConcrete &&
			resolved.IsUnknown() && !original.IsUnknown() {
			return nil, false, false
		}
		if resolved.Equal(original) {
			if result != nil {
				result[index] = original
			}
			continue
		}
		if result == nil {
			result = make([]types.Type, count)
			for previous := 0; previous < index; previous++ {
				result[previous] = value.Argument(previous)
			}
		}
		result[index] = resolved
	}
	return result, result != nil, true
}

func resolveSpecialCallableType(
	value,
	receiver,
	currentClass types.Type,
	depth int,
) types.Type {
	result := resolveSpecialTypeAtDepth(
		value.Result(), receiver, currentClass, depth,
	)
	resultChanged := !result.Equal(value.Result())
	var parameters []types.CallableParameter
	for index := 0; index < value.ParameterCount(); index++ {
		parameter := value.Parameter(index)
		resolved := resolveSpecialTypeAtDepth(
			parameter.Type, receiver, currentClass, depth,
		)
		if resolved.Equal(parameter.Type) {
			continue
		}
		if parameters == nil {
			parameters = copyCallableParameters(value)
		}
		parameters[index].Type = resolved
	}
	if parameters == nil && !resultChanged {
		return value
	}
	if parameters == nil {
		parameters = copyCallableParameters(value)
	}
	return types.Callable(parameters, result)
}

func resolveSpecialShapeType(
	value,
	receiver,
	currentClass types.Type,
	depth int,
) types.Type {
	var fields []types.ShapeField
	for index := 0; index < value.FieldCount(); index++ {
		field := value.Field(index)
		resolved := resolveSpecialTypeAtDepth(
			field.Type, receiver, currentClass, depth,
		)
		if resolved.Equal(field.Type) {
			continue
		}
		if fields == nil {
			fields = copyShapeFields(value)
		}
		fields[index].Type = resolved
	}
	if fields == nil {
		return value
	}
	if value.Kind() == types.ArrayShapeKind {
		return types.ArrayShapeOwned(fields, value.IsOpenShape())
	}
	return types.ObjectShape(fields, value.IsOpenShape())
}

func copyCallableParameters(value types.Type) []types.CallableParameter {
	result := make([]types.CallableParameter, value.ParameterCount())
	for index := range result {
		result[index] = value.Parameter(index)
	}
	return result
}

func copyShapeFields(value types.Type) []types.ShapeField {
	result := make([]types.ShapeField, value.FieldCount())
	for index := range result {
		result[index] = value.Field(index)
	}
	return result
}

func applySpecialTypeArguments(
	base types.Type,
	special types.Type,
) types.Type {
	argumentCount := special.ArgumentCount()
	if argumentCount == 0 || base.Kind() != types.ObjectKind ||
		base.Name() == "" {
		return base
	}
	arguments := make([]types.Type, argumentCount)
	for index := range arguments {
		arguments[index] = special.Argument(index)
	}
	return types.Named(base.Name(), arguments...)
}

func joinTypes(relations types.Relations, values []types.Type) types.Type {
	if len(values) == 0 {
		return types.Unknown()
	}
	joiner := types.NewJoiner(relations, values[0])
	for _, value := range values[1:] {
		joiner.Add(value)
	}
	return joiner.Value()
}

type namedTypeCache struct {
	name  string
	value types.Type
}

func (cache *namedTypeCache) typeFor(name string) types.Type {
	if name != "" && name == cache.name {
		return cache.value
	}
	value := types.Named(name)
	if name != "" {
		cache.name = name
		cache.value = value
	}
	return value
}

func (s *functionState) namedType(name string) types.Type {
	return s.namedTypes.typeFor(name)
}

func castType(node *phpsyntax.Node) types.Type {
	text := strings.ToLower(compact(node.Text()))
	switch {
	case strings.HasPrefix(text, "(int)"), strings.HasPrefix(text, "(integer)"):
		return types.Int()
	case strings.HasPrefix(text, "(float)"), strings.HasPrefix(text, "(double)"),
		strings.HasPrefix(text, "(real)"):
		return types.Float()
	case strings.HasPrefix(text, "(string)"), strings.HasPrefix(text, "(binary)"):
		return types.String()
	case strings.HasPrefix(text, "(bool)"), strings.HasPrefix(text, "(boolean)"):
		return types.Bool()
	case strings.HasPrefix(text, "(array)"):
		return types.Array(types.Mixed(), types.Mixed())
	case strings.HasPrefix(text, "(object)"):
		return types.Object()
	default:
		return types.Unknown()
	}
}

func isCallNamed(node *phpsyntax.Node, name string) bool {
	call := phpquery.CallAt(node)
	return call != nil && strings.EqualFold(
		strings.TrimPrefix(phpquery.CallMethodName(call), "\\"),
		strings.TrimPrefix(name, "\\"),
	)
}

func strconvItoa(value int) string {
	return strconv.Itoa(value)
}
