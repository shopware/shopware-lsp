package completion

import (
	"context"

	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	"github.com/shopware/shopware-lsp/internal/messenger"
	"github.com/shopware/shopware-lsp/internal/php"
)

type MessengerCompletionProvider struct {
	phpIndex *php.PHPIndex
	index    *messenger.Index
}

func NewMessengerCompletionProvider(
	phpIndex *php.PHPIndex,
	indexes ...*messenger.Index,
) *MessengerCompletionProvider {
	var index *messenger.Index
	if len(indexes) != 0 {
		index = indexes[0]
	}
	return &MessengerCompletionProvider{
		phpIndex: phpIndex,
		index:    index,
	}
}

func (p *MessengerCompletionProvider) GetCompletions(
	ctx context.Context,
	request *lsp.CompletionRequest,
) []protocol.CompletionItem {
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
	if !found {
		return nil
	}
	if reference.Role == messenger.ReferenceMessage {
		if p.index == nil {
			return nil
		}
		names, err := p.index.MessageNames()
		if err != nil {
			return nil
		}
		result := make([]protocol.CompletionItem, 0, len(names))
		for _, name := range names {
			result = append(result, protocol.CompletionItem{
				Label:  name,
				Kind:   int(protocol.ClassCompletion),
				Detail: "Symfony Messenger message",
			})
		}
		return result
	}
	if reference.Role != messenger.ReferenceHandlerMethod {
		return nil
	}
	methods := messenger.PublicHandlerMethods(
		p.phpIndex,
		reference.Class,
	)
	result := make([]protocol.CompletionItem, 0, len(methods))
	for _, method := range methods {
		detail := method.DocSummary
		if detail == "" {
			detail = reference.Class
		}
		result = append(result, protocol.CompletionItem{
			Label:  method.Name,
			Kind:   int(protocol.MethodCompletion),
			Detail: detail,
		})
	}
	return result
}

func (p *MessengerCompletionProvider) GetTriggerCharacters() []string {
	return []string{"'", "\""}
}
