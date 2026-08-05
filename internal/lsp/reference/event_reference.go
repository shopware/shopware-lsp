package reference

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/shopware/shopware-lsp/internal/event"
	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	"github.com/shopware/shopware-lsp/internal/parser/cst"
	phpquery "github.com/shopware/shopware-lsp/internal/parser/php/query"
	"github.com/shopware/shopware-lsp/internal/php"
	"github.com/shopware/shopware-lsp/internal/uriutil"
)

type EventReferenceProvider struct {
	index *event.Index
}

func NewEventReferenceProvider(
	index *event.Index,
) *EventReferenceProvider {
	return &EventReferenceProvider{index: index}
}

func (p *EventReferenceProvider) GetReferences(
	ctx context.Context,
	request *lsp.ReferenceRequest,
) ([]protocol.Location, error) {
	if p == nil || p.index == nil || request == nil ||
		request.Node == nil {
		return nil, nil
	}
	if reference, ok := event.ReferenceAt(
		ctx,
		request.TextDocument.URI,
		request.Root,
		request.Node,
	); ok {
		switch reference.Role {
		case event.ReferenceEvent:
			current, found, err := p.index.GetEvent(reference.Name)
			if err != nil || !found {
				return nil, err
			}
			return eventOccurrenceLocations(current.Occurrences, false), nil
		case event.ReferenceListenerMethod:
			className := reference.Class
			if className == "" {
				path, _ := uriutil.Path(request.TextDocument.URI)
				listener, found, err := p.index.ListenerAt(
					path,
					reference.Node.RangeTrimmedTrivia().Start,
				)
				if err != nil || !found {
					return nil, err
				}
				className = listener.Class
			}
			events, err := p.index.EventsForListener(
				className,
				reference.Name,
			)
			if err != nil {
				return nil, err
			}
			return listenerEventLocations(events, className, reference.Name), nil
		}
	}

	if !strings.EqualFold(filepath.Ext(request.TextDocument.URI), ".php") {
		return nil, nil
	}
	method := phpquery.MethodAt(request.Node)
	if method == nil {
		return nil, nil
	}
	methodName := phpquery.MethodName(method)
	class := phpquery.ClassAt(method)
	if methodName == "" || class == nil {
		return nil, nil
	}
	nameResolver := php.NewNameResolver(request.Root)
	className := strings.TrimPrefix(
		nameResolver.Resolve(phpquery.ClassName(class)),
		`\`,
	)
	events, err := p.index.EventsForListener(className, methodName)
	if err != nil {
		return nil, err
	}
	return listenerEventLocations(events, className, methodName), nil
}

func listenerEventLocations(
	events []event.Event,
	className,
	methodName string,
) []protocol.Location {
	var occurrences []event.Occurrence
	for _, current := range events {
		for _, occurrence := range current.Occurrences {
			if occurrence.Kind == event.DispatchOccurrence ||
				strings.EqualFold(occurrence.Class, className) &&
					strings.EqualFold(occurrence.Method, methodName) {
				occurrences = append(occurrences, occurrence)
			}
		}
	}
	return eventOccurrenceLocations(occurrences, true)
}

func eventOccurrenceLocations(
	occurrences []event.Occurrence,
	preferMethod bool,
) []protocol.Location {
	seen := make(map[string]struct{}, len(occurrences))
	var result []protocol.Location
	for _, occurrence := range occurrences {
		rng := occurrence.Range
		if preferMethod && occurrence.MethodRange.Len() != 0 {
			rng = occurrence.MethodRange
		}
		location, found := eventLocation(occurrence.File, rng)
		if !found {
			continue
		}
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

func eventLocation(
	path string,
	rng cst.TextRange,
) (protocol.Location, bool) {
	source, err := os.ReadFile(path)
	if err != nil {
		return protocol.Location{}, false
	}
	lineIndex := cst.NewLineIndex(string(source))
	startLine, startCharacter := lineIndex.PositionUTF16(rng.Start)
	endLine, endCharacter := lineIndex.PositionUTF16(rng.End)
	return protocol.Location{
		URI: uriutil.FileURI(path),
		Range: protocol.Range{
			Start: protocol.Position{
				Line:      int(startLine),
				Character: int(startCharacter),
			},
			End: protocol.Position{
				Line:      int(endLine),
				Character: int(endCharacter),
			},
		},
	}, true
}
