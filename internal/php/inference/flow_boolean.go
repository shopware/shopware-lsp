package inference

import (
	"strings"

	phpquery "github.com/shopware/shopware-lsp/internal/parser/php/query"
	phpsyntax "github.com/shopware/shopware-lsp/internal/parser/php/syntax"
	"github.com/shopware/shopware-lsp/internal/php/literal"
	"github.com/shopware/shopware-lsp/internal/php/semantic"
	"github.com/shopware/shopware-lsp/internal/php/types"
)

func mergeRefinedEnvironment(base, refinements environment) environment {
	result := cloneEnvironment(base)
	refinements.visit(func(name string, value types.Type) {
		if original, exists := base.get(name); !exists || !original.Equal(value) {
			result.set(name, value)
		}
	})
	return result
}

func booleanAliasPrefix(name string) string {
	return booleanAliasBindingPrefix + name + ":"
}

func booleanAliasBranchPrefix(name string, truth bool) string {
	branch := "false:"
	if truth {
		branch = "true:"
	}
	return booleanAliasPrefix(name) + branch
}

func (s *functionState) captureBooleanAlias(
	name string,
	condition *phpsyntax.Node,
	env environment,
) {
	if condition == nil || condition.Kind() == phpsyntax.PhpVariable &&
		phpquery.VariableKey(condition) == name {
		return
	}
	trueEnv, falseEnv := s.conditionEnvironments(condition, env)
	for _, branch := range []struct {
		truth bool
		env   environment
	}{{truth: true, env: trueEnv}, {truth: false, env: falseEnv}} {
		prefix := booleanAliasBranchPrefix(name, branch.truth)
		branch.env.visit(func(key string, value types.Type) {
			if strings.HasPrefix(key, booleanAliasBindingPrefix) || key == name {
				return
			}
			original, exists := env.get(key)
			if !exists || original.Equal(value) {
				return
			}
			env.set(prefix+key, value)
		})
	}
}

func booleanAliasEnvironments(
	node *phpsyntax.Node,
	env environment,
) (environment, environment, bool) {
	name := phpquery.VariableKey(node)
	if name == "" {
		return environment{}, environment{}, false
	}
	truePrefix := booleanAliasBranchPrefix(name, true)
	falsePrefix := booleanAliasBranchPrefix(name, false)
	type refinement struct {
		key   string
		value types.Type
		truth bool
	}
	var refinements []refinement
	env.visit(func(key string, value types.Type) {
		switch {
		case strings.HasPrefix(key, truePrefix):
			refinements = append(refinements, refinement{
				key: strings.TrimPrefix(key, truePrefix), value: value, truth: true,
			})
		case strings.HasPrefix(key, falsePrefix):
			refinements = append(refinements, refinement{
				key: strings.TrimPrefix(key, falsePrefix), value: value,
			})
		}
	})
	if len(refinements) == 0 {
		return environment{}, environment{}, false
	}
	trueEnv := cloneEnvironment(env)
	falseEnv := cloneEnvironment(env)
	for _, refinement := range refinements {
		if refinement.truth {
			trueEnv.set(refinement.key, refinement.value)
		} else {
			falseEnv.set(refinement.key, refinement.value)
		}
	}
	return trueEnv, falseEnv, true
}

func predicateFlowTarget(node *phpsyntax.Node) *phpsyntax.Node {
	if node == nil {
		return nil
	}
	if node.Kind() == phpsyntax.PhpParenthesized {
		return predicateFlowTarget(firstDirectNode(node))
	}
	if node.Kind() == phpsyntax.PhpBinaryExpression && directOperator(node) == "??" {
		nodes := directNodes(node)
		if nodes.Len() >= 2 && isNullNode(nodes.At(nodes.Len()-1)) {
			return predicateFlowTarget(nodes.At(0))
		}
	}
	return node
}

func (s *functionState) conditionLiteralType(
	node *phpsyntax.Node,
	env environment,
) (types.Type, bool) {
	if value, ok := literal.TypeOf(node); ok {
		return value, true
	}
	if node == nil ||
		(node.Kind() != phpsyntax.PhpMemberAccess &&
			node.Kind() != phpsyntax.PhpScopedAccess) ||
		!hasDirectToken(node, phpsyntax.TkScopeResolution) {
		return types.Type{}, false
	}
	value := s.infer(node, env)
	switch value.Kind() {
	case types.LiteralStringKind, types.LiteralIntKind,
		types.LiteralFloatKind, types.TrueKind, types.FalseKind,
		types.NullKind:
		return value, true
	default:
		return types.Type{}, false
	}
}

func (s *functionState) narrowNullsafeReceiver(
	expression *phpsyntax.Node,
	trueEnv environment,
	originalEnv environment,
) bool {
	if !hasDirectToken(expression, phpsyntax.TkNullsafeObjectOperator) {
		return false
	}
	receiver := phpquery.CallReceiver(expression)
	if receiver == nil {
		receiver = firstDirectNode(expression)
	}
	key := flowExpressionKey(receiver)
	if key == "" {
		return false
	}
	original, exists := originalEnv.get(key)
	if !exists {
		original = s.infer(receiver, originalEnv)
	}
	value := s.relations.Without(original, types.Null())
	trueEnv.set(key, value)
	s.record(receiver, value, semantic.FlowSource, "nullsafe truthiness")
	return true
}

func (s *functionState) narrowIsset(
	call *phpsyntax.Node,
	env environment,
) (environment, environment, bool) {
	trueEnv := cloneEnvironment(env)
	falseEnv := cloneEnvironment(env)
	arguments := phpquery.Arguments(call)
	narrowed := false
	for index := range arguments {
		expression := phpquery.ArgumentExpression(call, index)
		key := flowExpressionKey(expression)
		if expression == nil || key == "" {
			continue
		}
		original, exists := env.get(key)
		if !exists {
			original = s.infer(expression, env)
		}
		if original.Kind() == types.NeverKind {
			trueEnv.set(impossibleEnvironmentBinding, types.Never())
			if len(arguments) == 1 {
				falseEnv.set(key, types.Null())
			}
			s.record(expression, types.Never(), semantic.FlowSource, "isset")
			narrowed = true
			continue
		}
		trueValue := s.relations.Without(original, types.Null())
		trueEnv.set(key, trueValue)
		s.narrowArrayAccessBase(expression, trueEnv, trueValue, false)
		// With one operand, a false isset() proves that operand is null or
		// absent. With multiple operands it only proves that at least one is,
		// so narrowing every operand on the false branch would be unsound.
		if len(arguments) == 1 {
			falseValue := s.relations.Narrow(original, types.Null())
			falseEnv.set(key, falseValue)
			s.narrowArrayAccessBase(expression, falseEnv, falseValue, true)
		}
		s.record(expression, trueValue, semantic.FlowSource, "isset")
		narrowed = true
	}
	return trueEnv, falseEnv, narrowed
}

func (s *functionState) narrowEmpty(
	call *phpsyntax.Node,
	env environment,
) (environment, environment, bool) {
	expression := phpquery.ArgumentExpression(call, 0)
	if expression == nil || flowExpressionKey(expression) == "" {
		return environment{}, environment{}, false
	}
	truthy, falsy := s.truthinessEnvironments(expression, env)
	// empty($value) is the inverse of the value's ordinary truthiness. The
	// false branch therefore carries the useful non-null/non-falsy refinement.
	return falsy, truthy, true
}
