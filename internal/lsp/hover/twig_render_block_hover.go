package hover

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	"github.com/shopware/shopware-lsp/internal/parser/cst"
	"github.com/shopware/shopware-lsp/internal/php"
	"github.com/shopware/shopware-lsp/internal/twig"
)

type TwigRenderBlockHoverProvider struct {
	root      string
	twigIndex *twig.TwigIndexer
	phpIndex  *php.PHPIndex
}

func NewTwigRenderBlockHoverProvider(
	root string,
	twigIndex *twig.TwigIndexer,
	phpIndex *php.PHPIndex,
) *TwigRenderBlockHoverProvider {
	return &TwigRenderBlockHoverProvider{
		root:      root,
		twigIndex: twigIndex,
		phpIndex:  phpIndex,
	}
}

func (p *TwigRenderBlockHoverProvider) GetHover(
	ctx context.Context,
	request *lsp.HoverRequest,
) (*protocol.Hover, error) {
	if p == nil || p.twigIndex == nil || p.phpIndex == nil ||
		request == nil || request.Root == nil ||
		request.LineIndex == nil ||
		!strings.EqualFold(filepath.Ext(request.TextDocument.URI), ".php") {
		return nil, nil
	}
	offset := request.LineIndex.OffsetUTF16(
		uint32(request.Position.Line),
		uint32(request.Position.Character),
	)
	reference, found := twig.RenderBlockReferenceAt(request.Root, offset)
	if !found || reference.Block == "" ||
		!twig.ValidateRenderBlockReference(
			ctx,
			reference,
			p.phpIndex,
			request.DocumentContent,
		) {
		return nil, nil
	}
	blocks, err := p.twigIndex.GetTemplateBlocks(reference.Template)
	if err != nil {
		return nil, err
	}
	var declarations []twig.TemplateBlock
	for _, block := range blocks {
		if strings.EqualFold(block.Name, reference.Block) {
			declarations = append(declarations, block)
		}
	}
	if len(declarations) == 0 {
		return nil, nil
	}
	var markdown strings.Builder
	fmt.Fprintf(
		&markdown,
		"**Twig block** `%s`\n\nTemplate: `%s`",
		escapeRenderBlockMarkdown(reference.Block),
		escapeRenderBlockMarkdown(reference.Template),
	)
	for _, block := range declarations {
		display := block.FilePath
		if relative, relativeErr := filepath.Rel(p.root, block.FilePath); relativeErr == nil {
			display = filepath.ToSlash(relative)
		}
		fmt.Fprintf(
			&markdown,
			"\n\n- `%s:%d`",
			escapeRenderBlockMarkdown(display),
			block.Line,
		)
	}
	rng := renderBlockHoverRange(reference.BlockRange, request.LineIndex)
	return &protocol.Hover{
		Contents: protocol.MarkupContent{
			Kind:  protocol.Markdown,
			Value: markdown.String(),
		},
		Range: &rng,
	}, nil
}

func renderBlockHoverRange(
	rng cst.TextRange,
	lineIndex *cst.LineIndex,
) protocol.Range {
	startLine, startCharacter := lineIndex.PositionUTF16(rng.Start)
	endLine, endCharacter := lineIndex.PositionUTF16(rng.End)
	return protocol.Range{
		Start: protocol.Position{
			Line:      int(startLine),
			Character: int(startCharacter),
		},
		End: protocol.Position{
			Line:      int(endLine),
			Character: int(endCharacter),
		},
	}
}

func escapeRenderBlockMarkdown(value string) string {
	return strings.ReplaceAll(value, "`", "\\`")
}
