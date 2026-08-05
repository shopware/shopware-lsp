package lsp

import (
	"context"
	"fmt"
	"sort"

	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
)

func (s *Server) prepareCallHierarchy(
	ctx context.Context,
	params *protocol.CallHierarchyPrepareParams,
) ([]protocol.CallHierarchyItem, error) {
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
	request := &CallHierarchyPrepareRequest{
		CallHierarchyPrepareParams: params,
		SyntaxContext:              syntax,
	}
	ctx = s.enrichContext(ctx, syntax)
	var result []protocol.CallHierarchyItem
	seen := make(map[callHierarchyItemKey]bool)
	for _, provider := range s.callHierarchyProviders {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		items, err := provider.PrepareCallHierarchy(ctx, request)
		if err != nil {
			return nil, fmt.Errorf(
				"prepare call hierarchy provider %T: %w", provider, err,
			)
		}
		for _, item := range items {
			key := newCallHierarchyItemKey(item)
			if seen[key] {
				continue
			}
			seen[key] = true
			result = append(result, item)
		}
	}
	sortCallHierarchyItems(result)
	return result, nil
}

func (s *Server) callHierarchyIncomingCalls(
	ctx context.Context,
	item protocol.CallHierarchyItem,
) ([]protocol.CallHierarchyIncomingCall, error) {
	request := &CallHierarchyCallsRequest{
		Item: item, Documents: s.documentManager.Documents(),
	}
	var result []protocol.CallHierarchyIncomingCall
	positions := make(map[callHierarchyItemKey]int)
	for _, provider := range s.callHierarchyProviders {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		calls, err := provider.IncomingCalls(ctx, request)
		if err != nil {
			return nil, fmt.Errorf(
				"incoming call hierarchy provider %T: %w", provider, err,
			)
		}
		for _, call := range calls {
			key := newCallHierarchyItemKey(call.From)
			if index, found := positions[key]; found {
				result[index].FromRanges = mergeCallHierarchyRanges(
					result[index].FromRanges, call.FromRanges,
				)
				continue
			}
			call.FromRanges = mergeCallHierarchyRanges(call.FromRanges)
			positions[key] = len(result)
			result = append(result, call)
		}
	}
	sort.SliceStable(result, func(left, right int) bool {
		return callHierarchyItemLess(result[left].From, result[right].From)
	})
	return result, nil
}

func (s *Server) callHierarchyOutgoingCalls(
	ctx context.Context,
	item protocol.CallHierarchyItem,
) ([]protocol.CallHierarchyOutgoingCall, error) {
	request := &CallHierarchyCallsRequest{
		Item: item, Documents: s.documentManager.Documents(),
	}
	var result []protocol.CallHierarchyOutgoingCall
	positions := make(map[callHierarchyItemKey]int)
	for _, provider := range s.callHierarchyProviders {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		calls, err := provider.OutgoingCalls(ctx, request)
		if err != nil {
			return nil, fmt.Errorf(
				"outgoing call hierarchy provider %T: %w", provider, err,
			)
		}
		for _, call := range calls {
			key := newCallHierarchyItemKey(call.To)
			if index, found := positions[key]; found {
				result[index].FromRanges = mergeCallHierarchyRanges(
					result[index].FromRanges, call.FromRanges,
				)
				continue
			}
			call.FromRanges = mergeCallHierarchyRanges(call.FromRanges)
			positions[key] = len(result)
			result = append(result, call)
		}
	}
	sort.SliceStable(result, func(left, right int) bool {
		return callHierarchyItemLess(result[left].To, result[right].To)
	})
	return result, nil
}

type callHierarchyItemKey struct {
	uri,
	name string
	startLine,
	startCharacter,
	endLine,
	endCharacter int
}

func newCallHierarchyItemKey(
	item protocol.CallHierarchyItem,
) callHierarchyItemKey {
	return callHierarchyItemKey{
		uri: item.URI, name: item.Name,
		startLine:      item.SelectionRange.Start.Line,
		startCharacter: item.SelectionRange.Start.Character,
		endLine:        item.SelectionRange.End.Line,
		endCharacter:   item.SelectionRange.End.Character,
	}
}

func sortCallHierarchyItems(items []protocol.CallHierarchyItem) {
	sort.SliceStable(items, func(left, right int) bool {
		return callHierarchyItemLess(items[left], items[right])
	})
}

func callHierarchyItemLess(
	left,
	right protocol.CallHierarchyItem,
) bool {
	if left.URI != right.URI {
		return left.URI < right.URI
	}
	if left.SelectionRange.Start.Line != right.SelectionRange.Start.Line {
		return left.SelectionRange.Start.Line < right.SelectionRange.Start.Line
	}
	if left.SelectionRange.Start.Character !=
		right.SelectionRange.Start.Character {
		return left.SelectionRange.Start.Character <
			right.SelectionRange.Start.Character
	}
	return left.Name < right.Name
}

func mergeCallHierarchyRanges(groups ...[]protocol.Range) []protocol.Range {
	var result []protocol.Range
	seen := make(map[documentHighlightRangeKey]bool)
	for _, group := range groups {
		for _, rangeValue := range group {
			key := newDocumentHighlightRangeKey(rangeValue)
			if seen[key] {
				continue
			}
			seen[key] = true
			result = append(result, rangeValue)
		}
	}
	sort.SliceStable(result, func(left, right int) bool {
		if result[left].Start.Line != result[right].Start.Line {
			return result[left].Start.Line < result[right].Start.Line
		}
		if result[left].Start.Character != result[right].Start.Character {
			return result[left].Start.Character < result[right].Start.Character
		}
		if result[left].End.Line != result[right].End.Line {
			return result[left].End.Line < result[right].End.Line
		}
		return result[left].End.Character < result[right].End.Character
	})
	return result
}
