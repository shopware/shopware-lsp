package definition

import (
	"context"

	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	"github.com/shopware/shopware-lsp/internal/messenger"
	"github.com/shopware/shopware-lsp/internal/php"
)

type MessengerDefinitionProvider struct {
	phpIndex *php.PHPIndex
	index    *messenger.Index
}

func NewMessengerDefinitionProvider(
	phpIndex *php.PHPIndex,
	indexes ...*messenger.Index,
) *MessengerDefinitionProvider {
	var index *messenger.Index
	if len(indexes) != 0 {
		index = indexes[0]
	}
	return &MessengerDefinitionProvider{
		phpIndex: phpIndex,
		index:    index,
	}
}

func (p *MessengerDefinitionProvider) GetDefinition(
	ctx context.Context,
	request *lsp.DefinitionRequest,
) []protocol.Location {
	if p == nil || p.phpIndex == nil || request == nil ||
		request.Root == nil || request.Node == nil {
		return nil
	}
	reference, found := messenger.ReferenceAt(
		ctx,
		request.TextDocument.URI,
		request.Root,
		request.Node,
	)
	if !found || reference.Name == "" {
		return nil
	}
	if reference.Role == messenger.ReferenceMessage {
		symbol, exists := p.phpIndex.FindClass(reference.Name)
		if !exists {
			return nil
		}
		return []protocol.Location{phpSymbolLocation(symbol)}
	}
	if reference.Role != messenger.ReferenceHandlerMethod {
		return nil
	}
	methods := p.phpIndex.FindMethods(reference.Class, reference.Name)
	result := make([]protocol.Location, 0, len(methods))
	for _, method := range methods {
		result = append(result, phpSymbolLocation(method))
	}
	return result
}
