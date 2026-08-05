package completion

import (
	"context"

	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	"github.com/shopware/shopware-lsp/internal/validation"
)

type ValidationCompletionProvider struct{}

func NewValidationCompletionProvider() *ValidationCompletionProvider {
	return &ValidationCompletionProvider{}
}

func (p *ValidationCompletionProvider) GetCompletions(
	ctx context.Context,
	request *lsp.CompletionRequest,
) []protocol.CompletionItem {
	if request == nil || request.Node == nil {
		return nil
	}
	reference, found := validation.OptionReferenceAt(
		ctx,
		request.Root,
		request.Node,
	)
	if !found {
		return nil
	}
	properties := validation.ConstraintPropertiesInContext(
		ctx,
		reference.Constraint,
	)
	items := make([]protocol.CompletionItem, 0, len(properties))
	for _, property := range properties {
		items = append(items, protocol.CompletionItem{
			Label:  property.Name,
			Kind:   int(protocol.PropertyCompletion),
			Detail: property.Type.String(),
		})
	}
	return items
}

func (p *ValidationCompletionProvider) GetTriggerCharacters() []string {
	return []string{"'", "\""}
}
