package inference

import (
	"slices"
	"strings"

	phpquery "github.com/shopware/shopware-lsp/internal/parser/php/query"
	phpsyntax "github.com/shopware/shopware-lsp/internal/parser/php/syntax"
	"github.com/shopware/shopware-lsp/internal/php/semantic"
	"github.com/shopware/shopware-lsp/internal/php/types"
)

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
		for id := range scope.SymbolIDs(s.document.Symbols, name) {
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
