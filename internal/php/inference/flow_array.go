package inference

import (
	"strings"

	phpquery "github.com/shopware/shopware-lsp/internal/parser/php/query"
	phpsyntax "github.com/shopware/shopware-lsp/internal/parser/php/syntax"
	"github.com/shopware/shopware-lsp/internal/php/resolver"
	"github.com/shopware/shopware-lsp/internal/php/semantic"
	"github.com/shopware/shopware-lsp/internal/php/types"
)

func isEmptyArrayLiteral(node *phpsyntax.Node) bool {
	return node != nil && node.Kind() == phpsyntax.PhpArray &&
		inspectArrayLiteral(node).itemCount == 0
}

func (s *functionState) narrowEmptyArray(
	expression *phpsyntax.Node,
	operator string,
	env environment,
) (environment, environment, bool) {
	key := flowExpressionKey(expression)
	if expression == nil || key == "" {
		return environment{}, environment{}, false
	}
	original, exists := env.get(key)
	if !exists {
		original = s.infer(expression, env)
	}
	nonEmpty, empty, arrayPossible := s.arrayEmptinessTypes(original, 0)
	if !arrayPossible {
		return environment{}, environment{}, false
	}
	trueValue, falseValue := nonEmpty, empty
	if operator == "===" {
		trueValue, falseValue = empty, nonEmpty
	}
	trueEnv := cloneEnvironment(env)
	falseEnv := cloneEnvironment(env)
	trueEnv.set(key, trueValue)
	falseEnv.set(key, falseValue)
	s.record(expression, trueValue, semantic.FlowSource, "empty array comparison")
	return trueEnv, falseEnv, true
}

func (s *functionState) arrayEmptinessTypes(
	value types.Type,
	depth int,
) (nonEmpty, empty types.Type, arrayPossible bool) {
	if depth >= maxSpecialTypeDepth {
		return types.Type{}, types.Type{}, false
	}
	if resolved, found := s.analyzer.Snapshot.ResolveTypeAlias(value); found &&
		!resolved.IsUnknown() && !resolved.Equal(value) {
		return s.arrayEmptinessTypes(resolved, depth+1)
	}
	emptyArray := types.Array(types.ArrayKey(), types.Never())
	switch value.Kind() {
	case types.ArrayKind:
		if value.ArgumentCount() != 2 {
			return types.Type{}, types.Type{}, false
		}
		if value.Argument(1).Kind() == types.NeverKind {
			return types.Never(), emptyArray, true
		}
		return types.NonEmptyArray(value.Argument(0), value.Argument(1)),
			emptyArray, true
	case types.NonEmptyArrayKind:
		return value, types.Never(), true
	case types.ListKind:
		if value.ArgumentCount() != 1 {
			return types.Type{}, types.Type{}, false
		}
		return types.NonEmptyList(value.Argument(0)), emptyArray, true
	case types.NonEmptyListKind:
		return value, types.Never(), true
	case types.ArrayShapeKind:
		for fieldIndex := 0; fieldIndex < value.FieldCount(); fieldIndex++ {
			if !value.Field(fieldIndex).Optional {
				return value, types.Never(), true
			}
		}
		key, element := arrayShapeIterableTypes(value, s.relations)
		if value.IsOpenShape() {
			key = s.relations.Join(key, types.ArrayKey())
			element = s.relations.Join(element, types.Mixed())
		}
		if element.Kind() == types.NeverKind {
			return types.Never(), emptyArray, true
		}
		return types.NonEmptyArray(key, element), emptyArray, true
	case types.UnionKind:
		nonEmptyJoiner := types.NewJoiner(s.relations, types.Never())
		emptyJoiner := types.NewJoiner(s.relations, types.Never())
		foundArray := false
		for index := 0; index < value.ArgumentCount(); index++ {
			member := value.Argument(index)
			memberNonEmpty, memberEmpty, memberArray :=
				s.arrayEmptinessTypes(member, depth+1)
			if memberArray {
				foundArray = true
				nonEmptyJoiner.Add(memberNonEmpty)
				emptyJoiner.Add(memberEmpty)
			} else {
				// A non-array value is always strictly different from [].
				nonEmptyJoiner.Add(member)
			}
		}
		return nonEmptyJoiner.Value(), emptyJoiner.Value(), foundArray
	default:
		return value, types.Never(), false
	}
}

func (s *functionState) narrowHasAccessor(
	call *phpsyntax.Node,
	env environment,
) (environment, environment, bool) {
	name := phpquery.CallMethodName(call)
	targetName := ""
	reuseArguments := false
	switch {
	case strings.EqualFold(name, "has"):
		targetName = "get"
		reuseArguments = true
	case len(name) > 3 && strings.EqualFold(name[:3], "has"):
		if len(phpquery.Arguments(call)) != 0 {
			return environment{}, environment{}, false
		}
		targetName = "get" + name[3:]
	default:
		return environment{}, environment{}, false
	}
	receiverNode := phpquery.CallReceiver(call)
	if receiverNode == nil {
		return environment{}, environment{}, false
	}
	receiver := s.inferReceiver(receiverNode, env, false)
	arguments, uncertain := s.inferArguments(call, env)
	if uncertain {
		return environment{}, environment{}, false
	}
	hasCompatible := false
	(resolver.MemberResolver{
		Snapshot: s.analyzer.Snapshot,
	}).VisitMethods(receiver, name, func(member resolver.ResolvedMember) bool {
		selfType := s.memberSelfType(member.Symbol, receiver)
		symbol := resolveMemberSpecialTypes(member.Symbol, receiver, selfType)
		resolved := resolver.ResolveSignature(s.relations, symbol, arguments)
		if resolved.Compatible && s.relations.IsAssignableTo(resolved.ReturnType, types.Bool()) {
			hasCompatible = true
			return false
		}
		return true
	})
	if !hasCompatible {
		return environment{}, environment{}, false
	}
	targetArguments := arguments
	if !reuseArguments {
		targetArguments = nil
	}
	var returns []types.Type
	(resolver.MemberResolver{
		Snapshot: s.analyzer.Snapshot,
	}).VisitMethods(receiver, targetName, func(member resolver.ResolvedMember) bool {
		selfType := s.memberSelfType(member.Symbol, receiver)
		symbol := resolveMemberSpecialTypes(member.Symbol, receiver, selfType)
		resolved := resolver.ResolveSignature(s.relations, symbol, targetArguments)
		if resolved.Compatible {
			returns = append(returns, resolved.ReturnType)
		}
		return true
	})
	if len(returns) == 0 {
		return environment{}, environment{}, false
	}
	original := joinTypes(s.relations, returns)
	nonNull := s.relations.Without(original, types.Null())
	if nonNull.Kind() == types.NeverKind || nonNull.Equal(original) {
		return environment{}, environment{}, false
	}
	receiverKey := flowExpressionKey(receiverNode)
	if receiverKey == "" {
		return environment{}, environment{}, false
	}
	var target strings.Builder
	target.WriteString(receiverKey)
	target.WriteString("->")
	target.WriteString(targetName)
	target.WriteByte('(')
	if reuseArguments {
		for index := range phpquery.Arguments(call) {
			if index > 0 {
				target.WriteByte(',')
			}
			expression := phpquery.ArgumentExpression(call, index)
			if expression == nil {
				return environment{}, environment{}, false
			}
			target.WriteString(expression.Text())
		}
	}
	target.WriteByte(')')
	key := normalizeFlowExpression(target.String())
	trueEnv := cloneEnvironment(env)
	trueEnv.set(key, nonNull)
	return trueEnv, cloneEnvironment(env), true
}
