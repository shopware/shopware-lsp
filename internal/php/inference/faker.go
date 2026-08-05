package inference

import (
	"strings"

	"github.com/shopware/shopware-lsp/internal/php/semantic"
	"github.com/shopware/shopware-lsp/internal/php/types"
)

const fakerGeneratorClass = "Faker\\Generator"

// FakerTypes models return types that Faker exposes through Generator's
// PHPDoc-only magic methods. The upstream randomElement declaration returns
// mixed even though its implementation preserves the input element type.
var FakerTypes Extension = ExtensionFunc(func(
	context CallContext,
) (semantic.TypeFact, bool) {
	if context.Static ||
		!strings.EqualFold(context.Name, "randomElement") ||
		!isFakerGenerator(context) {
		return semantic.TypeFact{}, false
	}

	var element types.Type
	switch len(context.Arguments) {
	case 0:
		// Matches Faker's default ['a', 'b', 'c'] argument.
		element = types.String()
	case 1:
		relations := types.Relations{}
		if context.Snapshot != nil {
			relations = context.Snapshot.Relations()
		}
		_, element = iterableTypes(context.Arguments[0].Type, relations)
	default:
		return semantic.TypeFact{}, false
	}
	if element.IsUnknown() || element.Kind() == types.MixedKind {
		return semantic.TypeFact{}, false
	}
	return semantic.TypeFact{
		Type:       types.Nullable(element),
		Confidence: semantic.DeclaredConfidence,
		Source:     semantic.SignatureSource,
		Reason:     "Faker randomElement preserves the input element type",
	}, true
})

func isFakerGenerator(context CallContext) bool {
	target := types.Named(fakerGeneratorClass)
	if context.Receiver.Kind() == types.ObjectKind &&
		strings.EqualFold(context.Receiver.Name(), target.Name()) {
		return true
	}
	return context.Snapshot != nil &&
		context.Snapshot.Relations().IsSubtype(context.Receiver, target)
}
