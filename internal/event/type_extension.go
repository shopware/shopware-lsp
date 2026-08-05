package event

import (
	"strings"

	"github.com/shopware/shopware-lsp/internal/php/inference"
	"github.com/shopware/shopware-lsp/internal/php/semantic"
	"github.com/shopware/shopware-lsp/internal/php/types"
)

type PHPTypeExtension struct{}

func NewPHPTypeExtension() *PHPTypeExtension {
	return &PHPTypeExtension{}
}

func (extension *PHPTypeExtension) InferCall(
	context inference.CallContext,
) (semantic.TypeFact, bool) {
	if extension == nil ||
		!strings.EqualFold(context.Name, "dispatch") ||
		len(context.Arguments) == 0 ||
		!isDispatcherType(context.Receiver, context.Snapshot) {
		return semantic.TypeFact{}, false
	}
	result := context.Arguments[0].Type
	if result.Kind() == types.LiteralStringKind &&
		len(context.Arguments) > 1 {
		result = context.Arguments[1].Type
	}
	if result.IsUnknown() {
		return semantic.TypeFact{}, false
	}
	return semantic.TypeFact{
		Type:       result,
		Confidence: semantic.DeclaredConfidence,
		Source:     semantic.FrameworkSource,
		Reason:     "Symfony EventDispatcher returns the dispatched event",
	}, true
}
