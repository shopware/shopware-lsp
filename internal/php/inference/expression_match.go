package inference

import (
	"strings"

	phpquery "github.com/shopware/shopware-lsp/internal/parser/php/query"
	phpsyntax "github.com/shopware/shopware-lsp/internal/parser/php/syntax"
	"github.com/shopware/shopware-lsp/internal/php/resolver"
	"github.com/shopware/shopware-lsp/internal/php/semantic"
	"github.com/shopware/shopware-lsp/internal/php/types"
)

func (s *functionState) inferMatch(node *phpsyntax.Node, env environment) types.Type {
	var results []types.Type
	children := directNodes(node)
	matchTrue := false
	var selector *phpsyntax.Node
	if children.Len() > 0 &&
		children.At(0).Kind() != phpsyntax.PhpMatchArm {
		selector = children.At(0)
		s.infer(selector, env)
		matchTrue = isTrueCondition(selector)
		if selector.Kind() == phpsyntax.PhpParenthesized {
			selector = firstDirectNode(selector)
		}
	}
	remaining := s.cloneEnvironment(env)
	for cursor := children.Cursor(); cursor.Next(); {
		arm := cursor.Node()
		if arm.Kind() != phpsyntax.PhpMatchArm {
			continue
		}
		armNodes := directNodes(arm)
		if armNodes.Len() == 0 {
			continue
		}
		resultNode := armNodes.At(armNodes.Len() - 1)
		armEnv := env
		if matchTrue {
			if matchArmIsDefault(arm) {
				armEnv = remaining
			} else {
				hasCondition := false
				for index := 0; index < armNodes.Len()-1; index++ {
					matched, unmatched := s.conditionEnvironments(
						armNodes.At(index),
						remaining,
					)
					if hasCondition {
						armEnv = s.joinEnvironments(
							armEnv,
							matched,
						)
					} else {
						armEnv = matched
						hasCondition = true
					}
					remaining = unmatched
				}
			}
		} else if selector != nil {
			if matchArmIsDefault(arm) {
				armEnv = remaining
			} else {
				hasCondition := false
				for index := 0; index < armNodes.Len()-1; index++ {
					condition := armNodes.At(index)
					matched, unmatched, narrowed := s.narrowClassIdentity(
						selector,
						condition,
						"===",
						remaining,
					)
					if !narrowed {
						if constraint, literalValue := s.conditionLiteralType(condition, remaining); literalValue {
							matched, unmatched = s.narrowLiteral(
								selector,
								constraint,
								"===",
								remaining,
							)
						} else {
							s.infer(condition, remaining)
							matched = s.cloneEnvironment(remaining)
							unmatched = s.cloneEnvironment(remaining)
						}
					}
					if hasCondition {
						armEnv = s.joinEnvironments(armEnv, matched)
					} else {
						armEnv = matched
						hasCondition = true
					}
					remaining = unmatched
				}
			}
		}
		results = append(results, s.infer(resultNode, armEnv))
	}
	return joinTypes(s.relations, results)
}

func matchArmIsDefault(node *phpsyntax.Node) bool {
	if node == nil {
		return false
	}
	for index := 0; index < node.ChildCount(); index++ {
		token, ok := node.Child(index).(*phpsyntax.Token)
		if ok && strings.EqualFold(strings.TrimSpace(token.Text()), "default") {
			return true
		}
	}
	return false
}

func (s *functionState) inferName(node *phpsyntax.Node) types.Type {
	switch strings.ToLower(phpquery.NameValue(node)) {
	case "null":
		return types.Null()
	case "true":
		return types.True()
	case "false":
		return types.False()
	default:
		return types.Unknown()
	}
}

func (s *functionState) joinMembers(
	members []types.Type,
	receiver types.Type,
) types.Type {
	var values []types.Type
	for _, member := range members {
		values = append(values, resolveSpecialType(member, receiver, s.currentClass))
	}
	return joinTypes(s.relations, values)
}

func (s *functionState) extensionType(
	node *phpsyntax.Node,
	name string,
	receiver types.Type,
	arguments []CallArgument,
	static bool,
) (types.Type, bool) {
	context := CallContext{
		Snapshot:     s.analyzer.Snapshot,
		Document:     s.document,
		Node:         node,
		Name:         name,
		Receiver:     receiver,
		Arguments:    arguments,
		CurrentClass: s.currentClass,
		Static:       static,
	}
	for _, extension := range s.analyzer.Extensions {
		if fact, ok := extension.InferCall(context); ok {
			s.document.SetTypeFact(semantic.NodeIdentity(node), fact)
			return fact.Type, true
		}
	}
	return types.Unknown(), false
}

func (s *functionState) nameContextAt(offset uint32) resolver.NameContext {
	scope, ok := s.document.ScopeAt(offset)
	if !ok {
		context := resolver.NewNameContext(s.document.Namespace)
		context.PHPDocAliases = s.document.TypeAliases
		return context
	}
	return resolver.NameContext{
		Namespace:     scope.Namespace,
		Imports:       scope.Imports,
		PHPDocAliases: s.document.TypeAliases,
	}
}

func (s *functionState) parseNativeType(source string, offset uint32) types.Type {
	value, err := types.ParseNative(source)
	if err != nil {
		return types.Error()
	}
	return s.nameContextAt(offset).ResolveType(value)
}

func (s *functionState) typeFromSyntax(node *phpsyntax.Node) types.Type {
	return s.parseNativeType(compact(node.Text()), node.Range().Start)
}

func (s *functionState) record(
	node *phpsyntax.Node,
	value types.Type,
	source semantic.TypeSource,
	reason string,
) {
	s.recordWithConfidence(node, value, semantic.InferredConfidence, source, reason)
}

func (s *functionState) recordWithConfidence(
	node *phpsyntax.Node,
	value types.Type,
	confidence semantic.Confidence,
	source semantic.TypeSource,
	reason string,
) {
	if node == nil {
		return
	}
	identity := semantic.NodeIdentity(node)
	// Absence already has the exact semantics of an unknown fact through
	// Document.TypeOf. Keeping explicit unknowns only expands the per-file map
	// and persisted payload without adding information. Delete first so a
	// later fixed-point pass cannot leave a stale known result behind.
	if value.IsUnknown() {
		s.document.DeleteTypeFact(identity)
		return
	}
	s.document.SetTypeFact(identity, semantic.TypeFact{
		Type:       value,
		Confidence: confidence,
		Source:     source,
		Origin:     node.RangeTrimmedTrivia(),
		Reason:     reason,
	})
}
