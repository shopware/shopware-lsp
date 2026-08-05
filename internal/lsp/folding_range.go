package lsp

import (
	"context"
	"fmt"
	"sort"

	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
)

func (s *Server) foldingRanges(
	ctx context.Context,
	params *protocol.FoldingRangeParams,
) ([]protocol.FoldingRange, error) {
	if params == nil {
		return nil, nil
	}
	document, ok := s.documentManager.GetDocument(params.TextDocument.URI)
	if !ok {
		return nil, nil
	}
	request := &FoldingRangeRequest{
		FoldingRangeParams: params, Document: document,
	}
	var result []protocol.FoldingRange
	seen := make(map[foldingRangeKey]bool)
	for _, provider := range s.foldingRangeProviders {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		ranges, err := provider.GetFoldingRanges(ctx, request)
		if err != nil {
			return nil, fmt.Errorf("folding range provider %T: %w", provider, err)
		}
		for _, rangeValue := range ranges {
			if rangeValue.StartLine < 0 || rangeValue.EndLine <= rangeValue.StartLine {
				continue
			}
			key := newFoldingRangeKey(rangeValue)
			if seen[key] {
				continue
			}
			seen[key] = true
			result = append(result, rangeValue)
		}
	}
	sort.SliceStable(result, func(left, right int) bool {
		if result[left].StartLine != result[right].StartLine {
			return result[left].StartLine < result[right].StartLine
		}
		if result[left].EndLine != result[right].EndLine {
			return result[left].EndLine > result[right].EndLine
		}
		return result[left].Kind < result[right].Kind
	})
	return result, nil
}

type foldingRangeKey struct {
	startLine, startCharacter int
	endLine, endCharacter     int
	kind                      string
}

func newFoldingRangeKey(value protocol.FoldingRange) foldingRangeKey {
	startCharacter, endCharacter := -1, -1
	if value.StartCharacter != nil {
		startCharacter = *value.StartCharacter
	}
	if value.EndCharacter != nil {
		endCharacter = *value.EndCharacter
	}
	return foldingRangeKey{
		startLine: value.StartLine, startCharacter: startCharacter,
		endLine: value.EndLine, endCharacter: endCharacter, kind: value.Kind,
	}
}
