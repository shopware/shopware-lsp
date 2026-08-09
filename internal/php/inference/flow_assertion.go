package inference

import (
	"strings"

	phpquery "github.com/shopware/shopware-lsp/internal/parser/php/query"
	phpsyntax "github.com/shopware/shopware-lsp/internal/parser/php/syntax"
	"github.com/shopware/shopware-lsp/internal/php/resolver"
	"github.com/shopware/shopware-lsp/internal/php/semantic"
	"github.com/shopware/shopware-lsp/internal/php/types"
)

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
