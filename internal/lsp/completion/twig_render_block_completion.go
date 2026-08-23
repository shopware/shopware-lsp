package completion

import (
	"context"
	"path/filepath"
	"sort"
	"strings"

	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	"github.com/shopware/shopware-lsp/internal/php"
	"github.com/shopware/shopware-lsp/internal/twig"
)

type TwigRenderBlockCompletionProvider struct {
	twigIndex *twig.TwigIndexer
	phpIndex  *php.PHPIndex
}

func NewTwigRenderBlockCompletionProvider(
	twigIndex *twig.TwigIndexer,
	phpIndex *php.PHPIndex,
) *TwigRenderBlockCompletionProvider {
	return &TwigRenderBlockCompletionProvider{
		twigIndex: twigIndex,
		phpIndex:  phpIndex,
	}
}

func (p *TwigRenderBlockCompletionProvider) GetCompletions(
	ctx context.Context,
	request *lsp.CompletionRequest,
) []protocol.CompletionItem {
	if p == nil || p.twigIndex == nil || p.phpIndex == nil ||
		request == nil || request.Root == nil ||
		request.LineIndex == nil ||
		!strings.EqualFold(filepath.Ext(request.TextDocument.URI), ".php") {
		return nil
	}
	offset := request.LineIndex.OffsetUTF16(
		uint32(request.Position.Line),
		uint32(request.Position.Character),
	)
	reference, found := twig.RenderBlockReferenceAt(request.Root, offset)
	if !found || !twig.ValidateRenderBlockReference(
		ctx,
		reference,
		p.phpIndex,
		request.DocumentContent,
	) {
		return nil
	}
	blocks, err := p.twigIndex.GetTemplateBlocks(reference.Template)
	if err != nil {
		return nil
	}
	byName := make(map[string]twig.TemplateBlock)
	for _, block := range blocks {
		key := strings.ToLower(block.Name)
		if _, exists := byName[key]; !exists {
			byName[key] = block
		}
	}
	items := make([]protocol.CompletionItem, 0, len(byName))
	for _, block := range byName {
		item := protocol.CompletionItem{
			Label:  block.Name,
			Kind:   int(protocol.ReferenceCompletion),
			Detail: "Twig block",
		}
		item.Documentation.Kind = string(protocol.Markdown)
		item.Documentation.Value = "Block reachable from `" +
			reference.Template + "`."
		if block.Documentation != "" {
			item.Documentation.Value += "\n\n" + block.Documentation
		}
		items = append(items, item)
	}
	sort.Slice(items, func(left, right int) bool {
		return strings.ToLower(items[left].Label) <
			strings.ToLower(items[right].Label)
	})
	return items
}

func (p *TwigRenderBlockCompletionProvider) GetTriggerCharacters() []string {
	return []string{"'", "\""}
}
