package inference

import (
	"strings"

	phpquery "github.com/shopware/shopware-lsp/internal/parser/php/query"
	phpsyntax "github.com/shopware/shopware-lsp/internal/parser/php/syntax"
	"github.com/shopware/shopware-lsp/internal/php/resolver"
	"github.com/shopware/shopware-lsp/internal/php/semantic"
	"github.com/shopware/shopware-lsp/internal/php/types"
)

// LinkMembers resolves member-name references after expression inference has
// established receiver types. Keeping this pass separate makes the binder
// independent from type inference while still enabling definitions/references.
func LinkMembers(
	document *semantic.Document,
	snapshot *semantic.Snapshot,
	root *phpsyntax.Node,
) *semantic.Document {
	if document == nil || snapshot == nil || root == nil {
		return document
	}
	return LinkMembersOwned(document.Clone(), snapshot, root)
}

// LinkMembersOwned appends resolved member references to a document
// exclusively owned by the caller.
func LinkMembersOwned(
	document *semantic.Document,
	snapshot *semantic.Snapshot,
	root *phpsyntax.Node,
) *semantic.Document {
	if document == nil || snapshot == nil || root == nil {
		return document
	}
	result := document
	memberResolver := resolver.MemberResolver{Snapshot: snapshot}
	var staticReceiverTypes namedTypeCache
	phpquery.Visit(root, func(node *phpsyntax.Node) bool {
		nodes := directNodes(node)
		if nodes.Len() < 2 {
			return true
		}
		static := node.Kind() == phpsyntax.PhpScopedCall ||
			node.Kind() == phpsyntax.PhpScopedAccess ||
			hasDirectToken(node, phpsyntax.TkScopeResolution)
		receiverNode := nodes.At(0)
		receiver := result.TypeOf(receiverNode).Type
		if receiver.IsUnknown() && receiverNode.Kind() == phpsyntax.PhpVariable {
			receiver = declaredVariableType(
				result,
				snapshot,
				receiverNode,
			)
		}
		if receiver.IsUnknown() && static &&
			receiverNode.Kind() == phpsyntax.PhpName {
			receiver = staticReceiverType(
				result,
				snapshot,
				phpquery.NameValue(receiverNode),
				receiverNode.Range().Start,
				&staticReceiverTypes,
			)
		}
		nameNode := nodes.At(1)
		// A variable member name is intentionally resolved at runtime. The
		// variable itself is linked by the binder, but there is no literal
		// method/property symbol that can be diagnosed or navigated here.
		if nameNode.Kind() == phpsyntax.PhpVariable ||
			(!static &&
				strings.HasPrefix(strings.TrimSpace(nameNode.Text()), "$")) {
			return true
		}
		name := phpquery.NameValue(nameNode)
		if name == "" || (static && strings.EqualFold(name, "class")) {
			return true
		}

		dynamicClassProperty := node.Kind() == phpsyntax.PhpScopedCall &&
			receiverNode.Kind() == phpsyntax.PhpObjectCreation &&
			strings.HasPrefix(name, "$")
		targetKind := semantic.PropertySymbol
		switch node.Kind() {
		case phpsyntax.PhpMemberCall, phpsyntax.PhpScopedCall:
			if !dynamicClassProperty {
				targetKind = semantic.MethodSymbol
			}
		default:
			if static && !strings.HasPrefix(name, "$") {
				targetKind = semantic.ClassConstantSymbol
			}
		}
		reference := semantic.Reference{
			Name:       name,
			Kind:       semantic.MemberName,
			Receiver:   receiver,
			TargetKind: targetKind,
			Static:     static,
			Write:      memberIsWrite(node),
			Range:      nameNode.RangeTrimmedTrivia(),
		}
		visitCandidate := func(id semantic.SymbolID) bool {
			reference.AddCandidate(id)
			return true
		}
		switch {
		case dynamicClassProperty:
			memberResolver.VisitPropertyIDs(receiver, name, visitCandidate)
		case node.Kind() == phpsyntax.PhpMemberCall ||
			node.Kind() == phpsyntax.PhpScopedCall:
			memberResolver.VisitMethodIDs(receiver, name, visitCandidate)
		default:
			if static && !strings.HasPrefix(name, "$") {
				memberResolver.VisitConstantIDs(
					receiver,
					name,
					visitCandidate,
				)
			} else {
				memberResolver.VisitPropertyIDs(
					receiver,
					name,
					visitCandidate,
				)
			}
		}
		if scope, ok := result.ScopeAt(nameNode.Range().Start); ok {
			reference.Scope = scope.ID
		}
		result.References = append(result.References, reference)
		return true
	},
		phpsyntax.PhpMemberCall,
		phpsyntax.PhpScopedCall,
		phpsyntax.PhpMemberAccess,
		phpsyntax.PhpScopedAccess,
	)
	return result
}

func declaredVariableType(
	document *semantic.Document,
	snapshot *semantic.Snapshot,
	node *phpsyntax.Node,
) types.Type {
	if document == nil || snapshot == nil || node == nil {
		return types.Unknown()
	}
	name := phpquery.VariableKey(node)
	scope, ok := document.ScopeAt(node.Range().Start)
	if !ok {
		return types.Unknown()
	}
	for {
		for id := range scope.SymbolIDs(document.Symbols, name) {
			symbol, found := snapshot.Symbol(id)
			if !found || symbol.Type.IsUnknown() {
				continue
			}
			return symbol.Type
		}
		if scope.ID == scope.Parent ||
			int(scope.Parent) >= len(document.Scopes) {
			return types.Unknown()
		}
		scope = document.Scopes[scope.Parent]
	}
}

func memberIsWrite(node *phpsyntax.Node) bool {
	if node == nil || node.Parent() == nil {
		return false
	}
	parent := node.Parent()
	switch parent.Kind() {
	case phpsyntax.PhpAssignmentExpression:
		left := firstDirectNode(parent)
		return left != nil && left.Range() == node.Range()
	case phpsyntax.PhpUnaryExpression:
		operator := directOperator(parent)
		return operator == "++" || operator == "--"
	default:
		return false
	}
}

func nameContext(document *semantic.Document, offset uint32) resolver.NameContext {
	context := resolver.NewNameContext(document.Namespace)
	if scope, ok := document.ScopeAt(offset); ok {
		context.Namespace = scope.Namespace
		context.Imports = scope.Imports
	}
	return context
}

func staticReceiverType(
	document *semantic.Document,
	snapshot *semantic.Snapshot,
	name string,
	offset uint32,
	namedTypes *namedTypeCache,
) types.Type {
	switch strings.ToLower(name) {
	case "self", "static":
		if class, ok := enclosingClassAt(document, snapshot, offset); ok {
			return namedTypes.typeFor(class.FullyQualified)
		}
	case "parent":
		if class, ok := enclosingClassAt(document, snapshot, offset); ok &&
			len(class.Extends()) > 0 {
			return namedTypes.typeFor(class.Extends()[0])
		}
	default:
		return namedTypes.typeFor(
			nameContext(document, offset).ResolveClass(name),
		)
	}
	return types.Unknown()
}

func enclosingClassAt(
	document *semantic.Document,
	snapshot *semantic.Snapshot,
	offset uint32,
) (semantic.Symbol, bool) {
	scope, ok := document.ScopeAt(offset)
	if !ok {
		return semantic.Symbol{}, false
	}
	for {
		if scope.Owner != "" {
			symbol, exists := snapshot.Symbol(scope.Owner)
			if exists {
				if symbol.IsClassLike() {
					return symbol, true
				}
				if symbol.Container != "" {
					if container, found := snapshot.Symbol(symbol.Container); found &&
						container.IsClassLike() {
						return container, true
					}
				}
			}
		}
		if scope.ID == scope.Parent || int(scope.Parent) >= len(document.Scopes) {
			break
		}
		scope = document.Scopes[scope.Parent]
	}
	return semantic.Symbol{}, false
}
