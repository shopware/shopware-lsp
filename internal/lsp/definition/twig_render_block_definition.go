package definition

import (
	"context"
	"os"
	"path/filepath"
	"strings"

	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	"github.com/shopware/shopware-lsp/internal/parser/cst"
	"github.com/shopware/shopware-lsp/internal/php"
	"github.com/shopware/shopware-lsp/internal/twig"
	"github.com/shopware/shopware-lsp/internal/uriutil"
)

type TwigRenderBlockDefinitionProvider struct {
	twigIndex *twig.TwigIndexer
	phpIndex  *php.PHPIndex
}

func NewTwigRenderBlockDefinitionProvider(
	twigIndex *twig.TwigIndexer,
	phpIndex *php.PHPIndex,
) *TwigRenderBlockDefinitionProvider {
	return &TwigRenderBlockDefinitionProvider{
		twigIndex: twigIndex,
		phpIndex:  phpIndex,
	}
}

func (p *TwigRenderBlockDefinitionProvider) GetDefinition(
	ctx context.Context,
	request *lsp.DefinitionRequest,
) []protocol.Location {
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
	if !found || reference.Block == "" ||
		!twig.ValidateRenderBlockReference(
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
	var result []protocol.Location
	for _, block := range blocks {
		if !strings.EqualFold(block.Name, reference.Block) {
			continue
		}
		source, readErr := os.ReadFile(block.FilePath)
		if readErr != nil {
			continue
		}
		result = append(result, protocol.Location{
			URI: uriutil.FileURI(block.FilePath),
			Range: renderBlockProtocolRange(
				block.Range,
				cst.NewLineIndex(string(source)),
			),
		})
	}
	return result
}

func renderBlockProtocolRange(
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
