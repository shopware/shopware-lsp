package reference

import (
	"context"
	"path/filepath"
	"strings"

	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	"github.com/shopware/shopware-lsp/internal/twigcomponent"
	"github.com/shopware/shopware-lsp/internal/uriutil"
)

type LiveComponentEventReferenceProvider struct {
	index *twigcomponent.Index
}

func NewLiveComponentEventReferenceProvider(
	index *twigcomponent.Index,
) *LiveComponentEventReferenceProvider {
	return &LiveComponentEventReferenceProvider{index: index}
}

func (p *LiveComponentEventReferenceProvider) GetReferences(
	_ context.Context,
	request *lsp.ReferenceRequest,
) ([]protocol.Location, error) {
	if p == nil || p.index == nil || request == nil ||
		request.Root == nil || request.LineIndex == nil {
		return nil, nil
	}
	offset := request.LineIndex.OffsetUTF16(
		uint32(request.Position.Line),
		uint32(request.Position.Character),
	)
	path, _ := uriutil.Path(request.TextDocument.URI)
	extension := strings.ToLower(filepath.Ext(path))
	event := ""
	switch extension {
	case ".php":
		if reference, found := twigcomponent.LiveEventReferenceAtPHP(
			path,
			request.Root,
			offset,
		); found {
			event = reference.Name
		} else if listener, found := twigcomponent.LiveListenerAtPHP(
			path,
			request.Root,
			offset,
		); found {
			event = listener.Name
		}
	case ".twig":
		if reference, found := twigcomponent.LiveEventReferenceAtTwig(
			path,
			request.Root,
			offset,
		); found {
			event = reference.Name
		}
	default:
		return nil, nil
	}
	if event == "" {
		return nil, nil
	}

	references, err := p.index.LiveEventReferences(event)
	if err != nil {
		return nil, err
	}
	var result []protocol.Location
	for _, reference := range references {
		if reference.File == path {
			continue
		}
		if location, ok := componentReferenceLocation(
			reference.File,
			reference.Range,
		); ok {
			result = append(result, location)
		}
	}
	for _, reference := range currentLiveEventReferences(
		path,
		extension,
		request,
	) {
		if !strings.EqualFold(reference.Name, event) {
			continue
		}
		result = append(result, protocol.Location{
			URI: request.TextDocument.URI,
			Range: twigComponentReferenceRange(
				reference.Range,
				request.LineIndex,
			),
		})
	}
	if request.Context.IncludeDeclaration {
		listeners, listenerErr := p.index.LiveListeners()
		if listenerErr != nil {
			return nil, listenerErr
		}
		if extension == ".php" {
			listeners = mergeCurrentLiveListeners(
				listeners,
				twigcomponent.LiveListenersInPHP(
					path,
					request.Root,
				),
			)
		}
		for _, listener := range listeners {
			if !strings.EqualFold(listener.Name, event) {
				continue
			}
			if listener.File == path {
				result = append(result, protocol.Location{
					URI: request.TextDocument.URI,
					Range: twigComponentReferenceRange(
						listener.Range,
						request.LineIndex,
					),
				})
				continue
			}
			if location, ok := componentReferenceLocation(
				listener.File,
				listener.Range,
			); ok {
				result = append(result, location)
			}
		}
	}
	return uniqueComponentReferenceLocations(result), nil
}

func currentLiveEventReferences(
	path,
	extension string,
	request *lsp.ReferenceRequest,
) []twigcomponent.LiveEventReference {
	if extension == ".php" {
		return twigcomponent.LiveEventReferencesInPHP(path, request.Root)
	}
	return twigcomponent.LiveEventReferencesInTwig(path, request.Root)
}

func mergeCurrentLiveListeners(
	indexed,
	current []twigcomponent.LiveListener,
) []twigcomponent.LiveListener {
	result := make(
		[]twigcomponent.LiveListener,
		0,
		len(indexed)+len(current),
	)
	for _, listener := range indexed {
		if listener.File != "" &&
			len(current) != 0 &&
			listener.File == current[0].File {
			continue
		}
		result = append(result, listener)
	}
	return append(result, current...)
}
