package hover

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	"github.com/shopware/shopware-lsp/internal/php"
	"github.com/shopware/shopware-lsp/internal/twig"
)

type TwigIncludeParameterHoverProvider struct {
	projectRoot string
	twigIndex   *twig.TwigIndexer
	phpIndex    *php.PHPIndex
}

func NewTwigIncludeParameterHoverProvider(
	projectRoot string,
	twigIndex *twig.TwigIndexer,
	phpIndex *php.PHPIndex,
) *TwigIncludeParameterHoverProvider {
	return &TwigIncludeParameterHoverProvider{
		projectRoot: projectRoot,
		twigIndex:   twigIndex,
		phpIndex:    phpIndex,
	}
}

func (p *TwigIncludeParameterHoverProvider) GetHover(
	_ context.Context,
	request *lsp.HoverRequest,
) (*protocol.Hover, error) {
	if p == nil || p.twigIndex == nil || request == nil ||
		request.Root == nil || request.LineIndex == nil ||
		!strings.HasSuffix(
			strings.ToLower(request.TextDocument.URI),
			".twig",
		) {
		return nil, nil
	}
	offset := request.LineIndex.OffsetUTF16(
		uint32(request.Position.Line),
		uint32(request.Position.Character),
	)
	parameter, found := twig.IncludeParameterAt(
		request.Root,
		request.Node,
		offset,
	)
	if !found {
		return nil, nil
	}
	variables, err := p.twigIndex.FindTemplateVariable(
		parameter.Name,
		parameter.Template,
	)
	if err != nil {
		return nil, err
	}
	phpType := ""
	phpFile := ""
	if p.phpIndex != nil {
		phpVariables, queryErr := p.phpIndex.TwigTemplateVariables(
			parameter.Template,
		)
		if queryErr != nil {
			return nil, queryErr
		}
		for _, variable := range phpVariables {
			if variable.Name == parameter.Name {
				phpType = variable.Type
				phpFile = variable.File
				break
			}
		}
	}
	if len(variables) == 0 && phpType == "" {
		return nil, nil
	}

	value := fmt.Sprintf(
		"**Twig include parameter** `%s`\n\nTarget template: `%s`",
		parameter.Name,
		parameter.Template,
	)
	if phpType != "" {
		value += fmt.Sprintf("\n\nPHP type: `%s`", phpType)
	}
	if len(variables) > 0 {
		value += fmt.Sprintf(
			"\n\nRead in `%s`.",
			p.displayPath(variables[0].FilePath),
		)
	} else if phpFile != "" {
		value += fmt.Sprintf(
			"\n\nProvided by `%s`.",
			p.displayPath(phpFile),
		)
	}
	return &protocol.Hover{
		Contents: protocol.MarkupContent{
			Kind:  protocol.Markdown,
			Value: value,
		},
		Range: securityProtocolRange(parameter.Range, request.LineIndex),
	}, nil
}

func (p *TwigIncludeParameterHoverProvider) displayPath(path string) string {
	if relative, err := filepath.Rel(p.projectRoot, path); err == nil {
		return filepath.ToSlash(relative)
	}
	return filepath.ToSlash(path)
}
