package completion

import (
	"context"
	"path/filepath"
	"sort"
	"strings"

	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	"github.com/shopware/shopware-lsp/internal/php"
	"github.com/shopware/shopware-lsp/internal/php/semantic"
	"github.com/shopware/shopware-lsp/internal/twig"
)

type TwigEnumCompletionProvider struct {
	phpIndex *php.PHPIndex
}

func NewTwigEnumCompletionProvider(
	phpIndex *php.PHPIndex,
) *TwigEnumCompletionProvider {
	return &TwigEnumCompletionProvider{phpIndex: phpIndex}
}

func (p *TwigEnumCompletionProvider) GetCompletions(
	_ context.Context,
	request *lsp.CompletionRequest,
) []protocol.CompletionItem {
	if p == nil || p.phpIndex == nil || request == nil ||
		request.Node == nil ||
		strings.ToLower(filepath.Ext(request.TextDocument.URI)) != ".twig" {
		return nil
	}
	if !twig.EnumCompletionAt(request.Node) {
		return nil
	}
	var items []protocol.CompletionItem
	for _, symbol := range p.phpIndex.ClassSymbols() {
		if symbol.Kind != semantic.EnumSymbol {
			continue
		}
		name := strings.TrimPrefix(symbol.FullyQualified, `\`)
		items = append(items, protocol.CompletionItem{
			Label:      name,
			FilterText: name + " " + twig.EscapeTwigClassName(name),
			InsertText: twig.EscapeTwigClassName(name),
			Kind:       int(protocol.EnumCompletion),
			Detail:     "PHP enum",
		})
	}
	sort.Slice(items, func(left, right int) bool {
		return strings.ToLower(items[left].Label) <
			strings.ToLower(items[right].Label)
	})
	return items
}

func (p *TwigEnumCompletionProvider) GetTriggerCharacters() []string {
	return []string{"\\"}
}
