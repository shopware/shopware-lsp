package inference

import (
	"strings"

	phpquery "github.com/shopware/shopware-lsp/internal/parser/php/query"
	phpsyntax "github.com/shopware/shopware-lsp/internal/parser/php/syntax"
	"github.com/shopware/shopware-lsp/internal/php/literal"
	"github.com/shopware/shopware-lsp/internal/php/resolver"
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
		nodes := directNodes(node)
		nodeCount := nodes.Len()
		if nodeCount >= 2 {
			operator := strings.ToLower(directOperator(node))
			if operator == "&&" || operator == "and" {
				leftTrue, leftFalse := s.conditionEnvironments(nodes.At(0), env)
				rightTrue, rightFalse := s.conditionEnvironments(
					nodes.At(nodeCount-1),
					leftTrue,
				)
				s.record(node, types.Bool(), semantic.FlowSource, "logical condition")
				return rightTrue, joinEnvironments(s.relations, leftFalse, rightFalse)
			}
			if operator == "||" || operator == "or" {
				leftTrue, leftFalse := s.conditionEnvironments(nodes.At(0), env)
				rightTrue, rightFalse := s.conditionEnvironments(
					nodes.At(nodeCount-1),
					leftFalse,
				)
				s.record(node, types.Bool(), semantic.FlowSource, "logical condition")
				return joinEnvironments(s.relations, leftTrue, rightTrue), rightFalse
			}
			s.infer(node, env)
			if operator == "instanceof" {
				return s.narrowInstanceof(
					nodes.At(0),
					nodes.At(nodeCount-1),
					env,
				)
			}
			if operator == "===" || operator == "!==" || operator == "==" || operator == "!=" {
				if isNullNode(nodes.At(0)) {
					return s.narrowNull(
						nodes.At(nodeCount-1),
						operator,
						env,
					)
				}
				if isNullNode(nodes.At(nodeCount - 1)) {
					return s.narrowNull(nodes.At(0), operator, env)
				}
			}
			if operator == "===" || operator == "!==" {
				if isEmptyArrayLiteral(nodes.At(0)) {
					if trueEnv, falseEnv, narrowed := s.narrowEmptyArray(
						nodes.At(nodeCount-1),
						operator,
						env,
					); narrowed {
						return trueEnv, falseEnv
					}
				}
				if isEmptyArrayLiteral(nodes.At(nodeCount - 1)) {
					if trueEnv, falseEnv, narrowed := s.narrowEmptyArray(
						nodes.At(0),
						operator,
						env,
					); narrowed {
						return trueEnv, falseEnv
					}
				}
				if trueEnv, falseEnv, narrowed := s.narrowClassIdentity(
					nodes.At(0),
					nodes.At(nodeCount-1),
					operator,
					env,
				); narrowed {
					return trueEnv, falseEnv
				}
				if constraint, literalValue := s.conditionLiteralType(
					nodes.At(0),
					env,
				); literalValue {
					return s.narrowLiteral(
						nodes.At(nodeCount-1),
						constraint,
						operator,
						env,
					)
				}
				if constraint, literalValue := s.conditionLiteralType(
					nodes.At(nodeCount-1),
					env,
				); literalValue {
					return s.narrowLiteral(
						nodes.At(0),
						constraint,
						operator,
						env,
					)
				}
			}
		}
	} else {
		s.infer(node, env)
	}
	if node.Kind() == phpsyntax.PhpMemberCall {
		trueEnv, falseEnv, narrowed := s.narrowCallAssertions(node, env)
		if conventionTrue, conventionFalse, convention := s.narrowHasAccessor(
			node,
			env,
		); convention {
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
		if narrowed {
			return trueEnv, falseEnv
		}
	}
	if node.Kind() == phpsyntax.PhpMemberAccess &&
		hasDirectToken(node, phpsyntax.TkNullsafeObjectOperator) {
		trueEnv, falseEnv := s.truthinessEnvironments(node, env)
		if s.narrowNullsafeReceiver(node, trueEnv, env) {
			return trueEnv, falseEnv
		}
	}
	if node.Kind() == phpsyntax.PhpFunctionCall {
		name := strings.ToLower(strings.TrimPrefix(
			phpquery.CallMethodName(node),
			"\\",
		))
		if name == "isset" {
			if trueEnv, falseEnv, narrowed := s.narrowIsset(node, env); narrowed {
				return trueEnv, falseEnv
			}
		}
		if trueEnv, falseEnv, narrowed := s.narrowClassPredicate(
			node,
			name,
			env,
		); narrowed {
			return trueEnv, falseEnv
		}
		if name == "is_numeric" {
			expression := phpquery.ArgumentExpression(node, 0)
			key := flowExpressionKey(expression)
			if expression != nil && key != "" {
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
				s.record(expression, trueValue, semantic.FlowSource, name)
				return trueEnv, falseEnv
			}
		}
		narrowed := predicateType(name)
		if !narrowed.IsUnknown() {
			arguments := phpquery.Arguments(node)
			if len(arguments) > 0 {
				expression := predicateFlowTarget(firstDirectNode(arguments[0]))
				key := flowExpressionKey(expression)
				if key != "" {
					trueEnv := cloneEnvironment(env)
					falseEnv := cloneEnvironment(env)
					original, exists := env.get(key)
					if !exists {
						original = s.infer(expression, env)
					}
					trueValue := s.relations.Narrow(original, narrowed)
					falseValue := s.relations.Without(original, narrowed)
					trueEnv.set(key, trueValue)
					falseEnv.set(key, falseValue)
					s.record(expression, trueValue, semantic.FlowSource, name)
					return trueEnv, falseEnv
				}
			}
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
	for _, member := range (resolver.MemberResolver{
		Snapshot: s.analyzer.Snapshot,
	}).Methods(receiver, name) {
		selfType := s.memberSelfType(member.Symbol, receiver)
		symbol := resolveMemberSpecialTypes(member.Symbol, receiver, selfType)
		resolved := resolver.ResolveSignature(s.relations, symbol, arguments)
		if resolved.Compatible && s.relations.IsAssignableTo(resolved.ReturnType, types.Bool()) {
			hasCompatible = true
			break
		}
	}
	if !hasCompatible {
		return environment{}, environment{}, false
	}
	targetArguments := arguments
	if !reuseArguments {
		targetArguments = nil
	}
	var returns []types.Type
	for _, member := range (resolver.MemberResolver{
		Snapshot: s.analyzer.Snapshot,
	}).Methods(receiver, targetName) {
		selfType := s.memberSelfType(member.Symbol, receiver)
		symbol := resolveMemberSpecialTypes(member.Symbol, receiver, selfType)
		resolved := resolver.ResolveSignature(s.relations, symbol, targetArguments)
		if resolved.Compatible {
			returns = append(returns, resolved.ReturnType)
		}
	}
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

func (s *functionState) narrowCallAssertions(
	call *phpsyntax.Node,
	env environment,
) (environment, environment, bool) {
	receiverNode := firstDirectNode(call)
	if receiverNode == nil {
		return environment{}, environment{}, false
	}
	receiver := s.infer(receiverNode, env)
	methodName := phpquery.CallMethodName(call)
	if methodName == "" {
		return environment{}, environment{}, false
	}

	trueEnv := cloneEnvironment(env)
	falseEnv := cloneEnvironment(env)
	narrowed := false
	arguments, _ := s.inferArguments(call, env)
	for _, member := range (resolver.MemberResolver{
		Snapshot: s.analyzer.Snapshot,
	}).Methods(receiver, methodName) {
		resolved := resolver.ResolveSignature(s.relations, member.Symbol, arguments)
		if !resolved.Compatible {
			continue
		}
		for _, assertion := range member.Symbol.Assertions {
			if !assertion.Conditional {
				continue
			}
			target := assertionTargetNode(call, receiverNode, member.Symbol, assertion.Target)
			assertionType := types.Substitute(assertion.Type, resolved.Templates)
			key := flowExpressionKey(target)
			original := types.Unknown()
			if key == "" {
				var found bool
				key, original, found = s.assertionReceiverMemberTarget(
					receiverNode,
					receiver,
					assertion.Target,
				)
				if !found {
					continue
				}
			}
			if types.ContainsUncertain(assertionType) {
				continue
			}
			if existing, exists := env.get(key); exists {
				original = existing
			} else if original.IsUnknown() {
				original = s.infer(target, env)
			}
			trueValue := applyTypeAssertion(
				s.relations, original, assertionType,
				assertion.Negated == assertion.WhenTrue,
			)
			falseValue := applyTypeAssertion(
				s.relations, original, assertionType,
				assertion.Negated != assertion.WhenTrue,
			)
			trueEnv.set(key, trueValue)
			falseEnv.set(key, falseValue)
			if target != nil {
				s.record(target, trueValue, semantic.FlowSource, "conditional assertion")
			}
			narrowed = true
		}
	}
	return trueEnv, falseEnv, narrowed
}

func (s *functionState) applyUnconditionalCallAssertions(
	call *phpsyntax.Node,
	env environment,
) bool {
	if call == nil ||
		(call.Kind() != phpsyntax.PhpFunctionCall &&
			call.Kind() != phpsyntax.PhpMemberCall &&
			call.Kind() != phpsyntax.PhpScopedCall) {
		return false
	}
	receiverNode := phpquery.CallReceiver(call)
	var symbols []semantic.Symbol
	methodName := phpquery.CallMethodName(call)
	if receiverNode != nil {
		receiver := s.inferReceiver(
			receiverNode,
			env,
			call.Kind() == phpsyntax.PhpScopedCall,
		)
		for _, member := range (resolver.MemberResolver{
			Snapshot: s.analyzer.Snapshot,
		}).Methods(receiver, methodName) {
			symbols = append(symbols, member.Symbol)
		}
		if len(symbols) == 0 && s.currentClassIsTrait() {
			// A trait does not declare which class will consume it, so static
			// PHPUnit assertions cannot be resolved through the trait's own
			// hierarchy. Use Assert's indexed declarations as the implicit
			// contract instead of duplicating their assertion semantics here.
			for _, member := range (resolver.MemberResolver{
				Snapshot: s.analyzer.Snapshot,
			}).Methods(types.Named("PHPUnit\\Framework\\Assert"), methodName) {
				symbols = append(symbols, member.Symbol)
			}
		}
	} else if call.Kind() == phpsyntax.PhpFunctionCall {
		name := methodName
		context := s.nameContextAt(call.Range().Start)
		context.VisitFunctionNames(name, func(candidate string) bool {
			s.analyzer.Snapshot.VisitFunctionViews(
				candidate,
				func(view semantic.SymbolView) bool {
					symbols = append(symbols, view.Materialize())
					return true
				},
			)
			return true
		})
	}

	hasAssertions := false
	for _, symbol := range symbols {
		for _, assertion := range symbol.Assertions {
			if !assertion.Conditional {
				hasAssertions = true
				break
			}
		}
		if hasAssertions {
			break
		}
	}
	if !hasAssertions {
		return false
	}
	arguments, _ := s.inferArguments(call, env)
	applied := false
	for _, symbol := range symbols {
		resolved := resolver.ResolveSignature(s.relations, symbol, arguments)
		if !resolved.Compatible {
			continue
		}
		for _, assertion := range symbol.Assertions {
			if assertion.Conditional {
				continue
			}
			target := assertionTargetNode(
				call,
				receiverNode,
				symbol,
				assertion.Target,
			)
			key := flowExpressionKey(target)
			assertionType := types.Substitute(assertion.Type, resolved.Templates)
			if key == "" || types.ContainsUncertain(assertionType) {
				continue
			}
			original, exists := env.get(key)
			if !exists {
				original = s.infer(target, env)
			}
			value := applyTypeAssertion(
				s.relations,
				original,
				assertionType,
				assertion.Negated,
			)
			env.set(key, value)
			s.record(target, value, semantic.FlowSource, "unconditional assertion")
			applied = true
		}
	}
	return applied
}

func (s *functionState) currentClassIsTrait() bool {
	container := s.symbol.Container
	if container == "" {
		return false
	}
	if symbol, ok := s.analyzer.Snapshot.Symbol(container); ok {
		return symbol.Kind == semantic.TraitSymbol
	}
	for _, symbol := range s.document.Symbols {
		if symbol.ID == container {
			return symbol.Kind == semantic.TraitSymbol
		}
	}
	return false
}

func applyTypeAssertion(
	relations types.Relations,
	original,
	asserted types.Type,
	negated bool,
) types.Type {
	if negated {
		return relations.Without(original, asserted)
	}
	return relations.Narrow(original, asserted)
}

func assertionTargetNode(
	call,
	receiver *phpsyntax.Node,
	method semantic.Symbol,
	target string,
) *phpsyntax.Node {
	if target == "$this" {
		return receiver
	}
	for index, parameter := range method.Parameters {
		if parameter.Name == target {
			return phpquery.ArgumentExpression(call, index)
		}
	}
	return nil
}

func (s *functionState) assertionReceiverMemberTarget(
	receiverNode *phpsyntax.Node,
	receiver types.Type,
	target string,
) (string, types.Type, bool) {
	const receiverPrefix = "$this->"
	if !strings.HasPrefix(target, receiverPrefix) {
		return "", types.Type{}, false
	}
	memberName := strings.TrimPrefix(target, receiverPrefix)
	methodTarget := strings.HasSuffix(memberName, "()")
	memberName = strings.TrimSuffix(memberName, "()")
	if memberName == "" || strings.ContainsAny(memberName, "()$>, ") {
		return "", types.Type{}, false
	}
	receiverKey := flowExpressionKey(receiverNode)
	if receiverKey == "" {
		return "", types.Type{}, false
	}
	key := normalizeFlowExpression(
		receiverKey + strings.TrimPrefix(target, "$this"),
	)
	var returns []types.Type
	memberResolver := resolver.MemberResolver{Snapshot: s.analyzer.Snapshot}
	if methodTarget {
		for _, member := range memberResolver.Methods(receiver, memberName) {
			selfType := s.memberSelfType(member.Symbol, receiver)
			symbol := resolveMemberSpecialTypes(member.Symbol, receiver, selfType)
			resolved := resolver.ResolveSignature(s.relations, symbol, nil)
			if resolved.Compatible {
				returns = append(returns, resolved.ReturnType)
			}
		}
	} else {
		returns = append(returns, memberResolver.PropertyTypes(receiver, memberName)...)
	}
	if len(returns) == 0 {
		return "", types.Type{}, false
	}
	return key, joinTypes(s.relations, returns), true
}

func (s *functionState) narrowClassIdentity(
	left,
	right *phpsyntax.Node,
	operator string,
	env environment,
) (environment, environment, bool) {
	valueNode, classNode, found := classIdentityOperands(left, right)
	if !found {
		valueNode, classNode, found = classIdentityOperands(right, left)
	}
	if !found {
		return environment{}, environment{}, false
	}

	key := flowExpressionKey(valueNode)
	if key == "" {
		return environment{}, environment{}, false
	}
	className := s.nameContextAt(classNode.Range().Start).ResolveClass(
		phpquery.NameValue(classNode),
	)
	constraint := types.Named(className)
	if constraint.IsUnknown() {
		return environment{}, environment{}, false
	}
	original, exists := env.get(key)
	if !exists {
		original = s.infer(valueNode, env)
	}
	equalEnv := cloneEnvironment(env)
	notEqualEnv := cloneEnvironment(env)
	equalEnv.set(key, s.relations.Narrow(original, constraint))
	notEqualEnv.set(key, s.relations.Without(original, constraint))
	equalValue, _ := equalEnv.get(key)
	s.record(valueNode, equalValue, semantic.FlowSource, "class identity")
	if operator == "!==" {
		return notEqualEnv, equalEnv, true
	}
	return equalEnv, notEqualEnv, true
}

func classIdentityOperands(
	valueAccess,
	classAccess *phpsyntax.Node,
) (*phpsyntax.Node, *phpsyntax.Node, bool) {
	value := classConstantReceiver(valueAccess)
	class := classConstantReceiver(classAccess)
	if value == nil || class == nil ||
		flowExpressionKey(value) == "" || class.Kind() != phpsyntax.PhpName {
		return nil, nil, false
	}
	return value, class, true
}

func classConstantReceiver(node *phpsyntax.Node) *phpsyntax.Node {
	if node == nil ||
		(node.Kind() != phpsyntax.PhpMemberAccess &&
			node.Kind() != phpsyntax.PhpScopedAccess) ||
		!hasDirectToken(node, phpsyntax.TkScopeResolution) {
		return nil
	}
	nodes := directNodes(node)
	if nodes.Len() < 2 || !strings.EqualFold(
		phpquery.NameValue(nodes.At(nodes.Len()-1)),
		"class",
	) {
		return nil
	}
	return nodes.At(0)
}

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
	truthy := s.relations.Without(original, types.Null())
	truthy = s.relations.Without(truthy, types.False())
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

func flowExpressionKey(node *phpsyntax.Node) string {
	if node == nil {
		return ""
	}
	switch node.Kind() {
	case phpsyntax.PhpVariable:
		return phpquery.VariableKey(node)
	case phpsyntax.PhpMemberAccess, phpsyntax.PhpMemberCall,
		phpsyntax.PhpScopedAccess, phpsyntax.PhpScopedCall,
		phpsyntax.PhpArrayAccess:
		source := node.Text()
		fullRange := node.Range()
		trimmedRange := node.RangeTrimmedTrivia()
		if trimmedRange.Start > fullRange.Start {
			source = source[trimmedRange.Start-fullRange.Start:]
		}
		return normalizeFlowExpression(source)
	default:
		return ""
	}
}

// conditionFlowExpressionKey unwraps value-preserving condition syntax while
// keeping flowExpressionKey suitable for expression-result caching. An
// assignment expression has the value of its left-hand target, but treating
// it as that target during ordinary inference would skip the assignment.
func conditionFlowExpressionKey(node *phpsyntax.Node) string {
	if node == nil {
		return ""
	}
	switch node.Kind() {
	case phpsyntax.PhpParenthesized:
		return conditionFlowExpressionKey(firstDirectNode(node))
	case phpsyntax.PhpAssignmentExpression:
		return flowExpressionKey(firstDirectNode(node))
	default:
		return flowExpressionKey(node)
	}
}

// normalizeFlowExpression produces the stable environment key used for
// repeated member and array expressions. Ordinary compact expressions borrow
// their source text without allocating. Only whitespace or nullsafe access
// requires a normalized copy.
func normalizeFlowExpression(source string) string {
	source = strings.TrimSpace(source)
	needsNormalization := false
	for index := 0; index < len(source); index++ {
		switch source[index] {
		case ' ', '\t', '\r', '\n':
			needsNormalization = true
		case '?':
			if index+2 < len(source) &&
				source[index+1] == '-' &&
				source[index+2] == '>' {
				needsNormalization = true
			}
		}
		if needsNormalization {
			break
		}
	}
	if !needsNormalization {
		return source
	}
	var result strings.Builder
	result.Grow(len(source))
	for index := 0; index < len(source); index++ {
		switch source[index] {
		case ' ', '\t', '\r', '\n':
			continue
		case '?':
			if index+2 < len(source) &&
				source[index+1] == '-' &&
				source[index+2] == '>' {
				result.WriteString("->")
				index += 2
				continue
			}
		}
		result.WriteByte(source[index])
	}
	return result.String()
}

func predicateType(name string) types.Type {
	switch name {
	case "is_string":
		return types.String()
	case "is_int", "is_integer", "is_long":
		return types.Int()
	case "is_float", "is_double", "is_real":
		return types.Float()
	case "is_bool":
		return types.Bool()
	case "is_array":
		return types.Array(types.Mixed(), types.Mixed())
	case "is_callable":
		return types.Callable(nil, types.Mixed())
	case "is_iterable":
		return types.Iterable(types.Mixed(), types.Mixed())
	case "is_object":
		return types.Object()
	case "is_resource":
		return types.Resource()
	case "is_null":
		return types.Null()
	default:
		return types.Unknown()
	}
}

func isNullNode(node *phpsyntax.Node) bool {
	return node != nil && (node.Kind() == phpsyntax.PhpNull ||
		node.Kind() == phpsyntax.PhpName && strings.EqualFold(compact(node.Text()), "null"))
}

func cloneEnvironment(source environment) environment {
	if source.handle == nil {
		return newEnvironment(0)
	}
	source.handle.shared = true
	handle := newEnvironmentHandle(source.handle.arena)
	handle.bindings = source.handle.bindings
	handle.table = source.handle.table
	handle.shared = true
	return environment{handle: handle}
}

func environmentIsImpossible(env environment) bool {
	_, impossible := env.get(impossibleEnvironmentBinding)
	return impossible
}

func joinEnvironments(
	relations types.Relations,
	left,
	right environment,
) environment {
	if environmentIsImpossible(left) {
		return cloneEnvironment(right)
	}
	if environmentIsImpossible(right) {
		return cloneEnvironment(left)
	}
	// The branches normally contain the same variables, so their union is
	// usually close to the larger frame rather than the sum of both sizes.
	// Start with that tighter hint and let the hybrid frame grow if branches
	// introduced disjoint bindings.
	var arena *environmentArena
	if left.handle != nil {
		arena = left.handle.arena
	}
	if arena == nil && right.handle != nil {
		arena = right.handle.arena
	}
	result := newEnvironmentIn(arena, max(left.len(), right.len()))
	joinLeftEnvironmentValues(relations, result, left, right)
	addMissingEnvironmentValues(result, right)
	return result
}

func joinLeftEnvironmentValues(
	relations types.Relations,
	result,
	left,
	right environment,
) {
	if left.handle == nil {
		return
	}
	if left.handle.table != nil {
		for name, value := range left.handle.table {
			other, present := right.get(name)
			if strings.HasPrefix(name, booleanAliasBindingPrefix) && !present {
				continue
			}
			if present {
				value = relations.Join(value, other)
			}
			result.set(name, value)
		}
		return
	}
	for _, binding := range left.handle.bindings {
		value := binding.value
		other, present := right.get(binding.name)
		if strings.HasPrefix(binding.name, booleanAliasBindingPrefix) && !present {
			continue
		}
		if present {
			value = relations.Join(value, other)
		}
		result.set(binding.name, value)
	}
}

func addMissingEnvironmentValues(result, source environment) {
	if source.handle == nil {
		return
	}
	if source.handle.table != nil {
		for name, value := range source.handle.table {
			if strings.HasPrefix(name, booleanAliasBindingPrefix) {
				continue
			}
			if _, exists := result.get(name); !exists {
				result.set(name, value)
			}
		}
		return
	}
	for _, binding := range source.handle.bindings {
		if strings.HasPrefix(binding.name, booleanAliasBindingPrefix) {
			continue
		}
		if _, exists := result.get(binding.name); !exists {
			result.set(binding.name, binding.value)
		}
	}
}
