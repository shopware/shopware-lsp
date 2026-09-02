package hover

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/shopware/shopware-lsp/internal/event"
	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	"github.com/shopware/shopware-lsp/internal/php"
	"github.com/shopware/shopware-lsp/internal/symfony"
	"github.com/shopware/shopware-lsp/internal/uriutil"
)

type EventHoverProvider struct {
	root     string
	index    *event.Index
	phpIndex *php.PHPIndex
	services *symfony.ServiceIndex
}

func NewEventHoverProvider(
	root string,
	index *event.Index,
	phpIndex *php.PHPIndex,
	services *symfony.ServiceIndex,
) *EventHoverProvider {
	return &EventHoverProvider{
		root:     root,
		index:    index,
		phpIndex: phpIndex,
		services: services,
	}
}

func (p *EventHoverProvider) GetHover(
	ctx context.Context,
	request *lsp.HoverRequest,
) (*protocol.Hover, error) {
	if p == nil || p.index == nil || request == nil ||
		request.Node == nil {
		return nil, nil
	}
	reference, ok := event.ReferenceAt(
		ctx,
		request.TextDocument.URI,
		request.Root,
		request.Node,
	)
	if !ok {
		return nil, nil
	}
	var markdown strings.Builder
	if reference.Role == event.ReferenceEvent {
		current, found, err := p.index.GetEvent(reference.Name)
		if err != nil || !found {
			return nil, err
		}
		fmt.Fprintf(
			&markdown,
			"**Symfony event** `%s`\n",
			escapeEventMarkdown(current.Name),
		)
		if current.EventType != "" {
			fmt.Fprintf(
				&markdown,
				"\nType: `%s`\n",
				escapeEventMarkdown(current.EventType),
			)
		}
		listeners := current.Listeners()
		if len(listeners) != 0 {
			fmt.Fprintf(
				&markdown,
				"\n%d listener(s):\n",
				len(listeners),
			)
			for index, listener := range listeners {
				if index == 8 {
					fmt.Fprintf(
						&markdown,
						"- … and %d more\n",
						len(listeners)-index,
					)
					break
				}
				target := p.listenerClass(listener)
				if listener.Method != "" {
					target += "::" + listener.Method + "()"
				}
				fmt.Fprintf(
					&markdown,
					"- `%s`",
					escapeEventMarkdown(target),
				)
				if listener.Priority != "" {
					fmt.Fprintf(
						&markdown,
						" (priority `%s`)",
						escapeEventMarkdown(listener.Priority),
					)
				}
				displayPath, pathErr := filepath.Rel(
					p.root,
					listener.File,
				)
				if pathErr != nil {
					displayPath = listener.File
				}
				fmt.Fprintf(
					&markdown,
					" — `%s`\n",
					escapeEventMarkdown(filepath.ToSlash(displayPath)),
				)
			}
		}
		if dispatchCount := len(current.Dispatches()); dispatchCount != 0 {
			fmt.Fprintf(
				&markdown,
				"\n%d indexed dispatch site(s)",
				dispatchCount,
			)
		}
	} else if reference.Role == event.ReferenceListenerMethod {
		path, _ := uriutil.Path(request.TextDocument.URI)
		listener, found, err := event.ResolveListener(
			p.index,
			path,
			reference.Node.Range().Start,
			reference,
		)
		if err != nil || !found {
			return nil, err
		}
		className := p.listenerClass(listener)
		fmt.Fprintf(
			&markdown,
			"**Symfony event listener** `%s::%s()`",
			escapeEventMarkdown(className),
			escapeEventMarkdown(reference.Name),
		)
		if p.phpIndex != nil {
			methods := p.phpIndex.FindMethods(className, reference.Name)
			if len(methods) != 0 && methods[0].DocSummary() != "" {
				fmt.Fprintf(
					&markdown,
					"\n\n%s",
					methods[0].DocSummary(),
				)
			}
		}
		events, err := p.index.EventsForListener(
			className,
			reference.Name,
		)
		if err != nil {
			return nil, err
		}
		if len(events) != 0 {
			names := make([]string, 0, len(events))
			for _, current := range events {
				names = append(names, "`"+current.Name+"`")
			}
			fmt.Fprintf(
				&markdown,
				"\n\nListens to: %s",
				strings.Join(names, ", "),
			)
		}
	} else {
		return nil, nil
	}

	rng := reference.Node.RangeTrimmedTrivia()
	startLine, startCharacter := request.LineIndex.PositionUTF16(rng.Start)
	endLine, endCharacter := request.LineIndex.PositionUTF16(rng.End)
	return &protocol.Hover{
		Contents: protocol.MarkupContent{
			Kind:  protocol.Markdown,
			Value: markdown.String(),
		},
		Range: &protocol.Range{
			Start: protocol.Position{
				Line:      int(startLine),
				Character: int(startCharacter),
			},
			End: protocol.Position{
				Line:      int(endLine),
				Character: int(endCharacter),
			},
		},
	}, nil
}

func (p *EventHoverProvider) listenerClass(
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

func escapeEventMarkdown(value string) string {
	return strings.ReplaceAll(value, "`", "\\`")
}
