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
	"github.com/shopware/shopware-lsp/internal/uriutil"
)

type TwigConstantCompletionProvider struct {
	phpIndex  *php.PHPIndex
	twigIndex *twig.TwigIndexer
}

func NewTwigConstantCompletionProvider(
	phpIndex *php.PHPIndex,
	twigIndex *twig.TwigIndexer,
) *TwigConstantCompletionProvider {
	return &TwigConstantCompletionProvider{
		phpIndex:  phpIndex,
		twigIndex: twigIndex,
	}
}

func (p *TwigConstantCompletionProvider) GetCompletions(
	_ context.Context,
	request *lsp.CompletionRequest,
) []protocol.CompletionItem {
	if p == nil || p.phpIndex == nil || request == nil ||
		request.Node == nil || request.Root == nil ||
		request.LineIndex == nil ||
		strings.ToLower(filepath.Ext(request.TextDocument.URI)) != ".twig" {
		return nil
	}
	path, err := uriutil.Path(request.TextDocument.URI)
	if err != nil {
		return nil
	}
	completion, found := twig.ConstantCompletionContextAt(
		path,
		request.Root,
		request.Node,
		twig.PHPAccessResolver{
			PHP:  p.phpIndex,
			Twig: p.twigIndex,
		},
	)
	if !found {
		return nil
	}
	switch {
	case completion.Class != "":
		partial := completion.Value
		if separator := strings.LastIndex(partial, "::"); separator >= 0 {
			partial = partial[separator+2:]
		}
		return p.memberItems(
			[]string{completion.Class},
			partial,
			completion,
			false,
			request,
		)
	case completion.ObjectArgument:
		if len(completion.ReceiverClasses) == 0 {
			return nil
		}
		return p.memberItems(
			completion.ReceiverClasses,
			completion.Value,
			completion,
			true,
			request,
		)
	default:
		return p.allItems(completion, request)
	}
}

func (p *TwigConstantCompletionProvider) memberItems(
	classes []string,
	partial string,
	completion twig.ConstantCompletionContext,
	objectRelative bool,
	request *lsp.CompletionRequest,
) []protocol.CompletionItem {
	var items []protocol.CompletionItem
	seen := make(map[string]struct{})
	for _, class := range classes {
		for _, symbol := range p.phpIndex.Constants(class) {
			if symbol.Visibility != semantic.Public ||
				!strings.HasPrefix(
					strings.ToLower(symbol.Name),
					strings.ToLower(partial),
				) {
				continue
			}
			key := strings.ToLower(class) + "\x00" +
				strings.ToLower(symbol.Name)
			if _, duplicate := seen[key]; duplicate {
				continue
			}
			seen[key] = struct{}{}
			insert := symbol.Name
			if !objectRelative {
				insert = twig.EscapeTwigClassName(class) +
					"::" + symbol.Name
			}
			items = append(items, twigConstantCompletionItem(
				symbol.Name,
				class+"::"+symbol.Name,
				insert,
				symbol,
				completion,
				request,
			))
		}
	}
	sortTwigConstantItems(items)
	return items
}

func (p *TwigConstantCompletionProvider) allItems(
	completion twig.ConstantCompletionContext,
	request *lsp.CompletionRequest,
) []protocol.CompletionItem {
	prefix := strings.TrimPrefix(completion.Value, "\\")
	var items []protocol.CompletionItem
	seen := make(map[string]struct{})
	for _, symbol := range p.phpIndex.ConstantSymbols() {
		if symbol.Kind != semantic.GlobalConstantSymbol &&
			symbol.Visibility != semantic.Public {
			continue
		}
		display := p.phpIndex.ConstantSymbolName(symbol)
		switch strings.ToLower(display) {
		case "true", "false", "null":
			continue
		}
		if !strings.HasPrefix(
			strings.ToLower(display),
			strings.ToLower(prefix),
		) {
			continue
		}
		if _, duplicate := seen[display]; duplicate {
			continue
		}
		seen[display] = struct{}{}
		items = append(items, twigConstantCompletionItem(
			twigConstantShortLabel(display),
			display,
			twig.EscapeTwigClassName(display),
			symbol,
			completion,
			request,
		))
	}
	sortTwigConstantItems(items)
	return items
}

func twigConstantCompletionItem(
	label,
	detail,
	insert string,
	symbol semantic.Symbol,
	completion twig.ConstantCompletionContext,
	request *lsp.CompletionRequest,
) protocol.CompletionItem {
	kind := protocol.ConstantCompletion
	if symbol.Kind == semantic.EnumCaseSymbol {
		kind = protocol.EnumMemberCompletion
	}
	item := protocol.CompletionItem{
		Label:      label,
		FilterText: detail + " " + insert,
		Kind:       int(kind),
		Detail:     detail,
		TextEdit: protocol.TextEdit{
			Range: containerConstantCompletionRange(
				completion.ContentRange,
				request.LineIndex,
			),
			NewText: insert,
		},
	}
	if symbol.DocSummary != "" {
		item.Documentation.Kind = string(protocol.Markdown)
		item.Documentation.Value = symbol.DocSummary
	}
	if symbol.Flags.Has(semantic.DeprecatedFlag) {
		item.Deprecated = true
	}
	return item
}

func twigConstantShortLabel(display string) string {
	if separator := strings.LastIndex(display, "::"); separator >= 0 {
		class := display[:separator]
		if namespace := strings.LastIndex(class, "\\"); namespace >= 0 {
			class = class[namespace+1:]
		}
		return class + display[separator:]
	}
	if namespace := strings.LastIndex(display, "\\"); namespace >= 0 {
		return display[namespace+1:]
	}
	return display
}

func sortTwigConstantItems(items []protocol.CompletionItem) {
	sort.Slice(items, func(left, right int) bool {
		if !strings.EqualFold(items[left].Label, items[right].Label) {
			return strings.ToLower(items[left].Label) <
				strings.ToLower(items[right].Label)
		}
		return strings.ToLower(items[left].Detail) <
			strings.ToLower(items[right].Detail)
	})
}

func (p *TwigConstantCompletionProvider) GetTriggerCharacters() []string {
	return []string{"\\", ":"}
}
