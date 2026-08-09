package phpsemantic

import (
	"context"

	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	"github.com/shopware/shopware-lsp/internal/parser/cst"
	"github.com/shopware/shopware-lsp/internal/php"
)

func (p *Provider) GetReferences(
	ctx context.Context,
	request *lsp.ReferenceRequest,
) ([]protocol.Location, error) {
	if !isPHP(request.TextDocument.URI) || request.LineIndex == nil {
		return nil, nil
	}
	phpContext := php.GetPHPContext(ctx)
	if phpContext == nil || phpContext.Document == nil || phpContext.Snapshot == nil {
		return nil, nil
	}
	offset := request.LineIndex.OffsetUTF16(
		uint32(request.Position.Line),
		uint32(request.Position.Character),
	)
	symbol, ok := php.SymbolAt(phpContext.Document, phpContext.Snapshot, offset)
	if !ok {
		return nil, nil
	}
	cache := newLocationCache(request.Document)
	var locations []protocol.Location
	if request.Context.IncludeDeclaration {
		locations = append(locations, cache.symbol(symbol))
	}
	for _, reference := range phpContext.Snapshot.ReferencesTo(symbol.ID) {
		locations = append(locations, cache.textRange(
			reference.Path,
			cst.TextRange{Start: reference.RangeStart, End: reference.RangeEnd},
		))
	}
	return locations, nil
}
