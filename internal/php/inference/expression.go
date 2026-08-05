package inference

import (
	"slices"
	"strconv"
	"strings"

	phpquery "github.com/shopware/shopware-lsp/internal/parser/php/query"
	phpsyntax "github.com/shopware/shopware-lsp/internal/parser/php/syntax"
	"github.com/shopware/shopware-lsp/internal/php/literal"
	"github.com/shopware/shopware-lsp/internal/php/resolver"
	"github.com/shopware/shopware-lsp/internal/php/semantic"
	"github.com/shopware/shopware-lsp/internal/php/types"
)

func (s *functionState) infer(node *phpsyntax.Node, env environment) types.Type {
	if node == nil {
		return types.Unknown()
	}
	if fact, exists := s.document.TypeFact(semantic.NodeIdentity(node)); exists {
		// Literal expressions are independent of the flow environment. Reuse
		// their immutable type across fixed-point and branch passes instead of
		// rebuilding the same literal node and cached text each time.
		if fact.Confidence == semantic.DeclaredConfidence ||
			fact.Source == semantic.LiteralSource {
			return fact.Type
		}
	}
	if value, ok := literal.TypeOf(node); ok {
		return value
	}
	if key := flowExpressionKey(node); key != "" {
		if value, exists := env.get(key); exists {
			switch node.Kind() {
			case phpsyntax.PhpMemberAccess,
				phpsyntax.PhpMemberCall,
				phpsyntax.PhpScopedAccess,
				phpsyntax.PhpScopedCall:
				// The flow fact belongs to the complete repeated
				// expression, but the later member-linking pass also needs
				// the receiver node's narrowed fact.
				s.infer(firstDirectNode(node), env)
			case phpsyntax.PhpArrayAccess:
				// A cached array-element value must not skip inference of
				// member calls used by its key expression. Those calls still
				// need receiver facts for semantic member linking.
				nodes := directNodes(node)
				if nodes.Len() > 0 {
					s.infer(nodes.At(0), env)
				}
				if nodes.Len() > 1 {
					s.infer(nodes.At(nodes.Len()-1), env)
				}
			}
			s.record(node, value, semantic.FlowSource, "flow expression")
			return value
		}
	}
	var result types.Type
	source := semantic.InferredConfidence
	typeSource := semantic.AssignmentSource
	reason := ""

	switch node.Kind() {
	case phpsyntax.PhpVariable:
		name := phpquery.VariableKey(node)
		if value, ok := env.get(name); ok {
			result = value
		} else {
			result = types.Unknown()
		}
	case phpsyntax.PhpParenthesized:
		result = s.infer(firstDirectNode(node), env)
	case phpsyntax.PhpAssignmentExpression:
		result = s.inferAssignment(node, env)
	case phpsyntax.PhpBinaryExpression:
		result = s.inferBinary(node, env)
	case phpsyntax.PhpUnaryExpression:
		result = s.inferUnary(node, env)
	case phpsyntax.PhpTernaryExpression:
		result = s.inferTernary(node, env)
	case phpsyntax.PhpArray:
		result = s.inferArray(node, env)
	case phpsyntax.PhpArrayAccess:
		result = s.inferArrayAccess(node, env)
	case phpsyntax.PhpObjectCreation:
		result = s.inferObjectCreation(node, env)
		typeSource = semantic.SignatureSource
	case phpsyntax.PhpMemberAccess:
		result = s.inferMember(node, env, hasDirectToken(node, phpsyntax.TkScopeResolution))
		typeSource = semantic.SignatureSource
	case phpsyntax.PhpScopedAccess:
		result = s.inferMember(node, env, true)
		typeSource = semantic.SignatureSource
	case phpsyntax.PhpMemberCall:
		result = s.inferCall(node, env, false, false)
		typeSource = semantic.SignatureSource
	case phpsyntax.PhpScopedCall:
		result = s.inferCall(node, env, true, false)
		typeSource = semantic.SignatureSource
	case phpsyntax.PhpFunctionCall:
		result = s.inferCall(node, env, false, true)
		typeSource = semantic.SignatureSource
	case phpsyntax.PhpArrowFunction:
		result = s.inferArrowFunction(node, env)
	case phpsyntax.PhpClosure:
		result = s.inferClosure(node, env)
	case phpsyntax.PhpMatchExpression:
		result = s.inferMatch(node, env)
	case phpsyntax.PhpThrowExpression:
		if expression := lastDirectNode(node); expression != nil {
			s.infer(expression, env)
		}
		result = types.Never()
	case phpsyntax.PhpCloneExpression:
		result = s.infer(lastDirectNode(node), env)
	case phpsyntax.PhpYieldExpression:
		value := types.Mixed()
		expressions := directNodes(node)
		for index := 0; index < expressions.Len(); index++ {
			value = s.infer(expressions.At(index), env)
		}
		result = types.Iterable(types.ArrayKey(), value)
	case phpsyntax.PhpCastExpression:
		s.infer(lastDirectNode(node), env)
		result = castType(node)
	case phpsyntax.PhpName:
		result = s.inferName(node)
	case phpsyntax.PhpArgument, phpsyntax.PhpNamedArgument,
		phpsyntax.PhpArrayItem, phpsyntax.PhpMatchArm:
		result = s.infer(lastDirectNode(node), env)
	default:
		result = types.Unknown()
	}
	if result.IsUnknown() {
		typeSource = semantic.UnknownSource
		source = semantic.UnknownConfidence
	}
	s.recordWithConfidence(node, result, source, typeSource, reason)
	return result
}

func (s *functionState) inferAssignment(node *phpsyntax.Node, env environment) types.Type {
	nodes := directNodes(node)
	nodeCount := nodes.Len()
	if nodeCount < 2 {
		return types.Unknown()
	}
	value := s.infer(nodes.At(nodeCount-1), env)
	left := nodes.At(0)
	if directOperator(node) == "??=" {
		existing := s.infer(left, env)
		nonNull := s.relations.Without(existing, types.Null())
		value = s.relations.Join(nonNull, value)
	}
	s.assignTarget(left, value, nodes.At(nodeCount-1), env)
	return value
}

func (s *functionState) assignTarget(
	left *phpsyntax.Node,
	value types.Type,
	source *phpsyntax.Node,
	env environment,
) {
	if left == nil {
		return
	}
	if left.Kind() == phpsyntax.PhpVariable {
		name := phpquery.VariableKey(left)
		env.deletePrefix(booleanAliasPrefix(name))
		env.set(name, value)
		s.record(left, value, semantic.AssignmentSource, "assignment")
		s.updateLocalType(name, left.Range().Start, value)
		if value.Kind() == types.BoolKind ||
			value.Kind() == types.TrueKind ||
			value.Kind() == types.FalseKind {
			s.captureBooleanAlias(name, source, env)
		}
	} else if left.Kind() == phpsyntax.PhpArray {
		s.assignDestructured(left, value, env)
	} else if left.Kind() == phpsyntax.PhpArrayAccess {
		s.infer(left, env)
		s.updateArrayAssignment(left, value, env)
		if key := flowExpressionKey(left); key != "" {
			env.set(key, value)
			s.record(left, value, semantic.AssignmentSource, "assignment")
		}
	} else if key := flowExpressionKey(left); key != "" {
		s.infer(left, env)
		s.recordReadonlyPropertyAssignment(left, value)
		env.set(key, value)
		s.record(left, value, semantic.AssignmentSource, "assignment")
	} else {
		s.infer(left, env)
	}
}

func (s *functionState) assignDestructured(
	left *phpsyntax.Node,
	value types.Type,
	env environment,
) {
	position := 0
	for cursor := directNodes(left).Cursor(); cursor.Next(); position++ {
		item := cursor.Node()
		if item.Kind() != phpsyntax.PhpArrayItem {
			continue
		}
		parts := directNodes(item)
		if parts.Len() == 0 {
			continue
		}
		key := types.LiteralInt(strconvItoa(position))
		target := parts.At(parts.Len() - 1)
		if parts.Len() > 1 {
			key = s.infer(parts.At(0), env)
		}
		element := s.arrayAccessType(value, key)
		if element.IsUnknown() {
			continue
		}
		s.assignTarget(target, element, nil, env)
	}
}

func (s *functionState) updateArrayAssignment(
	left *phpsyntax.Node,
	value types.Type,
	env environment,
) {
	base, path, found := s.arrayAccessPath(left, env)
	if !found {
		return
	}
	name := phpquery.VariableKey(base)
	existing, found := env.get(name)
	if !found {
		existing = s.infer(base, env)
	}
	updated := s.arrayPathAssignmentType(
		existing,
		path,
		value,
		0,
	)
	if updated.IsUnknown() {
		return
	}
	env.set(name, updated)
	s.record(base, updated, semantic.AssignmentSource, "array assignment")
	s.updateLocalType(name, base.Range().Start, updated)
}

type arrayAccessSegment struct {
	key       types.Type
	fieldName string
	literal   bool
	append    bool
}

func (s *functionState) arrayAccessPath(
	node *phpsyntax.Node,
	env environment,
) (*phpsyntax.Node, []arrayAccessSegment, bool) {
	var reversed []arrayAccessSegment
	for node != nil && node.Kind() == phpsyntax.PhpArrayAccess {
		nodes := directNodes(node)
		if nodes.Len() == 0 {
			return nil, nil, false
		}
		segment := arrayAccessSegment{key: types.Int(), append: nodes.Len() == 1}
		if !segment.append {
			keyNode := nodes.At(nodes.Len() - 1)
			segment.key = s.infer(keyNode, env)
			segment.fieldName, segment.literal = s.arrayFieldName(keyNode, env)
			if segment.literal {
				// Foo::class is a single, literal array key even though its value
				// type remains class-string<Foo> in every other expression.
				segment.key = types.LiteralString(segment.fieldName)
			}
		}
		reversed = append(reversed, segment)
		node = nodes.At(0)
	}
	if node == nil || node.Kind() != phpsyntax.PhpVariable || len(reversed) == 0 {
		return nil, nil, false
	}
	slices.Reverse(reversed)
	return node, reversed, true
}

func (s *functionState) arrayPathAssignmentType(
	existing types.Type,
	path []arrayAccessSegment,
	value types.Type,
	depth int,
) types.Type {
	if len(path) == 0 || depth >= maxSpecialTypeDepth {
		return types.Unknown()
	}
	if len(path) == 1 {
		return s.arrayAssignmentType(
			existing,
			path[0].key,
			value,
			path[0].append,
			depth+1,
		)
	}
	if resolved, found := s.analyzer.Snapshot.ResolveTypeAlias(existing); found &&
		!resolved.IsUnknown() && !resolved.Equal(existing) {
		return s.arrayPathAssignmentType(resolved, path, value, depth+1)
	}
	if existing.Kind() == types.UnionKind {
		alternatives := make([]types.Type, 0, existing.ArgumentCount())
		for index := 0; index < existing.ArgumentCount(); index++ {
			updated := s.arrayPathAssignmentType(
				existing.Argument(index),
				path,
				value,
				depth+1,
			)
			if updated.IsUnknown() {
				return types.Unknown()
			}
			alternatives = append(alternatives, updated)
		}
		return types.Union(alternatives...)
	}

	head := path[0]
	switch existing.Kind() {
	case types.ArrayKind, types.NonEmptyArrayKind:
		if existing.ArgumentCount() != 2 {
			return types.Unknown()
		}
		if existing.Argument(1).Kind() == types.NeverKind {
			updated := s.arrayPathAssignmentType(
				types.Array(types.ArrayKey(), types.Never()),
				path[1:],
				value,
				depth+1,
			)
			if updated.IsUnknown() {
				return types.Unknown()
			}
			if head.append {
				return types.NonEmptyList(updated)
			}
			return types.NonEmptyArray(head.key, updated)
		}
		updated := s.arrayPathAssignmentType(
			existing.Argument(1),
			path[1:],
			value,
			depth+1,
		)
		if updated.IsUnknown() {
			return types.Unknown()
		}
		return types.NonEmptyArray(
			s.relations.Join(existing.Argument(0), head.key),
			updated,
		)
	case types.ListKind, types.NonEmptyListKind:
		if existing.ArgumentCount() != 1 {
			return types.Unknown()
		}
		updated := s.arrayPathAssignmentType(
			existing.Argument(0),
			path[1:],
			value,
			depth+1,
		)
		if updated.IsUnknown() {
			return types.Unknown()
		}
		if existing.Kind() == types.NonEmptyListKind {
			return types.NonEmptyList(updated)
		}
		return types.List(updated)
	case types.ArrayShapeKind:
		fields := existing.Fields()
		matched := false
		for index := range fields {
			if head.literal && strings.Trim(fields[index].Name, `"'`) != head.fieldName {
				continue
			}
			updated := s.arrayPathAssignmentType(
				fields[index].Type,
				path[1:],
				value,
				depth+1,
			)
			if updated.IsUnknown() {
				continue
			}
			fields[index].Type = updated
			matched = true
		}
		if matched {
			return types.ArrayShapeOwned(fields, existing.IsOpenShape())
		}
	}
	return types.Unknown()
}

func (s *functionState) arrayAssignmentType(
	existing,
	key,
	value types.Type,
	appendValue bool,
	depth int,
) types.Type {
	if depth >= maxSpecialTypeDepth {
		return types.Unknown()
	}
	if resolved, found := s.analyzer.Snapshot.ResolveTypeAlias(existing); found &&
		!resolved.IsUnknown() && !resolved.Equal(existing) {
		return s.arrayAssignmentType(
			resolved,
			key,
			value,
			appendValue,
			depth+1,
		)
	}
	if existing.Kind() == types.UnionKind {
		alternatives := make([]types.Type, 0, existing.ArgumentCount())
		for index := 0; index < existing.ArgumentCount(); index++ {
			updated := s.arrayAssignmentType(
				existing.Argument(index),
				key,
				value,
				appendValue,
				depth+1,
			)
			if updated.IsUnknown() {
				return types.Unknown()
			}
			alternatives = append(alternatives, updated)
		}
		return types.Union(alternatives...)
	}

	var updated types.Type
	switch existing.Kind() {
	case types.ArrayKind, types.NonEmptyArrayKind:
		if existing.ArgumentCount() != 2 {
			return types.Unknown()
		}
		keyType := existing.Argument(0)
		valueType := existing.Argument(1)
		if valueType.Kind() == types.NeverKind {
			if appendValue {
				updated = types.NonEmptyList(value)
			} else if fieldName, literal := arrayLiteralKey(key); literal {
				updated = types.ArrayShapeOwned([]types.ShapeField{{
					Name: fieldName,
					Type: value,
				}}, false)
			} else {
				updated = types.NonEmptyArray(key, value)
			}
		} else {
			updated = types.NonEmptyArray(
				s.relations.Join(keyType, key),
				s.relations.Join(valueType, value),
			)
		}
	case types.ListKind, types.NonEmptyListKind:
		if existing.ArgumentCount() != 1 {
			return types.Unknown()
		}
		elementType := existing.Argument(0)
		if appendValue {
			updated = types.NonEmptyList(s.relations.Join(elementType, value))
		} else {
			updated = types.NonEmptyArray(
				s.relations.Join(types.Int(), key),
				s.relations.Join(elementType, value),
			)
		}
	case types.ArrayShapeKind:
		if !appendValue {
			if fieldName, literal := arrayLiteralKey(key); literal {
				fields := make(
					[]types.ShapeField,
					0,
					existing.FieldCount()+1,
				)
				replaced := false
				for fieldIndex := 0; fieldIndex < existing.FieldCount(); fieldIndex++ {
					field := existing.Field(fieldIndex)
					if strings.Trim(field.Name, `"'`) == fieldName {
						field.Type = value
						field.Optional = false
						replaced = true
					}
					fields = append(fields, field)
				}
				if replaced || len(fields) < maxInferredTupleFields {
					if !replaced {
						fields = append(fields, types.ShapeField{
							Name: fieldName,
							Type: value,
						})
					}
					updated = types.ArrayShapeOwned(fields, false)
					break
				}
			}
		}
		_, elementType := arrayShapeIterableTypes(existing, s.relations)
		if appendValue && arrayShapeIsList(existing) {
			updated = types.NonEmptyList(s.relations.Join(elementType, value))
		} else {
			keyType, existingValue := arrayShapeIterableTypes(
				existing,
				s.relations,
			)
			updated = types.Array(
				s.relations.Join(keyType, key),
				s.relations.Join(existingValue, value),
			)
		}
	default:
		updated = types.Array(key, value)
	}
	return updated
}

func (s *functionState) updateLocalType(
	name string,
	offset uint32,
	value types.Type,
) {
	if value.IsUnknown() {
		return
	}
	scope, ok := s.document.ScopeAt(offset)
	if !ok {
		return
	}
	for {
		for id := range scope.SymbolIDs(name) {
			for index := range s.document.Symbols {
				symbol := &s.document.Symbols[index]
				if symbol.ID != id || symbol.Kind != semantic.LocalSymbol {
					continue
				}
				if symbol.Type.IsUnknown() {
					symbol.Type = value
				} else {
					symbol.Type = s.relations.Join(symbol.Type, value)
				}
				return
			}
		}
		if scope.ID == scope.Parent || int(scope.Parent) >= len(s.document.Scopes) {
			return
		}
		scope = s.document.Scopes[scope.Parent]
	}
}

func (s *functionState) inferBinary(node *phpsyntax.Node, env environment) types.Type {
	nodes := directNodes(node)
	nodeCount := nodes.Len()
	if nodeCount < 2 {
		return types.Unknown()
	}
	operator := directOperator(node)
	normalizedOperator := strings.ToLower(operator)
	leftNode := nodes.At(0)
	rightNode := nodes.At(nodeCount - 1)
	switch normalizedOperator {
	case "&&", "and":
		leftTrue, _ := s.conditionEnvironments(leftNode, env)
		s.infer(rightNode, leftTrue)
		return types.Bool()
	case "||", "or":
		_, leftFalse := s.conditionEnvironments(leftNode, env)
		s.infer(rightNode, leftFalse)
		return types.Bool()
	}
	left := s.infer(leftNode, env)
	right := s.infer(rightNode, env)
	switch normalizedOperator {
	case "==", "!=", "===", "!==", "<", ">", "<=", ">=",
		"instanceof", "xor":
		return types.Bool()
	case "<=>":
		return types.Int()
	case ".":
		return types.String()
	case "??":
		return s.relations.Join(s.relations.Without(left, types.Null()), right)
	case "+", "-", "*", "/", "%", "**":
		if (left.Kind() == types.ArrayKind || left.Kind() == types.NonEmptyArrayKind) &&
			(right.Kind() == types.ArrayKind || right.Kind() == types.NonEmptyArrayKind) &&
			operator == "+" {
			return s.relations.Join(left, right)
		}
		if isFloatLike(left) || isFloatLike(right) || operator == "/" {
			return types.Float()
		}
		if isIntLike(left) && isIntLike(right) {
			return types.Int()
		}
		return types.Unknown()
	case "|", "&", "^", "<<", ">>":
		return types.Int()
	default:
		return types.Unknown()
	}
}

func (s *functionState) inferUnary(node *phpsyntax.Node, env environment) types.Type {
	value := s.infer(lastDirectNode(node), env)
	for _, keyword := range []string{
		"include", "include_once", "require", "require_once",
	} {
		if hasDirectTokenText(node, keyword) {
			// Included PHP files may return any value. The type of the path
			// expression says nothing about the value produced by the file.
			return types.Mixed()
		}
	}
	switch directOperator(node) {
	case "!":
		return types.Bool()
	case "+", "-", "++", "--":
		if isFloatLike(value) {
			return types.Float()
		}
		if isIntLike(value) {
			return types.Int()
		}
	case "~":
		if value.Kind() == types.StringKind || value.Kind() == types.LiteralStringKind {
			return types.String()
		}
		return types.Int()
	default:
		return value
	}
	return types.Unknown()
}

func (s *functionState) inferTernary(node *phpsyntax.Node, env environment) types.Type {
	nodes := directNodes(node)
	nodeCount := nodes.Len()
	if nodeCount < 2 {
		return types.Unknown()
	}
	condition := nodes.At(0)
	trueEnv, falseEnv := s.conditionEnvironments(condition, env)
	if nodeCount == 2 {
		truthy := withoutFalsyLiterals(
			s.relations,
			s.infer(nodes.At(0), trueEnv),
		)
		return s.relations.Join(
			truthy,
			s.infer(nodes.At(1), falseEnv),
		)
	}
	trueNode := nodes.At(nodeCount - 2)
	falseNode := nodes.At(nodeCount - 1)
	trueValue := s.narrowRepeatedConditionExpression(
		condition,
		trueNode,
		s.infer(trueNode, trueEnv),
		true,
	)
	falseValue := s.narrowRepeatedConditionExpression(
		condition,
		falseNode,
		s.infer(falseNode, falseEnv),
		false,
	)
	return s.relations.Join(
		trueValue,
		falseValue,
	)
}

func (s *functionState) narrowRepeatedConditionExpression(
	condition,
	branch *phpsyntax.Node,
	value types.Type,
	truth bool,
) types.Type {
	if condition == nil || branch == nil {
		return value
	}
	if condition.Kind() == phpsyntax.PhpParenthesized {
		return s.narrowRepeatedConditionExpression(
			firstDirectNode(condition),
			branch,
			value,
			truth,
		)
	}
	if condition.Kind() == phpsyntax.PhpUnaryExpression &&
		directOperator(condition) == "!" {
		return s.narrowRepeatedConditionExpression(
			lastDirectNode(condition),
			branch,
			value,
			!truth,
		)
	}
	if condition.Kind() != phpsyntax.PhpFunctionCall {
		return value
	}
	constraint := predicateType(
		strings.ToLower(strings.TrimPrefix(
			phpquery.CallMethodName(condition),
			"\\",
		)),
	)
	if constraint.IsUnknown() {
		return value
	}
	arguments := phpquery.Arguments(condition)
	if len(arguments) == 0 {
		return value
	}
	tested := lastDirectNode(arguments[0])
	if tested == nil ||
		compact(tested.Text()) != compact(branch.Text()) {
		return value
	}
	if truth {
		value = s.relations.Narrow(value, constraint)
	} else {
		value = s.relations.Without(value, constraint)
	}
	s.record(branch, value, semantic.FlowSource, "conditional predicate")
	return value
}

func (s *functionState) inferArray(node *phpsyntax.Node, env environment) types.Type {
	structure := inspectArrayLiteral(node)
	if structure.implicitList {
		if structure.itemCount > 0 &&
			structure.itemCount <= maxInferredTupleFields {
			fields := make([]types.ShapeField, 0, structure.itemCount)
			for cursor := directNodes(node).Cursor(); cursor.Next(); {
				item := cursor.Node()
				if item.Kind() != phpsyntax.PhpArrayItem {
					continue
				}
				values := directNodes(item)
				if values.Len() == 0 {
					continue
				}
				fields = append(fields, types.ShapeField{
					Name: strconvItoa(len(fields)),
					Type: s.infer(values.At(0), env),
				})
			}
			return types.ArrayShapeOwned(fields, false)
		}
		valueTypes := types.NewJoiner(s.relations, types.Never())
		for cursor := directNodes(node).Cursor(); cursor.Next(); {
			item := cursor.Node()
			if item.Kind() != phpsyntax.PhpArrayItem {
				continue
			}
			values := directNodes(item)
			if values.Len() == 0 {
				continue
			}
			valueTypes.Add(s.infer(values.At(0), env))
		}
		valueType := valueTypes.Value()
		if valueType.Kind() == types.NeverKind {
			return types.Array(types.ArrayKey(), types.Never())
		}
		return types.NonEmptyList(valueType)
	}

	keyTypes := types.NewJoiner(s.relations, types.Never())
	valueTypes := types.NewJoiner(s.relations, types.Never())
	var shapeFields []types.ShapeField
	var shapeFieldIndices map[string]int
	nextKey := 0
	shapeCandidate := true
	listCandidate := true
	knownNonEmpty := false
	for cursor := directNodes(node).Cursor(); cursor.Next(); {
		item := cursor.Node()
		if item.Kind() != phpsyntax.PhpArrayItem {
			continue
		}
		values := directNodes(item)
		valueCount := values.Len()
		if valueCount == 0 {
			continue
		}
		if hasDirectToken(item, phpsyntax.TkEllipsis) {
			source := s.infer(values.At(valueCount-1), env)
			knownNonEmpty = knownNonEmpty || arrayTypeKnownNonEmpty(source)
			key, value := s.arrayIterableTypes(source)
			if !s.arraySpreadProducesList(source) {
				listCandidate = false
			}
			if shapeCandidate {
				switch {
				case source.Kind() == types.ArrayShapeKind && !source.IsOpenShape():
					for fieldIndex := 0; fieldIndex < source.FieldCount(); fieldIndex++ {
						field := source.Field(fieldIndex)
						name := strings.Trim(field.Name, `"'`)
						if _, err := strconv.Atoi(name); err == nil {
							field.Name = strconvItoa(nextKey)
							nextKey++
						} else {
							field.Name = name
						}
						shapeFields, shapeFieldIndices = upsertShapeField(
							shapeFields,
							shapeFieldIndices,
							field,
						)
					}
				case (source.Kind() == types.ArrayKind ||
					source.Kind() == types.NonEmptyArrayKind) &&
					source.ArgumentCount() == 2 &&
					source.Argument(1).Kind() == types.NeverKind:
				default:
					shapeCandidate = false
					shapeFields = nil
					shapeFieldIndices = nil
				}
			}
			keyTypes.Add(key)
			valueTypes.Add(value)
			continue
		}
		var key, value types.Type
		knownNonEmpty = true
		if valueCount > 1 {
			listCandidate = false
			key = s.infer(values.At(0), env)
			value = s.infer(values.At(valueCount-1), env)
			name, literal := arrayLiteralKey(key)
			if literal && shapeCandidate {
				field := types.ShapeField{Name: name, Type: value}
				if shapeFields == nil {
					shapeFields = make(
						[]types.ShapeField,
						0,
						structure.itemCount,
					)
				}
				shapeFields, shapeFieldIndices = upsertShapeField(
					shapeFields,
					shapeFieldIndices,
					field,
				)
				if key.Kind() == types.LiteralIntKind {
					if numeric, err := strconv.Atoi(key.Name()); err == nil &&
						numeric >= nextKey {
						nextKey = numeric + 1
					}
				}
			} else if !literal {
				shapeCandidate = false
				shapeFields = nil
				shapeFieldIndices = nil
			}
		} else {
			name := strconvItoa(nextKey)
			key = types.LiteralInt(name)
			value = s.infer(values.At(0), env)
			if shapeCandidate {
				if shapeFields == nil {
					shapeFields = make(
						[]types.ShapeField,
						0,
						structure.itemCount,
					)
				}
				field := types.ShapeField{Name: name, Type: value}
				shapeFields, shapeFieldIndices = upsertShapeField(
					shapeFields,
					shapeFieldIndices,
					field,
				)
			}
			nextKey++
		}
		keyTypes.Add(key)
		valueTypes.Add(value)
	}
	keyType := keyTypes.Value()
	valueType := valueTypes.Value()
	if valueType.Kind() == types.NeverKind {
		return types.Array(types.ArrayKey(), types.Never())
	}
	if shapeCandidate {
		return types.ArrayShapeOwned(slices.Clip(shapeFields), false)
	}
	if listCandidate {
		if knownNonEmpty {
			return types.NonEmptyList(valueType)
		}
		return types.List(valueType)
	}
	if knownNonEmpty {
		return types.NonEmptyArray(keyType, valueType)
	}
	return types.Array(keyType, valueType)
}

const maxInferredTupleFields = 16

type arrayLiteralStructure struct {
	itemCount    int
	implicitList bool
	hasSpread    bool
}

func inspectArrayLiteral(node *phpsyntax.Node) arrayLiteralStructure {
	structure := arrayLiteralStructure{implicitList: true}
	for cursor := directNodes(node).Cursor(); cursor.Next(); {
		item := cursor.Node()
		if item.Kind() != phpsyntax.PhpArrayItem {
			continue
		}
		values := directNodes(item)
		if values.Len() == 0 {
			continue
		}
		structure.itemCount++
		if hasDirectToken(item, phpsyntax.TkEllipsis) {
			structure.implicitList = false
			structure.hasSpread = true
			continue
		}
		if values.Len() > 1 {
			structure.implicitList = false
		}
	}
	return structure
}

func isImplicitListArray(node *phpsyntax.Node) bool {
	return inspectArrayLiteral(node).implicitList
}

const shapeFieldLinearLimit = 16

func upsertShapeField(
	fields []types.ShapeField,
	indices map[string]int,
	field types.ShapeField,
) ([]types.ShapeField, map[string]int) {
	if indices != nil {
		if index, exists := indices[field.Name]; exists {
			fields[index] = field
			return fields, indices
		}
		indices[field.Name] = len(fields)
		return append(fields, field), indices
	}

	for index := range fields {
		if fields[index].Name == field.Name {
			fields[index] = field
			return fields, nil
		}
	}
	if len(fields) == shapeFieldLinearLimit {
		indices = make(map[string]int, shapeFieldLinearLimit*2)
		for index := range fields {
			indices[fields[index].Name] = index
		}
		indices[field.Name] = len(fields)
	}
	return append(fields, field), indices
}

func arrayLiteralKey(value types.Type) (string, bool) {
	switch value.Kind() {
	case types.LiteralIntKind, types.LiteralStringKind:
		return value.Name(), true
	default:
		return "", false
	}
}

func (s *functionState) arrayFieldName(
	node *phpsyntax.Node,
	env environment,
) (string, bool) {
	if value, literal := literal.TypeOf(node); literal {
		return arrayLiteralKey(value)
	}
	receiverNode := classConstantReceiver(node)
	if receiverNode == nil {
		return "", false
	}
	receiver := s.inferReceiver(receiverNode, env, true)
	if receiver.Kind() != types.ObjectKind || receiver.Name() == "" {
		return "", false
	}
	return receiver.Name(), true
}

func (s *functionState) arrayIterableTypes(
	value types.Type,
) (types.Type, types.Type) {
	switch value.Kind() {
	case types.ListKind, types.NonEmptyListKind:
		return types.Int(), value.Argument(0)
	case types.ArrayKind, types.NonEmptyArrayKind, types.IterableKind:
		return value.Argument(0), value.Argument(1)
	case types.ArrayShapeKind:
		return arrayShapeIterableTypes(value, s.relations)
	case types.UnionKind:
		keys := types.NewJoiner(s.relations, types.Never())
		values := types.NewJoiner(s.relations, types.Never())
		for index := 0; index < value.ArgumentCount(); index++ {
			member := value.Argument(index)
			key, element := s.arrayIterableTypes(member)
			keys.Add(key)
			values.Add(element)
		}
		return keys.Value(), values.Value()
	default:
		return types.ArrayKey(), types.Unknown()
	}
}

func (s *functionState) arraySpreadProducesList(value types.Type) bool {
	switch value.Kind() {
	case types.UnionKind:
		for _, member := range value.Arguments() {
			if !s.arraySpreadProducesList(member) {
				return false
			}
		}
		return value.ArgumentCount() > 0
	default:
		key, element := s.arrayIterableTypes(value)
		return element.Kind() == types.NeverKind ||
			!key.IsUnknown() && s.relations.IsSubtype(key, types.Int())
	}
}

func (s *functionState) inferArrayAccess(node *phpsyntax.Node, env environment) types.Type {
	nodes := directNodes(node)
	nodeCount := nodes.Len()
	if nodeCount == 0 {
		return types.Unknown()
	}
	receiver := s.infer(nodes.At(0), env)
	key := types.Unknown()
	if nodeCount > 1 {
		key = s.infer(nodes.At(nodeCount-1), env)
	}
	return s.arrayAccessType(receiver, key)
}

func (s *functionState) arrayAccessType(receiver, key types.Type) types.Type {
	if receiver.Kind() == types.UnionKind {
		var members []types.Type
		for _, alternative := range receiver.Arguments() {
			value := s.arrayAccessType(alternative, key)
			if !value.IsUnknown() {
				members = append(members, value)
			}
		}
		return joinTypes(s.relations, members)
	}
	switch receiver.Kind() {
	case types.ArrayKind, types.NonEmptyArrayKind, types.IterableKind:
		if receiver.ArgumentCount() == 2 {
			return receiver.Argument(1)
		}
	case types.ListKind, types.NonEmptyListKind:
		if receiver.ArgumentCount() == 1 {
			return receiver.Argument(0)
		}
	case types.StringKind, types.LiteralStringKind:
		return types.String()
	case types.ArrayShapeKind:
		if key.Kind() == types.LiteralStringKind ||
			key.Kind() == types.LiteralIntKind {
			for fieldIndex := 0; fieldIndex < receiver.FieldCount(); fieldIndex++ {
				field := receiver.Field(fieldIndex)
				if strings.Trim(field.Name, `"'`) == key.Name() {
					if field.Optional {
						return types.Nullable(field.Type)
					}
					return field.Type
				}
			}
		}
	}
	return types.Unknown()
}

func (s *functionState) inferObjectCreation(
	node *phpsyntax.Node,
	env environment,
) types.Type {
	nameNode := phpquery.DirectChild(node, phpsyntax.PhpName)
	if nameNode == nil {
		if anonymous := phpquery.DirectChild(
			node,
			phpsyntax.PhpAnonymousClass,
		); anonymous != nil {
			return types.Named(semantic.AnonymousClassName(
				s.document.Path,
				anonymous.RangeTrimmedTrivia().Start,
			))
		}
		return types.Object()
	}
	context := s.nameContextAt(node.Range().Start)
	name := phpquery.NameValue(nameNode)
	if strings.HasPrefix(name, "$") {
		value, found := env.get(name)
		if !found {
			return types.Object()
		}
		return dynamicObjectType(value)
	}
	className := context.ResolveClass(name)
	preserveCurrentTemplates := false
	switch strings.ToLower(strings.TrimPrefix(name, "\\")) {
	case "self", "static":
		if s.currentClass.Kind() == types.ObjectKind {
			className = s.currentClass.Name()
			preserveCurrentTemplates = true
		}
	case "parent":
		if s.currentClass.Kind() == types.ObjectKind {
			s.analyzer.Snapshot.VisitClassViews(
				s.currentClass.Name(),
				func(classView semantic.SymbolView) bool {
					class := classView.Materialize()
					if len(class.Extends) > 0 {
						className = class.Extends[0]
					}
					return false
				},
			)
		}
	}
	result := types.Named(className)
	arguments, uncertainUnpack := s.inferArguments(node, env)

	var inferred []types.Type
	hasConstructor := false
	permissiveConstructor := false
	foundClass := false
	s.analyzer.Snapshot.VisitClassViews(
		className,
		func(classView semantic.SymbolView) bool {
			foundClass = true
			class := classView.Materialize()
			currentTemplates := mapCurrentClassTemplates(
				class,
				s.currentClass,
				preserveCurrentTemplates,
			)
			constructorReceiver := types.Named(class.FullyQualified)
			if len(class.Templates) > 0 {
				templateArguments := make(
					[]types.Type,
					len(class.Templates),
				)
				for index, template := range class.Templates {
					templateArguments[index] = types.Template(template.Name)
				}
				constructorReceiver = types.Named(
					class.FullyQualified,
					templateArguments...,
				)
			}
			constructors := resolver.MemberResolver{
				Snapshot: s.analyzer.Snapshot,
			}.Methods(constructorReceiver, "__construct")
			if len(constructors) == 0 {
				if len(arguments) == 0 {
					inferred = append(
						inferred,
						genericObjectType(class, currentTemplates),
					)
				}
				return true
			}
			hasConstructor = true
			for _, constructor := range constructors {
				resolved := resolver.ResolveSignature(
					s.relations,
					constructor.Symbol,
					arguments,
				)
				if resolved.Compatible {
					s.applyByReferenceArguments(
						node,
						constructor.Symbol,
						arguments,
						env,
					)
					inferred = append(
						inferred,
						genericObjectType(
							class,
							mergeMissingTemplates(
								resolved.Templates,
								currentTemplates,
							),
						),
					)
				} else if constructor.Symbol.Flags.Has(
					semantic.GeneratedStubFlag,
				) {
					permissiveConstructor = true
				}
			}
			return true
		},
	)
	if !foundClass {
		return result
	}
	if len(inferred) > 0 {
		return joinTypes(s.relations, inferred)
	}
	if permissiveConstructor {
		return result
	}
	if !uncertainUnpack && (hasConstructor || len(arguments) > 0) {
		s.report(
			node,
			"php.arguments",
			"No matching constructor for "+className,
		)
	}
	return result
}

func mapCurrentClassTemplates(
	class semantic.Symbol,
	current types.Type,
	enabled bool,
) map[string]types.Type {
	if !enabled ||
		current.Kind() != types.ObjectKind ||
		!strings.EqualFold(class.FullyQualified, current.Name()) {
		return nil
	}
	count := min(len(class.Templates), current.ArgumentCount())
	if count == 0 {
		return nil
	}
	result := make(map[string]types.Type, count)
	for index := 0; index < count; index++ {
		result[class.Templates[index].Name] = current.Argument(index)
	}
	return result
}

func mergeMissingTemplates(
	inferred,
	fallback map[string]types.Type,
) map[string]types.Type {
	if len(fallback) == 0 {
		return inferred
	}
	result := make(map[string]types.Type, len(inferred)+len(fallback))
	for name, value := range fallback {
		result[name] = value
	}
	for name, value := range inferred {
		result[name] = value
	}
	return result
}

func dynamicObjectType(value types.Type) types.Type {
	switch value.Kind() {
	case types.ClassStringKind:
		if value.ArgumentCount() == 1 {
			return value.Argument(0)
		}
	case types.ObjectKind:
		return value
	case types.UnionKind:
		var objects []types.Type
		for index := 0; index < value.ArgumentCount(); index++ {
			alternative := value.Argument(index)
			object := dynamicObjectType(alternative)
			if object.Kind() == types.ObjectKind {
				objects = append(objects, object)
			}
		}
		if len(objects) > 0 {
			return types.Union(objects...)
		}
	}
	// A plain string can name any runtime class. Keep it uncertain instead of
	// turning it into the concrete broad `object` contract, which would make
	// downstream argument diagnostics claim an incompatibility we cannot
	// prove. class-string<T> above still retains T precisely.
	return types.Unknown()
}

func genericObjectType(
	class semantic.Symbol,
	inferred map[string]types.Type,
) types.Type {
	if len(class.Templates) == 0 {
		return types.Named(class.FullyQualified)
	}
	arguments := make([]types.Type, 0, len(class.Templates))
	for _, template := range class.Templates {
		value, exists := inferred[template.Name]
		if !exists {
			value = template.Default
		}
		if value.IsUnknown() {
			return types.Named(class.FullyQualified)
		}
		arguments = append(arguments, value)
	}
	return types.Named(class.FullyQualified, arguments...)
}

func (s *functionState) inferMember(
	node *phpsyntax.Node,
	env environment,
	static bool,
) types.Type {
	nodes := directNodes(node)
	nodeCount := nodes.Len()
	if nodeCount < 2 {
		return types.Unknown()
	}
	receiver := s.inferReceiver(nodes.At(0), env, static)
	name := phpquery.NameValue(nodes.At(nodeCount - 1))
	if static && strings.EqualFold(name, "class") {
		return types.ClassString(receiver)
	}
	memberResolver := resolver.MemberResolver{Snapshot: s.analyzer.Snapshot}
	var members []types.Type
	if static {
		if strings.HasPrefix(name, "$") {
			members = memberResolver.PropertyTypes(receiver, name)
		} else {
			members = memberResolver.ConstantTypes(receiver, name)
		}
	} else {
		members = memberResolver.PropertyTypes(receiver, name)
		if refined, found := s.readonlyPropertyType(receiver, name); found {
			return refined
		}
	}
	return s.joinMembers(members, receiver)
}

func (s *functionState) recordReadonlyPropertyAssignment(
	target *phpsyntax.Node,
	value types.Type,
) {
	if target == nil || target.Kind() != phpsyntax.PhpMemberAccess ||
		!strings.EqualFold(s.symbol.Name, "__construct") || value.IsUnknown() {
		return
	}
	nodes := directNodes(target)
	if nodes.Len() < 2 || nodes.At(0).Kind() != phpsyntax.PhpVariable ||
		phpquery.VariableKey(nodes.At(0)) != "$this" {
		return
	}
	name := phpquery.NameValue(nodes.At(nodes.Len() - 1))
	memberResolver := resolver.MemberResolver{Snapshot: s.analyzer.Snapshot}
	for _, id := range memberResolver.PropertyIDs(s.currentClass, name) {
		property, found := s.analyzer.Snapshot.Symbol(id)
		if !found || property.Kind != semantic.PropertySymbol ||
			property.Visibility != semantic.Private ||
			!property.Flags.Has(semantic.ReadonlyFlag) ||
			!s.relations.IsAssignableTo(value, property.Type) {
			continue
		}
		if existing, exists := s.readonlyPropertyTypes[id]; exists {
			s.readonlyPropertyTypes[id] = s.relations.Join(existing, value)
		} else {
			s.readonlyPropertyTypes[id] = value
		}
	}
}

func (s *functionState) readonlyPropertyType(
	receiver types.Type,
	name string,
) (types.Type, bool) {
	ids := (resolver.MemberResolver{
		Snapshot: s.analyzer.Snapshot,
	}).PropertyIDs(receiver, name)
	if len(ids) == 0 {
		return types.Type{}, false
	}
	values := make([]types.Type, 0, len(ids))
	for _, id := range ids {
		value, found := s.readonlyPropertyTypes[id]
		if !found {
			return types.Type{}, false
		}
		values = append(values, value)
	}
	return joinTypes(s.relations, values), true
}

func (s *functionState) inferCall(
	node *phpsyntax.Node,
	env environment,
	static bool,
	function bool,
) types.Type {
	nodes := directNodes(node)
	if nodes.Len() == 0 {
		return types.Unknown()
	}
	if isFirstClassCallable(node) {
		return s.inferFirstClassCallable(node, env, static, function)
	}
	arguments, uncertainUnpack := s.inferArguments(node, env)
	if function {
		callee := nodes.At(0)
		if callee.Kind() != phpsyntax.PhpName {
			callable := s.infer(callee, env)
			if result, found := callableResult(callable); found {
				return result
			}
			return types.Unknown()
		}
		name := phpquery.NameValue(callee)
		if extension, ok := s.extensionType(node, name, types.Unknown(), arguments, false); ok {
			return extension
		}
		context := s.nameContextAt(node.Range().Start)
		var results []types.Type
		var generatedFallbacks []types.Type
		candidateCount := 0
		context.VisitFunctionNames(name, func(candidateName string) bool {
			s.analyzer.Snapshot.VisitFunctionViews(
				candidateName,
				func(candidate semantic.SymbolView) bool {
					candidateCount++
					candidateSymbol := candidate.Materialize()
					resolved := resolver.ResolveSignature(
						s.relations,
						candidateSymbol,
						arguments,
					)
					if resolved.Compatible {
						s.applyByReferenceArguments(
							node,
							candidateSymbol,
							arguments,
							env,
						)
						s.recordReturnDependency(candidateSymbol)
						results = append(results, resolved.ReturnType)
					} else if candidateSymbol.Flags.Has(
						semantic.GeneratedStubFlag,
					) {
						generatedFallbacks = append(
							generatedFallbacks,
							resolved.ReturnType,
						)
					}
					return true
				},
			)
			return len(results) == 0
		})
		if len(results) == 0 && len(generatedFallbacks) > 0 {
			return joinTypes(s.relations, generatedFallbacks)
		}
		if candidateCount > 0 && len(results) == 0 && !uncertainUnpack {
			s.report(
				node,
				"php.arguments",
				"No matching signature for "+name,
			)
		}
		return joinTypes(s.relations, results)
	}

	if nodes.Len() < 2 {
		return types.Unknown()
	}
	receiver := s.inferReceiver(nodes.At(0), env, static)
	name := phpquery.NameValue(nodes.At(1))
	if static && nodes.At(0).Kind() == phpsyntax.PhpObjectCreation &&
		strings.HasPrefix(name, "$") {
		propertyTypes := resolver.MemberResolver{
			Snapshot: s.analyzer.Snapshot,
		}.PropertyTypes(receiver, name)
		return dynamicObjectType(s.joinMembers(propertyTypes, receiver))
	}
	lateStaticReceiver := receiver
	if static &&
		nodes.At(0).Kind() == phpsyntax.PhpName &&
		s.currentClass.Kind() == types.ObjectKind {
		switch strings.ToLower(phpquery.NameValue(nodes.At(0))) {
		case "self", "static", "parent":
			lateStaticReceiver = s.currentClass
		}
	}
	if extension, ok := s.extensionType(node, name, receiver, arguments, static); ok {
		return extension
	}
	members := resolver.MemberResolver{Snapshot: s.analyzer.Snapshot}.Methods(receiver, name)
	var results []types.Type
	var generatedFallbacks []types.Type
	for _, member := range members {
		selfType := s.memberSelfType(member.Symbol, receiver)
		symbol := resolveMemberSpecialTypes(
			member.Symbol,
			lateStaticReceiver,
			selfType,
		)
		resolved := resolver.ResolveSignature(
			s.relations,
			symbol,
			arguments,
		)
		if resolved.Compatible {
			s.applyByReferenceArguments(
				node,
				symbol,
				arguments,
				env,
			)
			s.recordReturnDependency(member.Symbol)
			results = append(
				results,
				resolved.ReturnType,
			)
		} else if member.Symbol.Flags.Has(semantic.GeneratedStubFlag) {
			generatedFallbacks = append(
				generatedFallbacks,
				resolved.ReturnType,
			)
		}
	}
	if len(results) == 0 && len(generatedFallbacks) > 0 {
		results = generatedFallbacks
	}
	if len(members) > 0 && len(results) == 0 && !uncertainUnpack {
		s.report(
			node,
			"php.arguments",
			"No matching signature for "+name,
		)
	}
	result := joinTypes(s.relations, results)
	if hasDirectToken(node, phpsyntax.TkNullsafeObjectOperator) {
		result = types.Nullable(result)
	}
	return result
}

func callableResult(value types.Type) (types.Type, bool) {
	if value.Kind() == types.CallableKind {
		return value.Result(), true
	}
	if value.Kind() != types.UnionKind {
		return types.Unknown(), false
	}
	var results []types.Type
	for index := 0; index < value.ArgumentCount(); index++ {
		alternative := value.Argument(index)
		if alternative.Kind() == types.CallableKind {
			results = append(results, alternative.Result())
		}
	}
	if len(results) == 0 {
		return types.Unknown(), false
	}
	return types.Union(results...), true
}

func isFirstClassCallable(node *phpsyntax.Node) bool {
	arguments := phpquery.IterateArguments(node)
	if !arguments.Next() {
		return false
	}
	argument := arguments.Node()
	return !arguments.Next() &&
		hasDirectToken(argument, phpsyntax.TkEllipsis) &&
		lastDirectNode(argument) == nil
}

func (s *functionState) inferFirstClassCallable(
	node *phpsyntax.Node,
	env environment,
	static bool,
	function bool,
) types.Type {
	nodes := directNodes(node)
	if nodes.Len() == 0 {
		return types.Callable(nil, types.Mixed())
	}
	var callables []types.Type
	if function {
		callee := nodes.At(0)
		if callee.Kind() != phpsyntax.PhpName {
			value := s.infer(callee, env)
			if value.Kind() == types.CallableKind {
				return value
			}
			return types.Callable(nil, types.Mixed())
		}
		name := phpquery.NameValue(callee)
		context := s.nameContextAt(node.Range().Start)
		context.VisitFunctionNames(name, func(candidateName string) bool {
			s.analyzer.Snapshot.VisitFunctionViews(
				candidateName,
				func(candidate semantic.SymbolView) bool {
					callables = append(
						callables,
						callableFromSymbol(candidate.Materialize()),
					)
					return true
				},
			)
			return len(callables) == 0
		})
		return joinTypes(s.relations, callables)
	}
	if nodes.Len() < 2 {
		return types.Callable(nil, types.Mixed())
	}
	receiver := s.inferReceiver(nodes.At(0), env, static)
	name := phpquery.NameValue(nodes.At(1))
	for _, member := range (resolver.MemberResolver{
		Snapshot: s.analyzer.Snapshot,
	}).Methods(receiver, name) {
		selfType := s.memberSelfType(member.Symbol, receiver)
		symbol := resolveMemberSpecialTypes(
			member.Symbol,
			receiver,
			selfType,
		)
		callables = append(callables, callableFromSymbol(symbol))
	}
	return joinTypes(s.relations, callables)
}

func resolveMemberSpecialTypes(
	symbol semantic.Symbol,
	receiver,
	selfType types.Type,
) semantic.Symbol {
	symbol.ReturnType = resolveSpecialType(
		symbol.ReturnType,
		receiver,
		selfType,
	)
	symbol.Parameters = append([]semantic.Parameter(nil), symbol.Parameters...)
	for index := range symbol.Parameters {
		parameter := &symbol.Parameters[index]
		parameter.Type = resolveSpecialType(
			parameter.Type,
			receiver,
			selfType,
		)
		parameter.NativeType = resolveSpecialType(
			parameter.NativeType,
			receiver,
			selfType,
		)
		parameter.DocType = resolveSpecialType(
			parameter.DocType,
			receiver,
			selfType,
		)
	}
	return symbol
}

func callableFromSymbol(symbol semantic.Symbol) types.Type {
	parameters := make(
		[]types.CallableParameter,
		len(symbol.Parameters),
	)
	for index, parameter := range symbol.Parameters {
		parameters[index] = types.CallableParameter{
			Name:        parameter.Name,
			Type:        parameter.Type,
			Optional:    parameter.Optional,
			Variadic:    parameter.Flags.Has(semantic.VariadicFlag),
			ByReference: parameter.Flags.Has(semantic.ByReferenceFlag),
		}
	}
	result := symbol.ReturnType
	if result.IsUnknown() {
		result = types.Mixed()
	}
	return types.Callable(parameters, result)
}

func (s *functionState) memberSelfType(
	member semantic.Symbol,
	receiver types.Type,
) types.Type {
	container := member.Container
	for container != "" {
		symbol, found := s.analyzer.Snapshot.Symbol(container)
		if !found {
			break
		}
		if symbol.IsClassLike() {
			if symbol.Kind == semantic.TraitSymbol {
				return receiver
			}
			if receiver.Kind() == types.ObjectKind {
				if strings.EqualFold(
					strings.TrimPrefix(receiver.Name(), "\\"),
					strings.TrimPrefix(symbol.FullyQualified, "\\"),
				) {
					return receiver
				}
				if hierarchy, ok := s.relations.Hierarchy.(types.SupertypeHierarchy); ok {
					if projected, found := hierarchy.AsSupertype(
						receiver,
						symbol.FullyQualified,
					); found {
						return projected
					}
				}
			}
			return types.Named(symbol.FullyQualified)
		}
		container = symbol.Container
	}
	return receiver
}

func (s *functionState) recordReturnDependency(symbol semantic.Symbol) {
	if !symbol.ReturnType.IsUnknown() ||
		symbol.Path != s.document.Path {
		return
	}
	if _, found := s.functionIndices[symbol.ID]; !found {
		return
	}
	if s.dependencies == nil {
		s.dependencies = make(map[semantic.SymbolID]struct{})
	}
	s.dependencies[symbol.ID] = struct{}{}
}

func (s *functionState) inferArguments(
	node *phpsyntax.Node,
	env environment,
) ([]CallArgument, bool) {
	arguments := phpquery.IterateArguments(node)
	result := s.arguments.allocate(arguments.Len())[:0]
	uncertainUnpack := false
	argumentIndex := 0
	for arguments.Next() {
		argument := arguments.Node()
		currentArgumentIndex := argumentIndex
		argumentIndex++
		expression := lastDirectNode(argument)
		value := types.Unknown()
		parameterTypes := s.contextualCallbackParameterTypes(
			node,
			currentArgumentIndex,
			env,
		)
		if len(parameterTypes) > 0 {
			switch expression.Kind() {
			case phpsyntax.PhpArrowFunction:
				value = s.inferArrowFunctionWithParameters(
					expression,
					env,
					parameterTypes,
				)
			case phpsyntax.PhpClosure:
				value = s.inferClosureWithParameters(
					expression,
					env,
					parameterTypes,
				)
			}
		}
		if value.IsUnknown() {
			value = s.infer(expression, env)
		}
		if hasDirectToken(argument, phpsyntax.TkEllipsis) {
			if expanded, ok := positionalTupleArguments(value); ok {
				result = append(result, expanded...)
				continue
			}
			_, element := iterableTypes(value, s.relations)
			if !element.IsUnknown() {
				value = element
			}
			uncertainUnpack = true
		}
		result = append(result, CallArgument{
			Name:       phpquery.ArgumentName(argument),
			Type:       value,
			Expression: expression.Text(),
		})
	}
	return result, uncertainUnpack
}

func (s *functionState) contextualCallbackParameterTypes(
	call *phpsyntax.Node,
	argumentIndex int,
	env environment,
) []types.Type {
	if call == nil || call.Kind() != phpsyntax.PhpFunctionCall {
		return nil
	}
	name := strings.ToLower(strings.TrimPrefix(
		phpquery.CallMethodName(call),
		"\\",
	))
	switch name {
	case "preg_replace_callback":
		if argumentIndex == 1 {
			return []types.Type{regexCallbackMatchesType()}
		}
	case "array_map":
		if argumentIndex != 0 {
			return nil
		}
		arguments := phpquery.Arguments(call)
		parameterTypes := make([]types.Type, 0, max(0, len(arguments)-1))
		for index := 1; index < len(arguments); index++ {
			source := phpquery.ArgumentExpression(call, index)
			_, element := iterableTypes(s.infer(source, env), s.relations)
			if element.IsUnknown() {
				return nil
			}
			parameterTypes = append(parameterTypes, element)
		}
		return parameterTypes
	case "array_filter":
		if argumentIndex == 1 {
			source := phpquery.ArgumentExpression(call, 0)
			_, element := iterableTypes(s.infer(source, env), s.relations)
			if !element.IsUnknown() {
				return []types.Type{element}
			}
		}
	}
	return nil
}

func regexCallbackMatchesType() types.Type {
	return types.ArrayShapeOwned([]types.ShapeField{{
		Name: "0",
		Type: types.String(),
	}}, true)
}

func positionalTupleArguments(value types.Type) ([]CallArgument, bool) {
	if value.Kind() != types.ArrayShapeKind || !arrayShapeIsList(value) {
		return nil, false
	}
	result := make([]CallArgument, value.FieldCount())
	for index := 0; index < value.FieldCount(); index++ {
		found := false
		for fieldIndex := 0; fieldIndex < value.FieldCount(); fieldIndex++ {
			field := value.Field(fieldIndex)
			if strings.Trim(field.Name, `"'`) != strconvItoa(index) {
				continue
			}
			result[index] = CallArgument{Type: field.Type}
			found = true
			break
		}
		if !found {
			return nil, false
		}
	}
	return result, true
}

func (s *functionState) inferReceiver(
	node *phpsyntax.Node,
	env environment,
	static bool,
) types.Type {
	if static && node.Kind() == phpsyntax.PhpName {
		name := phpquery.NameValue(node)
		switch strings.ToLower(name) {
		case "self":
			return s.currentClass
		case "static":
			return s.currentClass
		case "parent":
			if s.currentClass.Kind() == types.ObjectKind {
				parent := types.Unknown()
				s.analyzer.Snapshot.VisitClassViews(
					s.currentClass.Name(),
					func(classView semantic.SymbolView) bool {
						class := classView.Materialize()
						if len(class.Extends) > 0 {
							parent = types.Named(class.Extends[0])
						}
						return false
					},
				)
				if !parent.IsUnknown() {
					return parent
				}
			}
			return types.Unknown()
		default:
			return types.Named(s.nameContextAt(node.Range().Start).ResolveClass(name))
		}
	}
	return s.infer(node, env)
}

func (s *functionState) inferArrowFunction(
	node *phpsyntax.Node,
	env environment,
) types.Type {
	return s.inferArrowFunctionWithParameters(node, env, nil)
}

func (s *functionState) inferArrowFunctionWithParameters(
	node *phpsyntax.Node,
	env environment,
	parameterTypes []types.Type,
) types.Type {
	closureEnv := cloneEnvironment(env)
	parameters := s.callableParameters(node, closureEnv, parameterTypes)
	result := s.infer(lastDirectNode(node), closureEnv)
	if declared := phpquery.MethodReturnType(node); declared != "" {
		result = s.parseNativeType(declared, node.Range().Start)
	}
	return types.Callable(parameters, result)
}

func (s *functionState) inferClosure(
	node *phpsyntax.Node,
	env environment,
) types.Type {
	return s.inferClosureWithParameters(node, env, nil)
}

func (s *functionState) inferClosureWithParameters(
	node *phpsyntax.Node,
	env environment,
	parameterTypes []types.Type,
) types.Type {
	closureEnv := s.newEnvironment(0)
	if !hasDirectTokenText(node, "static") {
		if value, ok := env.get("$this"); ok {
			closureEnv.set("$this", value)
		}
	}
	for cursor := directNodes(node).Cursor(); cursor.Next(); {
		captured := cursor.Node()
		if captured.Kind() != phpsyntax.PhpVariable {
			continue
		}
		name := phpquery.VariableKey(captured)
		if value, ok := env.get(name); ok {
			closureEnv.set(name, value)
		}
	}
	parameters := s.callableParameters(node, closureEnv, parameterTypes)
	nested := *s
	nested.returns = nil
	nested.dependencies = nil
	nested.symbol = semantic.Symbol{ReturnType: types.Unknown()}
	declared := types.Unknown()
	if source := phpquery.MethodReturnType(node); source != "" {
		declared = s.parseNativeType(source, node.Range().Start)
		nested.symbol.ReturnType = declared
	}
	if body := phpquery.DirectChild(node, phpsyntax.PhpBlock); body != nil {
		nested.generator = containsDirectYield(body)
		nested.analyzeBlock(body, closureEnv)
	}
	result := joinTypes(s.relations, nested.returns)
	if !declared.IsUnknown() {
		result = declared
	}
	return types.Callable(parameters, result)
}

func (s *functionState) callableParameters(
	node *phpsyntax.Node,
	env environment,
	parameterTypes []types.Type,
) []types.CallableParameter {
	parameters := phpquery.IterateParameters(node)
	parameterCount := parameters.Len()
	if parameterCount == 0 {
		return nil
	}
	result := make([]types.CallableParameter, 0, parameterCount)
	for parameters.Next() {
		parameter := parameters.Node()
		name := phpquery.ParameterName(parameter)
		value := s.parseNativeType(phpquery.ParameterType(parameter), parameter.Range().Start)
		if len(result) < len(parameterTypes) &&
			!parameterTypes[len(result)].IsUnknown() {
			value = parameterTypes[len(result)]
		}
		env.set(name, value)
		result = append(result, types.CallableParameter{
			Name:     name,
			Type:     value,
			Optional: phpquery.ParameterOptional(parameter),
			Variadic: hasDirectToken(parameter, phpsyntax.TkEllipsis),
		})
	}
	return result
}

func (s *functionState) inferMatch(node *phpsyntax.Node, env environment) types.Type {
	var results []types.Type
	children := directNodes(node)
	matchTrue := false
	var selector *phpsyntax.Node
	if children.Len() > 0 &&
		children.At(0).Kind() != phpsyntax.PhpMatchArm {
		selector = children.At(0)
		s.infer(selector, env)
		matchTrue = isTrueCondition(selector)
		if selector.Kind() == phpsyntax.PhpParenthesized {
			selector = firstDirectNode(selector)
		}
	}
	remaining := cloneEnvironment(env)
	for cursor := children.Cursor(); cursor.Next(); {
		arm := cursor.Node()
		if arm.Kind() != phpsyntax.PhpMatchArm {
			continue
		}
		armNodes := directNodes(arm)
		if armNodes.Len() == 0 {
			continue
		}
		resultNode := armNodes.At(armNodes.Len() - 1)
		armEnv := env
		if matchTrue {
			if matchArmIsDefault(arm) {
				armEnv = remaining
			} else {
				hasCondition := false
				for index := 0; index < armNodes.Len()-1; index++ {
					matched, unmatched := s.conditionEnvironments(
						armNodes.At(index),
						remaining,
					)
					if hasCondition {
						armEnv = joinEnvironments(
							s.relations,
							armEnv,
							matched,
						)
					} else {
						armEnv = matched
						hasCondition = true
					}
					remaining = unmatched
				}
			}
		} else if selector != nil {
			if matchArmIsDefault(arm) {
				armEnv = remaining
			} else {
				hasCondition := false
				for index := 0; index < armNodes.Len()-1; index++ {
					condition := armNodes.At(index)
					matched, unmatched, narrowed := s.narrowClassIdentity(
						selector,
						condition,
						"===",
						remaining,
					)
					if !narrowed {
						if constraint, literalValue := s.conditionLiteralType(condition, remaining); literalValue {
							matched, unmatched = s.narrowLiteral(
								selector,
								constraint,
								"===",
								remaining,
							)
						} else {
							s.infer(condition, remaining)
							matched = cloneEnvironment(remaining)
							unmatched = cloneEnvironment(remaining)
						}
					}
					if hasCondition {
						armEnv = joinEnvironments(s.relations, armEnv, matched)
					} else {
						armEnv = matched
						hasCondition = true
					}
					remaining = unmatched
				}
			}
		}
		results = append(results, s.infer(resultNode, armEnv))
	}
	return joinTypes(s.relations, results)
}

func matchArmIsDefault(node *phpsyntax.Node) bool {
	if node == nil {
		return false
	}
	for index := 0; index < node.ChildCount(); index++ {
		token, ok := node.Child(index).(*phpsyntax.Token)
		if ok && strings.EqualFold(strings.TrimSpace(token.Text()), "default") {
			return true
		}
	}
	return false
}

func (s *functionState) inferName(node *phpsyntax.Node) types.Type {
	switch strings.ToLower(phpquery.NameValue(node)) {
	case "null":
		return types.Null()
	case "true":
		return types.True()
	case "false":
		return types.False()
	default:
		return types.Unknown()
	}
}

func (s *functionState) joinMembers(
	members []types.Type,
	receiver types.Type,
) types.Type {
	var values []types.Type
	for _, member := range members {
		values = append(values, resolveSpecialType(member, receiver, s.currentClass))
	}
	return joinTypes(s.relations, values)
}

func (s *functionState) extensionType(
	node *phpsyntax.Node,
	name string,
	receiver types.Type,
	arguments []CallArgument,
	static bool,
) (types.Type, bool) {
	context := CallContext{
		Snapshot:     s.analyzer.Snapshot,
		Document:     s.document,
		Node:         node,
		Name:         name,
		Receiver:     receiver,
		Arguments:    arguments,
		CurrentClass: s.currentClass,
		Static:       static,
	}
	for _, extension := range s.analyzer.Extensions {
		if fact, ok := extension.InferCall(context); ok {
			s.document.SetTypeFact(semantic.NodeIdentity(node), fact)
			return fact.Type, true
		}
	}
	return types.Unknown(), false
}

func (s *functionState) nameContextAt(offset uint32) resolver.NameContext {
	scope, ok := s.document.ScopeAt(offset)
	if !ok {
		context := resolver.NewNameContext(s.document.Namespace)
		context.PHPDocAliases = s.document.TypeAliases
		return context
	}
	return resolver.NameContext{
		Namespace:     scope.Namespace,
		Imports:       scope.Imports,
		PHPDocAliases: s.document.TypeAliases,
	}
}

func (s *functionState) parseNativeType(source string, offset uint32) types.Type {
	value, err := types.ParseNative(source)
	if err != nil {
		return types.Error()
	}
	return s.nameContextAt(offset).ResolveType(value)
}

func (s *functionState) typeFromSyntax(node *phpsyntax.Node) types.Type {
	return s.parseNativeType(compact(node.Text()), node.Range().Start)
}

func (s *functionState) record(
	node *phpsyntax.Node,
	value types.Type,
	source semantic.TypeSource,
	reason string,
) {
	s.recordWithConfidence(node, value, semantic.InferredConfidence, source, reason)
}

func (s *functionState) recordWithConfidence(
	node *phpsyntax.Node,
	value types.Type,
	confidence semantic.Confidence,
	source semantic.TypeSource,
	reason string,
) {
	if node == nil {
		return
	}
	identity := semantic.NodeIdentity(node)
	// Absence already has the exact semantics of an unknown fact through
	// Document.TypeOf. Keeping explicit unknowns only expands the per-file map
	// and persisted payload without adding information. Delete first so a
	// later fixed-point pass cannot leave a stale known result behind.
	if value.IsUnknown() {
		s.document.DeleteTypeFact(identity)
		return
	}
	s.document.SetTypeFact(identity, semantic.TypeFact{
		Type:       value,
		Confidence: confidence,
		Source:     source,
		Origin:     node.RangeTrimmedTrivia(),
		Reason:     reason,
	})
}
