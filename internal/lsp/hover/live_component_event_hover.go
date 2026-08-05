package hover

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	"github.com/shopware/shopware-lsp/internal/parser/cst"
	"github.com/shopware/shopware-lsp/internal/twigcomponent"
	"github.com/shopware/shopware-lsp/internal/uriutil"
)

type LiveComponentEventHoverProvider struct {
	index *twigcomponent.Index
}

func NewLiveComponentEventHoverProvider(
	index *twigcomponent.Index,
) *LiveComponentEventHoverProvider {
	return &LiveComponentEventHoverProvider{index: index}
}

func (p *LiveComponentEventHoverProvider) GetHover(
	_ context.Context,
	request *lsp.HoverRequest,
) (*protocol.Hover, error) {
	if p == nil || p.index == nil || request == nil ||
		request.Root == nil || request.LineIndex == nil {
		return nil, nil
	}
	offset := request.LineIndex.OffsetUTF16(
		uint32(request.Position.Line),
		uint32(request.Position.Character),
	)
	path, _ := uriutil.Path(request.TextDocument.URI)
	switch strings.ToLower(filepath.Ext(path)) {
	case ".php":
		if argument, found :=
			twigcomponent.LiveEventArgumentReferenceAtPHP(
				path,
				request.Root,
				offset,
			); found {
			return p.argumentHover(
				argument,
				argument.Range,
				request,
			)
		}
		if event, found := twigcomponent.LiveEventReferenceAtPHP(
			path,
			request.Root,
			offset,
		); found {
			return p.eventHover(event.Name, event.Range, request)
		}
	case ".twig":
		if argument, found :=
			twigcomponent.LiveEventArgumentReferenceAtTwig(
				path,
				request.Root,
				offset,
			); found {
			return p.argumentHover(
				argument,
				argument.Range,
				request,
			)
		}
		if event, found := twigcomponent.LiveEventReferenceAtTwig(
			path,
			request.Root,
			offset,
		); found {
			return p.eventHover(event.Name, event.Range, request)
		}
	}
	return nil, nil
}

func (p *LiveComponentEventHoverProvider) eventHover(
	event string,
	rng cst.TextRange,
	request *lsp.HoverRequest,
) (*protocol.Hover, error) {
	listeners, err := p.index.LiveListeners()
	if err != nil {
		return nil, err
	}
	var matching []twigcomponent.LiveListener
	for _, listener := range listeners {
		if strings.EqualFold(listener.Name, event) {
			matching = append(matching, listener)
		}
	}
	if len(matching) == 0 {
		return nil, nil
	}
	references, _ := p.index.LiveEventReferences(event)
	var value strings.Builder
	fmt.Fprintf(
		&value,
		"**Symfony UX Live event** `%s`\n\n",
		escapeComponentMarkdown(event),
	)
	for index, listener := range matching {
		if index != 0 {
			value.WriteString("\n")
		}
		fmt.Fprintf(
			&value,
			"```php\n%s\n```\n",
			liveEventListenerSignature(listener),
		)
	}
	fmt.Fprintf(
		&value,
		"\n%d listener(s) • %d indexed emission(s)",
		len(matching),
		len(references),
	)
	return &protocol.Hover{
		Contents: protocol.MarkupContent{
			Kind:  protocol.Markdown,
			Value: value.String(),
		},
		Range: securityProtocolRange(rng, request.LineIndex),
	}, nil
}

func (p *LiveComponentEventHoverProvider) argumentHover(
	reference twigcomponent.LiveEventArgumentReference,
	rng cst.TextRange,
	request *lsp.HoverRequest,
) (*protocol.Hover, error) {
	listeners, err := p.index.LiveListeners()
	if err != nil {
		return nil, err
	}
	for _, listener := range listeners {
		if !strings.EqualFold(listener.Name, reference.Event) {
			continue
		}
		for _, parameter := range listener.Parameters {
			if !parameter.LiveArg ||
				!strings.EqualFold(parameter.Name, reference.Name) {
				continue
			}
			value := fmt.Sprintf(
				"**Live event argument** `%s`\n\nEvent: `%s`\n\nListener: `%s::%s()`",
				escapeComponentMarkdown(parameter.Name),
				escapeComponentMarkdown(reference.Event),
				escapeComponentMarkdown(listener.Class),
				escapeComponentMarkdown(listener.Method),
			)
			if parameter.Type != "" {
				value += fmt.Sprintf(
					"\n\nPHP type: `%s`",
					escapeComponentMarkdown(parameter.Type),
				)
			}
			if parameter.Name != parameter.PHPName {
				value += fmt.Sprintf(
					"\n\nMapped to PHP parameter `$%s` via `#[LiveArg]`",
					escapeComponentMarkdown(parameter.PHPName),
				)
			}
			return &protocol.Hover{
				Contents: protocol.MarkupContent{
					Kind:  protocol.Markdown,
					Value: value,
				},
				Range: securityProtocolRange(rng, request.LineIndex),
			}, nil
		}
	}
	return nil, nil
}

func liveEventListenerSignature(
	listener twigcomponent.LiveListener,
) string {
	var parameters []string
	for _, parameter := range listener.Parameters {
		name := "$" + parameter.PHPName
		if parameter.Type != "" {
			name = parameter.Type + " " + name
		}
		if parameter.Optional {
			name += " = …"
		}
		parameters = append(parameters, name)
	}
	return listener.Class + "::" + listener.Method +
		"(" + strings.Join(parameters, ", ") + ")"
}
