package phpsemantic

import (
	"context"
	"strings"

	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	phpquery "github.com/shopware/shopware-lsp/internal/parser/php/query"
	"github.com/shopware/shopware-lsp/internal/php"
	"github.com/shopware/shopware-lsp/internal/php/semantic"
)

func (p *Provider) GetDefinition(
	ctx context.Context,
	request *lsp.DefinitionRequest,
) []protocol.Location {
	if !isPHP(request.TextDocument.URI) || request.LineIndex == nil {
		return nil
	}
	phpContext := php.GetPHPContext(ctx)
	if phpContext == nil || phpContext.Document == nil || phpContext.Snapshot == nil {
		return nil
	}
	if mode, _, found := assistantClassReference(
		ctx,
		request.Node,
	); found {
		name := strings.TrimPrefix(
			strings.TrimSpace(phpquery.StringValue(request.Node)),
			"\\",
		)
		var symbols []semantic.Symbol
		for _, symbol := range phpContext.Snapshot.Classes(name) {
			if assistantClassKindAllowed(mode, symbol.Kind) &&
				!symbol.Flags.Has(semantic.InternalFlag) {
				symbols = append(symbols, symbol)
			}
		}
		return locationsForSymbols(symbols, request.Document)
	}
	offset := request.LineIndex.OffsetUTF16(
		uint32(request.Position.Line),
		uint32(request.Position.Character),
	)
	var symbols []semantic.Symbol
	if reference, ok := php.ReferenceAt(phpContext.Document, offset); ok {
		symbols = append(
			symbols,
			referenceCandidates(phpContext.Snapshot, reference)...,
		)
	}
	if len(symbols) == 0 {
		if symbol, ok := php.SymbolAt(
			phpContext.Document,
			phpContext.Snapshot,
			offset,
		); ok {
			symbols = append(symbols, symbol)
		}
	}
	return locationsForSymbols(symbols, request.Document)
}
