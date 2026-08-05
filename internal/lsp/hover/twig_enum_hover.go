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
)

type TwigEnumHoverProvider struct {
	phpIndex *php.PHPIndex
}

func NewTwigEnumHoverProvider(
	phpIndex *php.PHPIndex,
) *TwigEnumHoverProvider {
	return &TwigEnumHoverProvider{phpIndex: phpIndex}
}

func (p *TwigEnumHoverProvider) GetHover(
	_ context.Context,
	request *lsp.HoverRequest,
) (*protocol.Hover, error) {
	if p == nil || p.phpIndex == nil || request == nil ||
		request.Node == nil || request.LineIndex == nil ||
		strings.ToLower(filepath.Ext(request.TextDocument.URI)) != ".twig" {
		return nil, nil
	}
	reference, found := twig.EnumReferenceAt(request.Node)
	if !found {
		return nil, nil
	}
	symbol, exists := p.phpIndex.FindClass(reference.Name)
	if !exists {
		return nil, nil
	}
	var markdown strings.Builder
	if symbol.Kind == semantic.EnumSymbol {
		fmt.Fprintf(
			&markdown,
			"**PHP enum** `%s`",
			escapeSecurityMarkdown(symbol.FullyQualified),
		)
		cases := twigEnumCases(p.phpIndex, symbol)
		if len(cases) != 0 {
			fmt.Fprintf(
				&markdown,
				"\n\nCases: `%s`",
				escapeSecurityMarkdown(strings.Join(cases, "`, `")),
			)
		}
	} else {
		fmt.Fprintf(
			&markdown,
			"**PHP class** `%s`\n\nThis class is not an enum.",
			escapeSecurityMarkdown(symbol.FullyQualified),
		)
	}
	return &protocol.Hover{
		Contents: protocol.MarkupContent{
			Kind:  protocol.Markdown,
			Value: markdown.String(),
		},
		Range: securityProtocolRange(reference.Range, request.LineIndex),
	}, nil
}

func twigEnumCases(
	index *php.PHPIndex,
	enum semantic.Symbol,
) []string {
	var result []string
	for _, member := range index.SemanticSnapshot().MembersOf(enum.ID) {
		if member.Kind == semantic.EnumCaseSymbol {
			result = append(result, member.Name)
		}
	}
	sort.Strings(result)
	return result
}
