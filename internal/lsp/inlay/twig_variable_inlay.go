package inlay

import (
	"context"
	"fmt"
	"strings"

	"github.com/shopware/shopware-lsp/internal/language"
	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	"github.com/shopware/shopware-lsp/internal/php"
	"github.com/shopware/shopware-lsp/internal/twig"
	"github.com/shopware/shopware-lsp/internal/uriutil"
)

type TwigVariableProvider struct {
	phpIndex *php.PHPIndex
}

const BrowseTwigVariablesCommand = "shopware.symfony.twigVariables"

func NewTwigVariableProvider(phpIndex *php.PHPIndex) *TwigVariableProvider {
	return &TwigVariableProvider{phpIndex: phpIndex}
}

func (p *TwigVariableProvider) GetInlayHints(
	ctx context.Context,
	request *lsp.InlayHintRequest,
) ([]protocol.InlayHint, error) {
	if ctx.Err() != nil || p == nil || p.phpIndex == nil ||
		request == nil || request.InlayHintParams == nil ||
		request.Document == nil ||
		request.Document.SyntaxLanguage != language.Twig {
		return nil, nil
	}
	rangeStart := request.Document.LineIndex.OffsetUTF16(
		uint32(max(request.Range.Start.Line, 0)),
		uint32(max(request.Range.Start.Character, 0)),
	)
	if rangeStart != 0 {
		return nil, nil
	}
	path, err := uriutil.Path(request.Document.URI)
	if err != nil {
		return nil, nil
	}
	variables, err := p.phpIndex.TwigTemplateVariables(
		twig.TemplateNames(path)...,
	)
	if err != nil || len(variables) == 0 {
		return nil, err
	}
	lines := make([]string, 0, len(variables))
	names := make([]string, 0, len(variables))
	for _, variable := range variables {
		typeName := variable.Type
		if typeName == "" {
			typeName = "unknown"
		}
		lines = append(lines, variable.Name+": "+typeName)
		names = append(names, variable.Name)
	}
	tooltip := strings.Join(lines, "\n")
	return []protocol.InlayHint{{
		Position: protocol.Position{},
		Label: []protocol.InlayHintLabelPart{{
			Value:   fmt.Sprintf("Variables (%d)", len(variables)),
			Tooltip: "Browse typed Twig variables and insert expressions",
			Command: &protocol.Command{
				Title:   "Browse Twig variables",
				Command: BrowseTwigVariablesCommand,
				Arguments: []interface{}{
					request.Document.URI,
					names,
				},
			},
		}},
		Kind:         protocol.InlayHintKindType,
		Tooltip:      tooltip,
		PaddingRight: true,
	}}, nil
}

var _ lsp.InlayHintProvider = (*TwigVariableProvider)(nil)
