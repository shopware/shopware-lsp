package inference

import (
	"strings"

	phpquery "github.com/shopware/shopware-lsp/internal/parser/php/query"
	phpsyntax "github.com/shopware/shopware-lsp/internal/parser/php/syntax"
	"github.com/shopware/shopware-lsp/internal/php/semantic"
	"github.com/shopware/shopware-lsp/internal/php/types"
)

func (s *functionState) applyUnset(call *phpsyntax.Node, env environment) {
	for index := range phpquery.Arguments(call) {
		expression := phpquery.ArgumentExpression(call, index)
		base, path, found := s.arrayAccessPath(expression, env)
		if !found {
			continue
		}
		name := phpquery.VariableKey(base)
		existing, found := env.get(name)
		if !found {
			continue
		}
		updated := s.updateFlowArrayPath(
			existing,
			path,
			types.Unknown(),
			true,
			true,
			0,
		)
		if updated.IsUnknown() || updated.Equal(existing) {
			continue
		}
		env.set(name, updated)
		s.record(base, updated, semantic.AssignmentSource, "unset array field")
	}
}

func (s *functionState) updateFlowArrayPath(
	existing types.Type,
	path []arrayAccessSegment,
	value types.Type,
	optional,
	remove bool,
	depth int,
) types.Type {
	if len(path) == 0 || depth >= maxSpecialTypeDepth {
		return types.Unknown()
	}
	if len(path) == 1 {
		if !path[0].literal {
			return existing
		}
		return s.updateFlowArrayField(
			existing,
			path[0].fieldName,
			value,
			optional,
			remove,
			depth+1,
		)
	}
	if resolved, found := s.analyzer.Snapshot.ResolveTypeAlias(existing); found &&
		!resolved.IsUnknown() && !resolved.Equal(existing) {
		return s.updateFlowArrayPath(
			resolved,
			path,
			value,
			optional,
			remove,
			depth+1,
		)
	}
	if existing.Kind() == types.UnionKind {
		alternatives := make([]types.Type, 0, existing.ArgumentCount())
		for index := 0; index < existing.ArgumentCount(); index++ {
			updated := s.updateFlowArrayPath(
				existing.Argument(index),
				path,
				value,
				optional,
				remove,
				depth+1,
			)
			if !updated.IsUnknown() {
				alternatives = append(alternatives, updated)
			}
		}
		return types.Union(alternatives...)
	}

	head := path[0]
	switch existing.Kind() {
	case types.ArrayKind, types.NonEmptyArrayKind:
		if existing.ArgumentCount() != 2 {
			return types.Unknown()
		}
		updated := s.updateFlowArrayPath(
			existing.Argument(1),
			path[1:],
			value,
			optional,
			remove,
			depth+1,
		)
		if updated.IsUnknown() {
			return types.Unknown()
		}
		if existing.Kind() == types.NonEmptyArrayKind && !remove {
			return types.NonEmptyArray(existing.Argument(0), updated)
		}
		return types.Array(existing.Argument(0), updated)
	case types.ListKind, types.NonEmptyListKind:
		if existing.ArgumentCount() != 1 {
			return types.Unknown()
		}
		updated := s.updateFlowArrayPath(
			existing.Argument(0),
			path[1:],
			value,
			optional,
			remove,
			depth+1,
		)
		if updated.IsUnknown() {
			return types.Unknown()
		}
		if existing.Kind() == types.NonEmptyListKind && !remove {
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
			updated := s.updateFlowArrayPath(
				fields[index].Type,
				path[1:],
				value,
				optional,
				remove,
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

func (s *functionState) updateFlowArrayField(
	existing types.Type,
	fieldName string,
	value types.Type,
	optional,
	remove bool,
	depth int,
) types.Type {
	if depth >= maxSpecialTypeDepth {
		return types.Unknown()
	}
	if resolved, found := s.analyzer.Snapshot.ResolveTypeAlias(existing); found &&
		!resolved.IsUnknown() && !resolved.Equal(existing) {
		return s.updateFlowArrayField(
			resolved,
			fieldName,
			value,
			optional,
			remove,
			depth+1,
		)
	}
	if existing.Kind() == types.UnionKind {
		alternatives := make([]types.Type, 0, existing.ArgumentCount())
		for index := 0; index < existing.ArgumentCount(); index++ {
			updated := s.updateFlowArrayField(
				existing.Argument(index),
				fieldName,
				value,
				optional,
				remove,
				depth+1,
			)
			if !updated.IsUnknown() {
				alternatives = append(alternatives, updated)
			}
		}
		return types.Union(alternatives...)
	}
	if existing.Kind() != types.ArrayShapeKind {
		return existing
	}
	fields := make([]types.ShapeField, 0, existing.FieldCount())
	found := false
	for index := 0; index < existing.FieldCount(); index++ {
		field := existing.Field(index)
		if strings.Trim(field.Name, `"'`) != fieldName {
			fields = append(fields, field)
			continue
		}
		found = true
		if remove {
			continue
		}
		if optional && value.Kind() == types.NullKind && field.Optional &&
			s.relations.Without(field.Type, types.Null()).Equal(field.Type) {
			// For a non-null optional field, false isset() means the key is
			// absent. Keep absence in the shape's optional bit instead of
			// inventing null as a possible value when the branches rejoin.
			continue
		}
		if value.Kind() == types.NeverKind {
			if field.Optional {
				continue
			}
			return types.Unknown()
		}
		field.Type = value
		field.Optional = optional
		fields = append(fields, field)
	}
	if !found {
		if !optional && !remove {
			return types.Unknown()
		}
		return existing
	}
	return types.ArrayShapeOwned(fields, existing.IsOpenShape())
}
