package inference

import (
	"strings"

	phpquery "github.com/shopware/shopware-lsp/internal/parser/php/query"
	phpsyntax "github.com/shopware/shopware-lsp/internal/parser/php/syntax"
	"github.com/shopware/shopware-lsp/internal/php/semantic"
	"github.com/shopware/shopware-lsp/internal/php/types"
)

func (s *functionState) narrowClassPredicate(
	call *phpsyntax.Node,
	name string,
	env environment,
) (environment, environment, bool) {
	arguments := phpquery.Arguments(call)
	if len(arguments) == 0 {
		return environment{}, environment{}, false
	}
	expression := phpquery.ArgumentExpression(call, 0)
	key := flowExpressionKey(expression)
	if expression == nil || key == "" {
		return environment{}, environment{}, false
	}
	original, exists := env.get(key)
	if !exists {
		original = s.infer(expression, env)
	}

	var constraint types.Type
	switch name {
	case "class_exists", "interface_exists", "trait_exists", "enum_exists":
		constraint = types.ClassString(types.Object())
	case "is_a", "is_subclass_of":
		if len(arguments) < 2 {
			return environment{}, environment{}, false
		}
		targetExpression := phpquery.ArgumentExpression(call, 1)
		target := s.infer(targetExpression, env)
		targetObject := types.Object()
		if target.Kind() == types.ClassStringKind &&
			target.ArgumentCount() == 1 {
			targetObject = target.Argument(0)
		}
		allowString := name == "is_subclass_of"
		if len(arguments) > 2 {
			allowStringType := s.infer(
				phpquery.ArgumentExpression(call, 2),
				env,
			)
			if allowStringType.Kind() == types.TrueKind {
				allowString = true
			} else if allowStringType.Kind() == types.FalseKind {
				allowString = false
			}
		}
		if allowString {
			switch {
			case s.relations.IsSubtype(original, types.String()):
				constraint = types.ClassString(targetObject)
			case s.relations.IsSubtype(original, types.Object()):
				constraint = targetObject
			default:
				constraint = types.Union(
					targetObject,
					types.ClassString(targetObject),
				)
			}
		} else {
			constraint = targetObject
		}
	default:
		return environment{}, environment{}, false
	}

	trueEnv := cloneEnvironment(env)
	falseEnv := cloneEnvironment(env)
	trueValue := s.relations.Narrow(original, constraint)
	trueEnv.set(key, trueValue)
	falseEnv.set(key, s.relations.Without(original, constraint))
	s.record(expression, trueValue, semantic.FlowSource, name)
	return trueEnv, falseEnv, true
}

func (s *functionState) narrowLiteral(
	valueNode *phpsyntax.Node,
	constraint types.Type,
	operator string,
	env environment,
) (environment, environment) {
	key := conditionFlowExpressionKey(valueNode)
	if key == "" {
		return cloneEnvironment(env), cloneEnvironment(env)
	}
	equal := operator == "==="
	trueEnv := cloneEnvironment(env)
	falseEnv := cloneEnvironment(env)
	original, exists := env.get(key)
	if !exists {
		original = s.infer(valueNode, env)
	}
	if equal {
		trueEnv.set(key, s.relations.Narrow(original, constraint))
		falseEnv.set(key, s.relations.Without(original, constraint))
	} else {
		trueEnv.set(key, s.relations.Without(original, constraint))
		falseEnv.set(key, s.relations.Narrow(original, constraint))
	}
	trueValue, _ := trueEnv.get(key)
	s.record(valueNode, trueValue, semantic.FlowSource, "literal comparison")
	return trueEnv, falseEnv
}

func (s *functionState) truthinessEnvironments(
	expression *phpsyntax.Node,
	env environment,
) (environment, environment) {
	key := flowExpressionKey(expression)
	if key == "" {
		return cloneEnvironment(env), cloneEnvironment(env)
	}
	original, exists := env.get(key)
	if !exists {
		original = s.infer(expression, env)
	}
	truthy := withoutFalsyLiterals(s.relations, original)
	trueEnv := cloneEnvironment(env)
	trueEnv.set(key, truthy)
	s.record(expression, truthy, semantic.FlowSource, "truthiness")
	return trueEnv, cloneEnvironment(env)
}

func (s *functionState) narrowInstanceof(
	valueNode,
	typeNode *phpsyntax.Node,
	env environment,
) (environment, environment) {
	key := conditionFlowExpressionKey(valueNode)
	if key == "" {
		return cloneEnvironment(env), cloneEnvironment(env)
	}
	className := compact(typeNode.Text())
	if typeNode.Kind() == phpsyntax.PhpName {
		className = s.nameContextAt(typeNode.Range().Start).ResolveClass(phpquery.NameValue(typeNode))
	}
	constraint := types.Named(className)
	trueEnv := cloneEnvironment(env)
	falseEnv := cloneEnvironment(env)
	original, exists := env.get(key)
	if !exists {
		original = s.infer(valueNode, env)
	}
	trueValue := s.relations.Narrow(original, constraint)
	falseValue := s.withoutInstanceofConstraint(original, constraint)
	trueEnv.set(key, trueValue)
	falseEnv.set(key, falseValue)
	s.record(valueNode, trueValue, semantic.FlowSource, "instanceof")
	return trueEnv, falseEnv
}

func (s *functionState) withoutInstanceofConstraint(
	original,
	constraint types.Type,
) types.Type {
	if original.Kind() == types.UnionKind {
		result := types.Never()
		for index := 0; index < original.ArgumentCount(); index++ {
			result = s.relations.Join(
				result,
				s.withoutInstanceofConstraint(original.Argument(index), constraint),
			)
		}
		return result
	}
	if original.Kind() == types.ObjectKind &&
		constraint.Kind() == types.ObjectKind &&
		strings.EqualFold(strings.TrimPrefix(original.Name(), "\\"), "DateTimeInterface") {
		switch strings.ToLower(strings.TrimPrefix(constraint.Name(), "\\")) {
		case "datetimeimmutable":
			// DateTimeInterface is an engine-only interface. Userland cannot
			// implement it, so excluding its immutable implementation leaves
			// exactly DateTime.
			return types.Named("DateTime")
		case "datetime":
			return types.Named("DateTimeImmutable")
		}
	}
	return s.relations.Without(original, constraint)
}

func (s *functionState) narrowNull(
	valueNode *phpsyntax.Node,
	operator string,
	env environment,
) (environment, environment) {
	key := conditionFlowExpressionKey(valueNode)
	if key == "" {
		return cloneEnvironment(env), cloneEnvironment(env)
	}
	equal := operator == "===" || operator == "=="
	trueEnv := cloneEnvironment(env)
	falseEnv := cloneEnvironment(env)
	original, exists := env.get(key)
	if !exists {
		original = s.infer(valueNode, env)
	}
	if equal {
		trueValue := s.relations.Narrow(original, types.Null())
		falseValue := s.relations.Without(original, types.Null())
		trueEnv.set(key, trueValue)
		falseEnv.set(key, falseValue)
		s.narrowArrayAccessBase(valueNode, trueEnv, trueValue, true)
		s.narrowArrayAccessBase(valueNode, falseEnv, falseValue, false)
	} else {
		trueValue := s.relations.Without(original, types.Null())
		falseValue := s.relations.Narrow(original, types.Null())
		trueEnv.set(key, trueValue)
		falseEnv.set(key, falseValue)
		s.narrowArrayAccessBase(valueNode, trueEnv, trueValue, false)
		s.narrowArrayAccessBase(valueNode, falseEnv, falseValue, true)
	}
	trueValue, _ := trueEnv.get(key)
	s.record(valueNode, trueValue, semantic.FlowSource, "null comparison")
	return trueEnv, falseEnv
}

func (s *functionState) narrowArrayAccessBase(
	node *phpsyntax.Node,
	env environment,
	value types.Type,
	optional bool,
) {
	base, path, found := s.arrayAccessPath(node, env)
	if !found {
		return
	}
	name := phpquery.VariableKey(base)
	existing, found := env.get(name)
	if !found {
		return
	}
	updated := s.updateFlowArrayPath(
		existing,
		path,
		value,
		optional,
		false,
		0,
	)
	if updated.IsUnknown() || updated.Equal(existing) {
		return
	}
	env.set(name, updated)
	s.record(base, updated, semantic.FlowSource, "array element null comparison")
}
