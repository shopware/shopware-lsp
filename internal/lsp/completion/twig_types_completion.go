package completion

import (
	"sort"
	"strings"

	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	"github.com/shopware/shopware-lsp/internal/php/semantic"
	"github.com/shopware/shopware-lsp/internal/twig"
)

func (p *TwigCompletionProvider) twigTypesTagCompletions(
	request *lsp.CompletionRequest,
) []protocol.CompletionItem {
	if p == nil || request == nil || request.CompletionParams == nil ||
		request.LineIndex == nil {
		return nil
	}
	offset := request.LineIndex.OffsetUTF16(
		uint32(request.Position.Line),
		uint32(request.Position.Character),
	)
	completion, found := twig.TypesTagCompletionAt(
		request.DocumentContent,
		offset,
	)
	if !found {
		return nil
	}
	if p.phpIndex == nil {
		return []protocol.CompletionItem{}
	}
	startLine, startCharacter := request.LineIndex.PositionUTF16(
		completion.Range.Start,
	)
	endLine, endCharacter := request.LineIndex.PositionUTF16(
		completion.Range.End,
	)
	prefix := strings.ToLower(strings.TrimPrefix(completion.Prefix, `\`))
	seen := make(map[string]struct{})
	var items []protocol.CompletionItem
	for _, symbol := range p.phpIndex.ClassSymbols() {
		name := strings.TrimPrefix(symbol.FullyQualified, `\`)
		key := strings.ToLower(name)
		if _, duplicate := seen[key]; duplicate ||
			prefix != "" && !strings.HasPrefix(key, prefix) {
			continue
		}
		seen[key] = struct{}{}
		kind := protocol.ClassCompletion
		detail := "PHP class"
		switch symbol.Kind {
		case semantic.InterfaceSymbol:
			kind = protocol.InterfaceCompletion
			detail = "PHP interface"
		case semantic.EnumSymbol:
			kind = protocol.EnumCompletion
			detail = "PHP enum"
		case semantic.TraitSymbol:
			detail = "PHP trait"
		}
		item := protocol.CompletionItem{
			Label:      name,
			FilterText: name + " " + twig.EscapeTwigClassName(name),
			Kind:       int(kind),
			Detail:     detail + " for Twig types tag",
			TextEdit: protocol.TextEdit{
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
				NewText: twig.EscapeTwigClassName(name),
			},
		}
		if symbol.Flags.Has(semantic.DeprecatedFlag) {
			item.Deprecated = true
		}
		items = append(items, item)
	}
	sort.Slice(items, func(left, right int) bool {
		return strings.ToLower(items[left].Label) <
			strings.ToLower(items[right].Label)
	})
	return items
}
