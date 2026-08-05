package definition

import (
	"context"
	"path/filepath"
	"strings"

	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	"github.com/shopware/shopware-lsp/internal/twigcomponent"
	"github.com/shopware/shopware-lsp/internal/uriutil"
)

type LiveComponentEventDefinitionProvider struct {
	index *twigcomponent.Index
}

func NewLiveComponentEventDefinitionProvider(
	index *twigcomponent.Index,
) *LiveComponentEventDefinitionProvider {
	return &LiveComponentEventDefinitionProvider{index: index}
}

func (p *LiveComponentEventDefinitionProvider) GetDefinition(
	_ context.Context,
	request *lsp.DefinitionRequest,
) []protocol.Location {
	if p == nil || p.index == nil || request == nil ||
		request.Root == nil || request.LineIndex == nil {
		return nil
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
			return p.argumentDefinitions(argument)
		}
		if event, found := twigcomponent.LiveEventReferenceAtPHP(
			path,
			request.Root,
			offset,
		); found {
			return p.listenerDefinitions(event.Name)
		}
	case ".twig":
		if argument, found :=
			twigcomponent.LiveEventArgumentReferenceAtTwig(
				path,
				request.Root,
				offset,
			); found {
			return p.argumentDefinitions(argument)
		}
		if event, found := twigcomponent.LiveEventReferenceAtTwig(
			path,
			request.Root,
			offset,
		); found {
			return p.listenerDefinitions(event.Name)
		}
	}
	return nil
}

func (p *LiveComponentEventDefinitionProvider) listenerDefinitions(
	event string,
) []protocol.Location {
	listeners, err := p.index.LiveListeners()
	if err != nil {
		return nil
	}
	var result []protocol.Location
	for _, listener := range listeners {
		if !strings.EqualFold(listener.Name, event) {
			continue
		}
		if location, ok := componentFileLocation(
			listener.File,
			listener.Range,
		); ok {
			result = append(result, location)
		}
	}
	return uniqueComponentLocations(result)
}

func (p *LiveComponentEventDefinitionProvider) argumentDefinitions(
	reference twigcomponent.LiveEventArgumentReference,
) []protocol.Location {
	listeners, err := p.index.LiveListeners()
	if err != nil {
		return nil
	}
	var result []protocol.Location
	for _, listener := range listeners {
		if !strings.EqualFold(listener.Name, reference.Event) {
			continue
		}
		for _, parameter := range listener.Parameters {
			if !parameter.LiveArg ||
				!strings.EqualFold(parameter.Name, reference.Name) {
				continue
			}
			if location, ok := componentFileLocation(
				listener.File,
				parameter.Range,
			); ok {
				result = append(result, location)
			}
		}
	}
	return uniqueComponentLocations(result)
}
