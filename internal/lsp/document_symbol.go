package lsp

import (
	"context"
	"fmt"
	"sort"

	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
)

func (s *Server) documentSymbols(
	ctx context.Context,
	params *protocol.DocumentSymbolParams,
) ([]protocol.DocumentSymbol, error) {
	if params == nil {
		return nil, nil
	}
	document, ok := s.documentManager.GetDocument(params.TextDocument.URI)
	if !ok {
		return nil, nil
	}
	request := &DocumentSymbolRequest{
		DocumentSymbolParams: params,
		Document:             document,
	}
	var result []protocol.DocumentSymbol
	seen := make(map[documentSymbolKey]bool)
	for _, provider := range s.documentSymbolProviders {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		symbols, err := provider.GetDocumentSymbols(ctx, request)
		if err != nil {
			return nil, fmt.Errorf(
				"document symbol provider %T: %w", provider, err,
			)
		}
		for _, symbol := range symbols {
			key := newDocumentSymbolKey(symbol)
			if seen[key] {
				continue
			}
			seen[key] = true
			result = append(result, symbol)
		}
	}
	sortDocumentSymbols(result)
	return result, nil
}

type documentSymbolKey struct {
	name                      string
	kind                      protocol.SymbolKind
	startLine, startCharacter int
	endLine, endCharacter     int
}

func newDocumentSymbolKey(symbol protocol.DocumentSymbol) documentSymbolKey {
	return documentSymbolKey{
		name:           symbol.Name,
		kind:           symbol.Kind,
		startLine:      symbol.Range.Start.Line,
		startCharacter: symbol.Range.Start.Character,
		endLine:        symbol.Range.End.Line,
		endCharacter:   symbol.Range.End.Character,
	}
}

func sortDocumentSymbols(symbols []protocol.DocumentSymbol) {
	for index := range symbols {
		sortDocumentSymbols(symbols[index].Children)
	}
	sort.SliceStable(symbols, func(left, right int) bool {
		leftRange := symbols[left].Range
		rightRange := symbols[right].Range
		if leftRange.Start.Line != rightRange.Start.Line {
			return leftRange.Start.Line < rightRange.Start.Line
		}
		if leftRange.Start.Character != rightRange.Start.Character {
			return leftRange.Start.Character < rightRange.Start.Character
		}
		if leftRange.End.Line != rightRange.End.Line {
			return leftRange.End.Line < rightRange.End.Line
		}
		if leftRange.End.Character != rightRange.End.Character {
			return leftRange.End.Character < rightRange.End.Character
		}
		if symbols[left].Kind != symbols[right].Kind {
			return symbols[left].Kind < symbols[right].Kind
		}
		return symbols[left].Name < symbols[right].Name
	})
}
