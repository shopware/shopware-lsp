package inference

import (
	"strings"

	phpquery "github.com/shopware/shopware-lsp/internal/parser/php/query"
	phpsyntax "github.com/shopware/shopware-lsp/internal/parser/php/syntax"
	"github.com/shopware/shopware-lsp/internal/php/semantic"
	"github.com/shopware/shopware-lsp/internal/php/types"
)

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
