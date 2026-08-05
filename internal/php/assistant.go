package php

import (
	"context"
	"strings"

	"github.com/shopware/shopware-lsp/internal/parser/cst"
	phpquery "github.com/shopware/shopware-lsp/internal/parser/php/query"
	phpsyntax "github.com/shopware/shopware-lsp/internal/parser/php/syntax"
	"github.com/shopware/shopware-lsp/internal/php/resolver"
	"github.com/shopware/shopware-lsp/internal/php/semantic"
	"github.com/shopware/shopware-lsp/internal/php/types"
)

// AssistantArgumentReference recognizes a direct string argument whose
// resolved function, constructor, or method parameter carries the requested
// PHPDoc assistant tag, such as #Route or #Service. Parameter tags declared on
// inherited interfaces, parents, and traits are included.
func AssistantArgumentReference(
	ctx context.Context,
	node *phpsyntax.Node,
	tag string,
) (cst.TextRange, bool) {
	reference, tags := AssistantArgumentTags(ctx, node, tag)
	return reference, len(tags) != 0
}

// AssistantArgumentTags recognizes a direct string call argument and returns
// the requested assistant tags carried by its resolved parameter. Resolution
// includes function, constructor, method, named/variadic argument, and
// inherited parameter contracts.
func AssistantArgumentTags(
	ctx context.Context,
	node *phpsyntax.Node,
	tags ...string,
) (cst.TextRange, []string) {
	phpContext := GetPHPContext(ctx)
	if phpContext == nil || phpContext.Document == nil ||
		phpContext.Snapshot == nil || node == nil || len(tags) == 0 {
		return cst.TextRange{}, nil
	}
	literal := phpquery.StringAt(node)
	if literal == nil {
		return cst.TextRange{}, nil
	}
	var (
		container  *phpsyntax.Node
		candidates []semantic.Symbol
	)
	if call := phpquery.CallAt(literal); call != nil {
		container = call
		candidates = assistantCallCandidates(phpContext, call)
	} else if object := assistantObjectCreationAt(literal); object != nil {
		container = object
		name := assistantNameContext(
			phpContext.Document,
			object.Range().Start,
		).ResolveClass(phpquery.ObjectClassName(object))
		for _, member := range (resolver.MemberResolver{
			Snapshot: phpContext.Snapshot,
		}).Methods(types.Named(name), "__construct") {
			candidates = append(candidates, member.Symbol)
		}
	}
	if container == nil || len(candidates) == 0 {
		return cst.TextRange{}, nil
	}
	argumentIndex := phpquery.ArgumentIndex(container, literal)
	if argumentIndex < 0 ||
		phpquery.ArgumentExpression(container, argumentIndex) != literal {
		return cst.TextRange{}, nil
	}
	argumentName := phpquery.ArgumentName(literal)
	seen := make(map[string]struct{}, len(tags))
	var matched []string
	for _, candidate := range candidates {
		parameterIndex := assistantParameterIndex(
			candidate.Parameters,
			argumentIndex,
			argumentName,
		)
		if parameterIndex < 0 {
			continue
		}
		for _, tag := range tags {
			key := strings.ToLower(strings.TrimSpace(tag))
			if key == "" {
				continue
			}
			if _, duplicate := seen[key]; duplicate {
				continue
			}
			if assistantParameterHasTag(
				phpContext.Snapshot,
				candidate,
				parameterIndex,
				tag,
				make(map[semantic.SymbolID]struct{}),
			) {
				seen[key] = struct{}{}
				matched = append(matched, tag)
			}
		}
	}
	if len(matched) == 0 {
		return cst.TextRange{}, nil
	}
	return phpquery.StringContentRange(literal), matched
}

// AssistantSiblingStringArgument returns the direct string value passed to a
// parameter carrying tag on the same resolved call. It is used by contracts
// such as #TranslationKey, whose key domain is supplied by a sibling
// #TranslationDomain parameter.
func AssistantSiblingStringArgument(
	ctx context.Context,
	node *phpsyntax.Node,
	tag string,
) (string, bool) {
	phpContext := GetPHPContext(ctx)
	if phpContext == nil || phpContext.Document == nil ||
		phpContext.Snapshot == nil || node == nil || tag == "" {
		return "", false
	}
	literal := phpquery.StringAt(node)
	if literal == nil {
		return "", false
	}
	var (
		container  *phpsyntax.Node
		candidates []semantic.Symbol
	)
	if call := phpquery.CallAt(literal); call != nil {
		container = call
		candidates = assistantCallCandidates(phpContext, call)
	} else if object := assistantObjectCreationAt(literal); object != nil {
		container = object
		name := assistantNameContext(
			phpContext.Document,
			object.Range().Start,
		).ResolveClass(phpquery.ObjectClassName(object))
		for _, member := range (resolver.MemberResolver{
			Snapshot: phpContext.Snapshot,
		}).Methods(types.Named(name), "__construct") {
			candidates = append(candidates, member.Symbol)
		}
	}
	for _, candidate := range candidates {
		for argumentIndex, argument := range phpquery.Arguments(container) {
			parameterIndex := assistantParameterIndex(
				candidate.Parameters,
				argumentIndex,
				phpquery.ArgumentName(argument),
			)
			if parameterIndex < 0 ||
				!assistantParameterHasTag(
					phpContext.Snapshot,
					candidate,
					parameterIndex,
					tag,
					make(map[semantic.SymbolID]struct{}),
				) {
				continue
			}
			expression := phpquery.ArgumentExpression(
				container,
				argumentIndex,
			)
			value := phpquery.StringAt(expression)
			if expression == nil || value == nil || value != expression {
				continue
			}
			return phpquery.StringValue(value), true
		}
	}
	return "", false
}

func assistantCallCandidates(
	phpContext *PHPContext,
	call *phpsyntax.Node,
) []semantic.Symbol {
	if phpContext == nil || call == nil {
		return nil
	}
	switch call.Kind() {
	case phpsyntax.PhpMemberCall, phpsyntax.PhpScopedCall:
		receiverNode := phpquery.CallReceiver(call)
		receiver := phpContext.Document.TypeOf(receiverNode).Type
		if receiver.IsUnknown() &&
			call.Kind() == phpsyntax.PhpScopedCall &&
			receiverNode != nil &&
			receiverNode.Kind() == phpsyntax.PhpName {
			name := assistantNameContext(
				phpContext.Document,
				receiverNode.Range().Start,
			).ResolveClass(strings.TrimSpace(receiverNode.Text()))
			receiver = types.Named(name)
		}
		members := (resolver.MemberResolver{
			Snapshot: phpContext.Snapshot,
		}).Methods(receiver, phpquery.CallMethodName(call))
		result := make([]semantic.Symbol, 0, len(members))
		for _, member := range members {
			result = append(result, member.Symbol)
		}
		return result
	case phpsyntax.PhpFunctionCall:
		nameContext := assistantNameContext(
			phpContext.Document,
			call.Range().Start,
		)
		var result []semantic.Symbol
		nameContext.VisitFunctionNames(
			phpquery.CallName(call),
			func(name string) bool {
				if functions := phpContext.Snapshot.Functions(name); len(functions) != 0 {
					result = functions
					return false
				}
				return true
			},
		)
		return result
	}
	return nil
}

func assistantObjectCreationAt(node *phpsyntax.Node) *phpsyntax.Node {
	for current := node; current != nil; current = current.Parent() {
		if current.Kind() == phpsyntax.PhpObjectCreation {
			return current
		}
	}
	return nil
}

func assistantNameContext(
	document *semantic.Document,
	offset uint32,
) resolver.NameContext {
	if document != nil {
		if scope, found := document.ScopeAt(offset); found {
			return resolver.NameContext{
				Namespace: scope.Namespace,
				Imports:   scope.Imports,
			}
		}
		return resolver.NewNameContext(document.Namespace)
	}
	return resolver.NewNameContext("")
}

func assistantParameterIndex(
	parameters []semantic.Parameter,
	argumentIndex int,
	argumentName string,
) int {
	if argumentName != "" {
		argumentName = strings.TrimPrefix(argumentName, "$")
		for index, parameter := range parameters {
			if strings.EqualFold(
				strings.TrimPrefix(parameter.Name, "$"),
				argumentName,
			) {
				return index
			}
		}
		return -1
	}
	if argumentIndex < len(parameters) {
		return argumentIndex
	}
	if len(parameters) != 0 &&
		parameters[len(parameters)-1].Flags.Has(semantic.VariadicFlag) {
		return len(parameters) - 1
	}
	return -1
}

func assistantParameterHasTag(
	snapshot *semantic.Snapshot,
	method semantic.Symbol,
	parameterIndex int,
	tag string,
	visited map[semantic.SymbolID]struct{},
) bool {
	if _, duplicate := visited[method.ID]; duplicate {
		return false
	}
	visited[method.ID] = struct{}{}
	if parameterIndex >= 0 && parameterIndex < len(method.Parameters) {
		for _, candidate := range method.Parameters[parameterIndex].
			AssistantTags {
			if strings.EqualFold(candidate, tag) {
				return true
			}
		}
	}
	owner, found := snapshot.Symbol(method.Container)
	if !found {
		return false
	}
	parentNames := append([]string(nil), owner.Extends...)
	parentNames = append(parentNames, owner.Implements...)
	parentNames = append(parentNames, owner.Traits...)
	for _, parentName := range parentNames {
		for _, parent := range snapshot.Classes(parentName) {
			for _, inherited := range snapshot.Members(parent.ID, method.Name) {
				if inherited.Kind == semantic.MethodSymbol &&
					assistantParameterHasTag(
						snapshot,
						inherited,
						parameterIndex,
						tag,
						visited,
					) {
					return true
				}
			}
		}
	}
	return false
}
