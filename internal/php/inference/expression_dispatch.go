package inference

import (
	phpquery "github.com/shopware/shopware-lsp/internal/parser/php/query"
	phpsyntax "github.com/shopware/shopware-lsp/internal/parser/php/syntax"
	"github.com/shopware/shopware-lsp/internal/php/literal"
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
