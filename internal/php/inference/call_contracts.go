package inference

import (
	"strings"

	phpquery "github.com/shopware/shopware-lsp/internal/parser/php/query"
	"github.com/shopware/shopware-lsp/internal/php/resolver"
	"github.com/shopware/shopware-lsp/internal/php/semantic"
	"github.com/shopware/shopware-lsp/internal/php/types"
)

// CallContracts evaluates dynamic return metadata published from
// .phpstorm.meta.php files. Hand-written framework and builtin extensions can
// run before this generic provider when they model a callable more precisely.
var CallContracts Extension = ExtensionFunc(func(
	context CallContext,
) (semantic.TypeFact, bool) {
	if context.Snapshot == nil {
		return semantic.TypeFact{}, false
	}
	var inferred []types.Type
	var returnContracts []semantic.CallReturnContract
	visit := func(contract semantic.CallContract) bool {
		if contract.ExitPoint && exitPointMatches(context, contract) {
			inferred = append(inferred, types.Never())
		}
		if contract.Return.Kind != semantic.CallReturnUnknown {
			returnContracts = append(returnContracts, contract.Return)
		}
		return true
	}
	if context.Receiver.IsUnknown() {
		visitFunctionContractCandidates(context, visit)
	} else {
		context.Snapshot.VisitMethodCallContracts(
			context.Receiver,
			context.Name,
			visit,
		)
	}
	if contracted, ok := resolveContractedCallSignatures(
		context,
		returnContracts,
	); ok {
		inferred = append(inferred, contracted)
	}
	if len(inferred) == 0 {
		return semantic.TypeFact{}, false
	}
	return semantic.TypeFact{
		Type:       joinTypes(context.Snapshot.Relations(), inferred),
		Confidence: semantic.DeclaredConfidence,
		Source:     semantic.SignatureSource,
		Reason:     ".phpstorm.meta.php call contract",
	}, true
})

func resolveContractedCallSignatures(
	context CallContext,
	contracts []semantic.CallReturnContract,
) (types.Type, bool) {
	if len(contracts) == 0 {
		return types.Type{}, false
	}
	foundSymbol := false
	var effective []types.Type
	visitSymbol := func(symbol semantic.Symbol) bool {
		foundSymbol = true
		resolved := resolver.ResolveSignatureWithContracts(
			context.Snapshot.Relations(),
			symbol,
			context.Arguments,
			contracts,
		)
		if resolved.Compatible && resolved.ContractApplied {
			effective = append(effective, resolved.ReturnType)
		}
		return true
	}
	if context.Receiver.IsUnknown() {
		nameContext := resolver.NewNameContext("")
		if context.Document != nil {
			nameContext = resolver.NewNameContext(context.Document.Namespace)
			nameContext.PHPDocAliases = context.Document.TypeAliases
			if context.Node != nil {
				if scope, ok := context.Document.ScopeAt(
					context.Node.Range().Start,
				); ok {
					nameContext.Namespace = scope.Namespace
					nameContext.Imports = scope.Imports
				}
			}
		}
		nameContext.VisitFunctionNames(context.Name, func(candidate string) bool {
			context.Snapshot.VisitFunctionViews(
				candidate,
				func(view semantic.SymbolView) bool {
					return visitSymbol(view.Materialize())
				},
			)
			return true
		})
	} else {
		(resolver.MemberResolver{
			Snapshot: context.Snapshot,
		}).VisitMethods(
			context.Receiver,
			context.Name,
			func(member resolver.ResolvedMember) bool {
				return visitSymbol(member.Symbol)
			},
		)
	}
	if !foundSymbol {
		return resolver.EffectiveCallReturnType(
			context.Snapshot.Relations(),
			context.Arguments,
			contracts,
		)
	}
	if len(effective) == 0 {
		return types.Type{}, false
	}
	return joinTypes(context.Snapshot.Relations(), effective), true
}

func exitPointMatches(context CallContext, contract semantic.CallContract) bool {
	for _, condition := range contract.ExitArguments {
		index := int(condition.Argument)
		if index < 0 || index >= len(context.Arguments) {
			return false
		}
		argument := context.Arguments[index].Type
		var expression string
		if context.Node != nil {
			if argumentNode := phpquery.ArgumentExpression(
				context.Node,
				index,
			); argumentNode != nil {
				expression = normalizeContractExpression(argumentNode.Text())
			}
		}
		matched := false
		for _, expected := range condition.Values {
			if callValueMatches(expected, argument, expression) {
				matched = true
				break
			}
		}
		if !matched {
			return false
		}
	}
	return true
}

func visitFunctionContractCandidates(
	context CallContext,
	visit func(semantic.CallContract) bool,
) {
	nameContext := resolver.NewNameContext("")
	if context.Document != nil {
		nameContext = resolver.NewNameContext(context.Document.Namespace)
		nameContext.PHPDocAliases = context.Document.TypeAliases
		if context.Node != nil {
			if scope, ok := context.Document.ScopeAt(
				context.Node.Range().Start,
			); ok {
				nameContext.Namespace = scope.Namespace
				nameContext.Imports = scope.Imports
			}
		}
	}
	nameContext.VisitFunctionNames(context.Name, func(candidate string) bool {
		return context.Snapshot.VisitFunctionCallContracts(candidate, visit)
	})
}

func evaluateCallReturnContract(
	context CallContext,
	contract semantic.CallReturnContract,
) (types.Type, bool) {
	arguments := context.Arguments
	if context.Node != nil {
		arguments = append([]CallArgument(nil), arguments...)
		index := int(contract.Argument)
		if index >= 0 && index < len(arguments) && arguments[index].Expression == "" {
			if argumentNode := phpquery.ArgumentExpression(context.Node, index); argumentNode != nil {
				arguments[index].Expression = argumentNode.Text()
			}
		}
	}
	return resolver.EffectiveCallReturnType(
		context.Snapshot.Relations(),
		arguments,
		[]semantic.CallReturnContract{contract},
	)
}

func callValueMatches(
	value semantic.CallValue,
	argument types.Type,
	expression string,
) bool {
	switch value.Kind {
	case semantic.CallValueString:
		return argument.Kind() == types.LiteralStringKind &&
			argument.Name() == value.Value
	case semantic.CallValueNumber:
		return argument.String() == normalizeContractExpression(value.Expression)
	default:
		return strings.EqualFold(
			normalizeContractExpression(value.Expression),
			expression,
		)
	}
}

func normalizeContractExpression(value string) string {
	return strings.Join(strings.Fields(strings.TrimSpace(value)), "")
}
