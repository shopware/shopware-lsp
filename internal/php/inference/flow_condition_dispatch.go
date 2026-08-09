package inference

import (
	"strings"

	phpquery "github.com/shopware/shopware-lsp/internal/parser/php/query"
	phpsyntax "github.com/shopware/shopware-lsp/internal/parser/php/syntax"
	"github.com/shopware/shopware-lsp/internal/php/semantic"
	"github.com/shopware/shopware-lsp/internal/php/types"
)

const booleanAliasBindingPrefix = "\x00boolean-alias:"
const impossibleEnvironmentBinding = "\x00impossible-flow"

func (s *functionState) conditionEnvironments(
	node *phpsyntax.Node,
	env environment,
) (environment, environment) {
	if node == nil {
		return cloneEnvironment(env), cloneEnvironment(env)
	}
	if node.Kind() == phpsyntax.PhpParenthesized {
		return s.conditionEnvironments(firstDirectNode(node), env)
	}
	if node.Kind() == phpsyntax.PhpUnaryExpression && directOperator(node) == "!" {
		falseEnv, trueEnv := s.conditionEnvironments(lastDirectNode(node), env)
		s.record(node, types.Bool(), semantic.FlowSource, "logical condition")
		return trueEnv, falseEnv
	}

	if node.Kind() == phpsyntax.PhpBinaryExpression {
		if trueEnv, falseEnv, narrowed := s.binaryConditionEnvironments(node, env); narrowed {
			return trueEnv, falseEnv
		}
	} else {
		s.infer(node, env)
	}

	switch node.Kind() {
	case phpsyntax.PhpMemberCall:
		if trueEnv, falseEnv, narrowed := s.memberCallConditionEnvironments(node, env); narrowed {
			return trueEnv, falseEnv
		}
	case phpsyntax.PhpMemberAccess:
		if hasDirectToken(node, phpsyntax.TkNullsafeObjectOperator) {
			trueEnv, falseEnv := s.truthinessEnvironments(node, env)
			if s.narrowNullsafeReceiver(node, trueEnv, env) {
				return trueEnv, falseEnv
			}
		}
	case phpsyntax.PhpFunctionCall:
		if trueEnv, falseEnv, narrowed := s.functionCallConditionEnvironments(node, env); narrowed {
			return trueEnv, falseEnv
		}
	}

	if flowExpressionKey(node) != "" {
		if node.Kind() == phpsyntax.PhpVariable {
			if trueEnv, falseEnv, narrowed := booleanAliasEnvironments(node, env); narrowed {
				return trueEnv, falseEnv
			}
		}
		return s.truthinessEnvironments(node, env)
	}
	if node.Kind() == phpsyntax.PhpAssignmentExpression {
		left := firstDirectNode(node)
		if left != nil && left.Kind() == phpsyntax.PhpVariable {
			return s.truthinessEnvironments(left, env)
		}
	}
	return cloneEnvironment(env), cloneEnvironment(env)
}

func (s *functionState) binaryConditionEnvironments(
	node *phpsyntax.Node,
	env environment,
) (environment, environment, bool) {
	nodes := directNodes(node)
	nodeCount := nodes.Len()
	if nodeCount < 2 {
		return environment{}, environment{}, false
	}
	left := nodes.At(0)
	right := nodes.At(nodeCount - 1)
	operator := strings.ToLower(directOperator(node))

	switch operator {
	case "&&", "and":
		leftTrue, leftFalse := s.conditionEnvironments(left, env)
		rightTrue, rightFalse := s.conditionEnvironments(right, leftTrue)
		s.record(node, types.Bool(), semantic.FlowSource, "logical condition")
		return rightTrue, joinEnvironments(s.relations, leftFalse, rightFalse), true
	case "||", "or":
		leftTrue, leftFalse := s.conditionEnvironments(left, env)
		rightTrue, rightFalse := s.conditionEnvironments(right, leftFalse)
		s.record(node, types.Bool(), semantic.FlowSource, "logical condition")
		return joinEnvironments(s.relations, leftTrue, rightTrue), rightFalse, true
	}

	s.infer(node, env)
	if operator == "instanceof" {
		trueEnv, falseEnv := s.narrowInstanceof(left, right, env)
		return trueEnv, falseEnv, true
	}
	if equalityOperator(operator) {
		if isNullNode(left) {
			trueEnv, falseEnv := s.narrowNull(right, operator, env)
			return trueEnv, falseEnv, true
		}
		if isNullNode(right) {
			trueEnv, falseEnv := s.narrowNull(left, operator, env)
			return trueEnv, falseEnv, true
		}
	}
	if operator != "===" && operator != "!==" {
		return environment{}, environment{}, false
	}
	return s.strictComparisonEnvironments(left, right, operator, env)
}

func equalityOperator(operator string) bool {
	return operator == "===" || operator == "!==" || operator == "==" || operator == "!="
}

func (s *functionState) strictComparisonEnvironments(
	left,
	right *phpsyntax.Node,
	operator string,
	env environment,
) (environment, environment, bool) {
	if isEmptyArrayLiteral(left) {
		if trueEnv, falseEnv, narrowed := s.narrowEmptyArray(right, operator, env); narrowed {
			return trueEnv, falseEnv, true
		}
	}
	if isEmptyArrayLiteral(right) {
		if trueEnv, falseEnv, narrowed := s.narrowEmptyArray(left, operator, env); narrowed {
			return trueEnv, falseEnv, true
		}
	}
	if trueEnv, falseEnv, narrowed := s.narrowClassIdentity(left, right, operator, env); narrowed {
		return trueEnv, falseEnv, true
	}
	if constraint, literalValue := s.conditionLiteralType(left, env); literalValue {
		trueEnv, falseEnv := s.narrowLiteral(right, constraint, operator, env)
		return trueEnv, falseEnv, true
	}
	if constraint, literalValue := s.conditionLiteralType(right, env); literalValue {
		trueEnv, falseEnv := s.narrowLiteral(left, constraint, operator, env)
		return trueEnv, falseEnv, true
	}
	return environment{}, environment{}, false
}

func (s *functionState) memberCallConditionEnvironments(
	node *phpsyntax.Node,
	env environment,
) (environment, environment, bool) {
	trueEnv, falseEnv, narrowed := s.narrowCallAssertions(node, env)
	if conventionTrue, conventionFalse, convention := s.narrowHasAccessor(node, env); convention {
		if narrowed {
			trueEnv = mergeRefinedEnvironment(trueEnv, conventionTrue)
			falseEnv = mergeRefinedEnvironment(falseEnv, conventionFalse)
		} else {
			trueEnv, falseEnv = conventionTrue, conventionFalse
		}
		narrowed = true
	}
	if !narrowed {
		trueEnv, falseEnv = s.truthinessEnvironments(node, env)
	}
	if s.narrowNullsafeReceiver(node, trueEnv, env) {
		narrowed = true
	}
	return trueEnv, falseEnv, narrowed
}

func (s *functionState) functionCallConditionEnvironments(
	node *phpsyntax.Node,
	env environment,
) (environment, environment, bool) {
	name := strings.ToLower(strings.TrimPrefix(phpquery.CallMethodName(node), "\\"))
	switch name {
	case "empty":
		if trueEnv, falseEnv, narrowed := s.narrowEmpty(node, env); narrowed {
			return trueEnv, falseEnv, true
		}
	case "isset":
		if trueEnv, falseEnv, narrowed := s.narrowIsset(node, env); narrowed {
			return trueEnv, falseEnv, true
		}
	}
	if trueEnv, falseEnv, narrowed := s.narrowClassPredicate(node, name, env); narrowed {
		return trueEnv, falseEnv, true
	}
	if name == "is_numeric" {
		if trueEnv, falseEnv, narrowed := s.numericPredicateEnvironments(node, env); narrowed {
			return trueEnv, falseEnv, true
		}
	}
	return s.typedPredicateEnvironments(node, name, env)
}

func (s *functionState) numericPredicateEnvironments(
	node *phpsyntax.Node,
	env environment,
) (environment, environment, bool) {
	expression := phpquery.ArgumentExpression(node, 0)
	key := flowExpressionKey(expression)
	if expression == nil || key == "" {
		return environment{}, environment{}, false
	}
	original, exists := env.get(key)
	if !exists {
		original = s.infer(expression, env)
	}
	trueValue := s.relations.Narrow(
		original,
		types.Union(types.Int(), types.Float(), types.String()),
	)
	falseValue := s.relations.Without(
		s.relations.Without(original, types.Int()),
		types.Float(),
	)
	trueEnv := cloneEnvironment(env)
	falseEnv := cloneEnvironment(env)
	trueEnv.set(key, trueValue)
	falseEnv.set(key, falseValue)
	s.record(expression, trueValue, semantic.FlowSource, "is_numeric")
	return trueEnv, falseEnv, true
}

func (s *functionState) typedPredicateEnvironments(
	node *phpsyntax.Node,
	name string,
	env environment,
) (environment, environment, bool) {
	narrowed := predicateType(name)
	if narrowed.IsUnknown() {
		return environment{}, environment{}, false
	}
	arguments := phpquery.Arguments(node)
	if len(arguments) == 0 {
		return environment{}, environment{}, false
	}
	expression := predicateFlowTarget(firstDirectNode(arguments[0]))
	key := flowExpressionKey(expression)
	if key == "" {
		return environment{}, environment{}, false
	}
	original, exists := env.get(key)
	if !exists {
		original = s.infer(expression, env)
	}
	trueValue := s.relations.Narrow(original, narrowed)
	falseValue := s.relations.Without(original, narrowed)
	trueEnv := cloneEnvironment(env)
	falseEnv := cloneEnvironment(env)
	trueEnv.set(key, trueValue)
	falseEnv.set(key, falseValue)
	s.record(expression, trueValue, semantic.FlowSource, name)
	return trueEnv, falseEnv, true
}
