package inference

import (
	"strings"

	phpquery "github.com/shopware/shopware-lsp/internal/parser/php/query"
	"github.com/shopware/shopware-lsp/internal/php/resolver"
	"github.com/shopware/shopware-lsp/internal/php/semantic"
	"github.com/shopware/shopware-lsp/internal/php/types"
)

// AttributeContracts evaluates call-site-sensitive JetBrains attributes that
// cannot be represented by a callable's ordinary declared return type.
var AttributeContracts Extension = ExtensionFunc(func(
	context CallContext,
) (semantic.TypeFact, bool) {
	if context.Snapshot == nil {
		return semantic.TypeFact{}, false
	}
	matched := false
	visit := func(symbol semantic.SymbolView) bool {
		attribute, ok := semantic.AttributeNamed(symbol.Attributes(), "NoReturn")
		if ok && noReturnAttributeMatches(context, attribute) {
			matched = true
		}
		return !matched
	}
	if context.Receiver.IsUnknown() {
		visitAttributeFunctionCandidates(context, visit)
	} else {
		(resolver.MemberResolver{
			Snapshot: context.Snapshot,
		}).VisitMethodIDs(
			context.Receiver,
			context.Name,
			func(id semantic.SymbolID) bool {
				member, found := context.Snapshot.SymbolView(id)
				return !found || visit(member)
			},
		)
	}
	if !matched {
		return semantic.TypeFact{}, false
	}
	return semantic.TypeFact{
		Type:       types.Never(),
		Confidence: semantic.DeclaredConfidence,
		Source:     semantic.SignatureSource,
		Reason:     "JetBrains NoReturn attribute",
	}, true
})

func visitAttributeFunctionCandidates(
	context CallContext,
	visit func(semantic.SymbolView) bool,
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
		return context.Snapshot.VisitFunctionViews(
			candidate,
			visit,
		)
	})
}

func noReturnAttributeMatches(
	context CallContext,
	attribute *semantic.Attribute,
) bool {
	if attribute == nil {
		return false
	}
	if len(attribute.Arguments) == 0 {
		return true
	}
	if len(context.Arguments) < len(attribute.Arguments) {
		return false
	}
	for index := range attribute.Arguments {
		expected := attribute.Arguments[index].Value
		if noReturnAnyArgument(expected) {
			continue
		}
		actual := context.Arguments[index].Type
		expression := ""
		if context.Node != nil {
			if argument := phpquery.ArgumentExpression(context.Node, index); argument != nil {
				expression = normalizeContractExpression(argument.Text())
			}
		}
		if !attributeValueMatches(expected, actual, expression) {
			return false
		}
	}
	return true
}

func noReturnAnyArgument(value semantic.AttributeValue) bool {
	if value.Kind != semantic.AttributeValueClassConstant {
		return false
	}
	identity := strings.ToLower(strings.TrimPrefix(value.Value, "\\"))
	return strings.HasSuffix(identity, "\\noreturn::any_argument") ||
		identity == "noreturn::any_argument"
}

func attributeValueMatches(
	expected semantic.AttributeValue,
	actual types.Type,
	expression string,
) bool {
	switch expected.Kind {
	case semantic.AttributeValueString:
		return actual.Kind() == types.LiteralStringKind &&
			actual.Name() == expected.Value
	case semantic.AttributeValueInteger, semantic.AttributeValueFloat:
		return actual.String() == normalizeContractExpression(expected.Expression)
	case semantic.AttributeValueBool:
		return expected.Value == "true" && actual.Kind() == types.TrueKind ||
			expected.Value == "false" && actual.Kind() == types.FalseKind
	case semantic.AttributeValueNull:
		return actual.Kind() == types.NullKind
	default:
		return strings.EqualFold(
			normalizeContractExpression(expected.Expression),
			expression,
		)
	}
}
