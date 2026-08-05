package lsp

import (
	"context"
	"fmt"
	"sort"

	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
)

func (s *Server) documentHighlights(
	ctx context.Context,
	params *protocol.DocumentHighlightParams,
) ([]protocol.DocumentHighlight, error) {
	if params == nil {
		return nil, nil
	}
	syntax, ok := s.documentManager.SyntaxContext(
		params.TextDocument.URI,
		params.Position.Line,
		params.Position.Character,
	)
	if !ok {
		return nil, nil
	}
	request := &DocumentHighlightRequest{
		DocumentHighlightParams: params,
		SyntaxContext:           syntax,
	}
	ctx = s.enrichContext(ctx, syntax)
	var result []protocol.DocumentHighlight
	positions := make(map[documentHighlightRangeKey]int)
	for _, provider := range s.documentHighlightProviders {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		highlights, err := provider.GetDocumentHighlights(ctx, request)
		if err != nil {
			return nil, fmt.Errorf(
				"document highlight provider %T: %w", provider, err,
			)
		}
		for _, highlight := range highlights {
			key := newDocumentHighlightRangeKey(highlight.Range)
			if index, found := positions[key]; found {
				if documentHighlightKindPriority(highlight.Kind) >
					documentHighlightKindPriority(result[index].Kind) {
					result[index].Kind = highlight.Kind
				}
				continue
			}
			positions[key] = len(result)
			result = append(result, highlight)
		}
	}
	sort.SliceStable(result, func(left, right int) bool {
		leftRange := result[left].Range
		rightRange := result[right].Range
		if leftRange.Start.Line != rightRange.Start.Line {
			return leftRange.Start.Line < rightRange.Start.Line
		}
		if leftRange.Start.Character != rightRange.Start.Character {
			return leftRange.Start.Character < rightRange.Start.Character
		}
		if leftRange.End.Line != rightRange.End.Line {
			return leftRange.End.Line < rightRange.End.Line
		}
		return leftRange.End.Character < rightRange.End.Character
	})
	return result, nil
}

type documentHighlightRangeKey struct {
	startLine,
	startCharacter,
	endLine,
	endCharacter int
}

func newDocumentHighlightRangeKey(
	rangeValue protocol.Range,
) documentHighlightRangeKey {
	return documentHighlightRangeKey{
		startLine:      rangeValue.Start.Line,
		startCharacter: rangeValue.Start.Character,
		endLine:        rangeValue.End.Line,
		endCharacter:   rangeValue.End.Character,
	}
}

func documentHighlightKindPriority(kind protocol.DocumentHighlightKind) int {
	switch kind {
	case protocol.DocumentHighlightWrite:
		return 3
	case protocol.DocumentHighlightRead:
		return 2
	default:
		return 1
	}
}
