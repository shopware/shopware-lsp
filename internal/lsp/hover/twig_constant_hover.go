package hover

import (
	"context"
	"fmt"
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

type TwigConstantHoverProvider struct {
	phpIndex  *php.PHPIndex
	twigIndex *twig.TwigIndexer
}

func NewTwigConstantHoverProvider(
	phpIndex *php.PHPIndex,
	twigIndex *twig.TwigIndexer,
) *TwigConstantHoverProvider {
	return &TwigConstantHoverProvider{
		phpIndex:  phpIndex,
		twigIndex: twigIndex,
	}
}

func (p *TwigConstantHoverProvider) GetHover(
	_ context.Context,
	request *lsp.HoverRequest,
) (*protocol.Hover, error) {
	if p == nil || p.phpIndex == nil || request == nil ||
		request.Node == nil || request.Root == nil ||
		request.LineIndex == nil ||
		strings.ToLower(filepath.Ext(request.TextDocument.URI)) != ".twig" {
		return nil, nil
	}
	path, err := uriutil.Path(request.TextDocument.URI)
	if err != nil {
		return nil, nil
	}
	references := twig.ConstantReferencesAt(
		path,
		request.Root,
		request.Node,
		twig.PHPAccessResolver{
			PHP:  p.phpIndex,
			Twig: p.twigIndex,
		},
	)
	var symbols []semantic.Symbol
	seen := make(map[semantic.SymbolID]struct{})
	for _, reference := range references {
		var current []semantic.Symbol
		if reference.Class != "" {
			current = p.phpIndex.FindConstants(
				reference.Class,
				reference.Name,
			)
		} else {
			current = p.phpIndex.FindGlobalConstants(reference.Name)
		}
		for _, symbol := range current {
			if symbol.Kind != semantic.GlobalConstantSymbol &&
				symbol.Visibility != semantic.Public {
				continue
			}
			if _, duplicate := seen[symbol.ID]; duplicate {
				continue
			}
			seen[symbol.ID] = struct{}{}
			symbols = append(symbols, symbol)
		}
	}
	if len(symbols) == 0 {
		return nil, nil
	}
	sort.Slice(symbols, func(left, right int) bool {
		return p.phpIndex.ConstantSymbolName(symbols[left]) <
			p.phpIndex.ConstantSymbolName(symbols[right])
	})
	var markdown strings.Builder
	for index, symbol := range symbols {
		if index != 0 {
			markdown.WriteString("\n\n---\n\n")
		}
		kind := "PHP constant"
		switch symbol.Kind {
		case semantic.ClassConstantSymbol:
			kind = "PHP class constant"
		case semantic.EnumCaseSymbol:
			kind = "PHP enum case"
		}
		fmt.Fprintf(
			&markdown,
			"**%s** `%s`",
			kind,
			escapeSecurityMarkdown(
				p.phpIndex.ConstantSymbolName(symbol),
			),
		)
		if !symbol.Type.IsUnknown() {
			fmt.Fprintf(
				&markdown,
				"\n\nType: `%s`",
				escapeSecurityMarkdown(symbol.Type.String()),
			)
		}
		if symbol.DocSummary != "" {
			fmt.Fprintf(
				&markdown,
				"\n\n%s",
				escapeSecurityMarkdown(symbol.DocSummary),
			)
		}
		if symbol.Flags.Has(semantic.DeprecatedFlag) {
			markdown.WriteString("\n\nDeprecated")
		}
	}
	return &protocol.Hover{
		Contents: protocol.MarkupContent{
			Kind:  protocol.Markdown,
			Value: markdown.String(),
		},
		Range: securityProtocolRange(
			references[0].Range,
			request.LineIndex,
		),
	}, nil
}
