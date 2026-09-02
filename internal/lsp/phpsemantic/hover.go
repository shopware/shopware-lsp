package phpsemantic

import (
	"context"

	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	"github.com/shopware/shopware-lsp/internal/php"
	"github.com/shopware/shopware-lsp/internal/php/semantic"
)

func (p *Provider) GetHover(
	ctx context.Context,
	request *lsp.HoverRequest,
) (*protocol.Hover, error) {
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
	if symbol, ok := php.SymbolAt(phpContext.Document, phpContext.Snapshot, offset); ok {
		value := "```php\n" + formatSymbol(symbol) + "\n```"
		if symbol.DocSummary() != "" {
			value += "\n\n" + symbol.DocSummary()
		}
		if symbol.Flags.Has(semantic.DeprecatedFlag) {
			value += formatDeprecationHover(symbol)
		}
		rng := symbolRangeAt(phpContext.Document, offset)
		return &protocol.Hover{
			Contents: protocol.MarkupContent{Kind: protocol.Markdown, Value: value},
			Range:    rangeFromText(request.LineIndex, rng),
		}, nil
	}

	for current := request.Node; current != nil; current = current.Parent() {
		fact := phpContext.Document.TypeOf(current)
		if fact.Type.IsUnknown() {
			continue
		}
		value := "```php\n" + fact.Type.String() + "\n```"
		if fact.Reason != "" {
			value += "\n\n" + fact.Reason
		}
		return &protocol.Hover{
			Contents: protocol.MarkupContent{Kind: protocol.Markdown, Value: value},
			Range:    rangeFromText(request.LineIndex, current.RangeTrimmedTrivia()),
		}, nil
	}
	return nil, nil
}
