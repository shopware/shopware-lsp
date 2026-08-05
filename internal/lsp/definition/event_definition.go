package definition

import (
	"context"
	"fmt"

	"github.com/shopware/shopware-lsp/internal/event"
	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	"github.com/shopware/shopware-lsp/internal/php"
	"github.com/shopware/shopware-lsp/internal/php/semantic"
	"github.com/shopware/shopware-lsp/internal/symfony"
	"github.com/shopware/shopware-lsp/internal/uriutil"
)

type EventDefinitionProvider struct {
	index    *event.Index
	phpIndex *php.PHPIndex
	services *symfony.ServiceIndex
}

func NewEventDefinitionProvider(
	index *event.Index,
	phpIndex *php.PHPIndex,
	services *symfony.ServiceIndex,
) *EventDefinitionProvider {
	return &EventDefinitionProvider{
		index:    index,
		phpIndex: phpIndex,
		services: services,
	}
}

func (p *EventDefinitionProvider) GetDefinition(
	ctx context.Context,
	request *lsp.DefinitionRequest,
) []protocol.Location {
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
		current, found, err := p.index.GetEvent(reference.Name)
		if err != nil || !found {
			return nil
		}
		var locations []protocol.Location
		if p.phpIndex != nil && current.EventType != "" {
			if symbol, exists := p.phpIndex.FindClass(
				current.EventType,
			); exists {
				locations = append(locations, phpSymbolLocation(symbol))
			}
		}
		for _, listener := range current.Listeners() {
			className := p.listenerClass(listener)
			var methods []semantic.Symbol
			if p.phpIndex != nil {
				methods = p.phpIndex.FindMethods(
					className,
					listener.Method,
				)
			}
			if len(methods) != 0 {
				for _, method := range methods {
					locations = append(
						locations,
						phpSymbolLocation(method),
					)
				}
				continue
			}
			if location, exists := consoleLocation(
				listener.File,
				listener.Range,
			); exists {
				locations = append(locations, location)
			}
		}
		return uniqueEventLocations(locations)
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
	methods := p.phpIndex.FindMethods(
		p.listenerClass(listener),
		reference.Name,
	)
	locations := make([]protocol.Location, 0, len(methods))
	for _, method := range methods {
		locations = append(locations, phpSymbolLocation(method))
	}
	return uniqueEventLocations(locations)
}

func (p *EventDefinitionProvider) listenerClass(
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

func uniqueEventLocations(
	locations []protocol.Location,
) []protocol.Location {
	seen := make(map[string]struct{}, len(locations))
	result := make([]protocol.Location, 0, len(locations))
	for _, location := range locations {
		key := fmt.Sprintf(
			"%s:%d:%d:%d:%d",
			location.URI,
			location.Range.Start.Line,
			location.Range.Start.Character,
			location.Range.End.Line,
			location.Range.End.Character,
		)
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		result = append(result, location)
	}
	return result
}
