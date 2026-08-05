package completion

import (
	"context"
	"fmt"

	"github.com/shopware/shopware-lsp/internal/event"
	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	"github.com/shopware/shopware-lsp/internal/php"
	"github.com/shopware/shopware-lsp/internal/symfony"
	"github.com/shopware/shopware-lsp/internal/uriutil"
)

type EventCompletionProvider struct {
	index    *event.Index
	phpIndex *php.PHPIndex
	services *symfony.ServiceIndex
}

func NewEventCompletionProvider(
	index *event.Index,
	phpIndex *php.PHPIndex,
	services *symfony.ServiceIndex,
) *EventCompletionProvider {
	return &EventCompletionProvider{
		index:    index,
		phpIndex: phpIndex,
		services: services,
	}
}

func (p *EventCompletionProvider) GetCompletions(
	ctx context.Context,
	request *lsp.CompletionRequest,
) []protocol.CompletionItem {
	if p == nil || p.index == nil || request == nil ||
		request.Node == nil {
		return nil
	}
	reference, ok := event.ReferenceAt(
		ctx,
		request.TextDocument.URI,
		request.Root,
		request.Node,
	)
	if !ok {
		return nil
	}
	if reference.Role == event.ReferenceEvent {
		events, err := p.index.GetEvents()
		if err != nil {
			return nil
		}
		items := make([]protocol.CompletionItem, 0, len(events))
		for _, current := range events {
			detail := current.EventType
			if detail == "" {
				detail = fmt.Sprintf(
					"%d listener(s)",
					len(current.Listeners()),
				)
			}
			items = append(items, protocol.CompletionItem{
				Label:  current.Name,
				Kind:   int(protocol.EventCompletion),
				Detail: detail,
			})
		}
		return items
	}
	if reference.Role != event.ReferenceListenerMethod ||
		p.phpIndex == nil {
		return nil
	}
	path, _ := uriutil.Path(request.TextDocument.URI)
	listener, found, err := event.ResolveListener(
		p.index,
		path,
		reference.Node.Range().Start,
		reference,
	)
	if err != nil || !found {
		return nil
	}
	className := p.listenerClass(listener)
	methods := event.PublicMethods(p.phpIndex, className)
	items := make([]protocol.CompletionItem, 0, len(methods))
	for _, method := range methods {
		detail := method.DocSummary
		if detail == "" {
			detail = className
		}
		items = append(items, protocol.CompletionItem{
			Label:  method.Name,
			Kind:   int(protocol.MethodCompletion),
			Detail: detail,
		})
	}
	return items
}

func (p *EventCompletionProvider) listenerClass(
	listener event.Occurrence,
) string {
	if listener.Class != "" || listener.Service == "" ||
		p.services == nil {
		return listener.Class
	}
	service, found, err := p.services.GetServiceByID(listener.Service)
	if err != nil || !found {
		return ""
	}
	return service.Class
}

func (p *EventCompletionProvider) GetTriggerCharacters() []string {
	return []string{"'", "\""}
}
