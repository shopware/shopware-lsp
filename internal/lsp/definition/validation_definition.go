package definition

import (
	"context"

	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	"github.com/shopware/shopware-lsp/internal/validation"
)

type ValidationDefinitionProvider struct{}

func NewValidationDefinitionProvider() *ValidationDefinitionProvider {
	return &ValidationDefinitionProvider{}
}

func (p *ValidationDefinitionProvider) GetDefinition(
	ctx context.Context,
	request *lsp.DefinitionRequest,
) []protocol.Location {
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
	property, found := validation.FindConstraintProperty(
		validation.ConstraintPropertiesInContext(
			ctx,
			reference.Constraint,
		),
		reference.Name,
	)
	if !found {
		return nil
	}
	return []protocol.Location{phpSymbolLocation(property)}
}
