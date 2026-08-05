package completion

import (
	"context"
	"path/filepath"
	"strings"

	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	"github.com/shopware/shopware-lsp/internal/parser/cst"
	"github.com/shopware/shopware-lsp/internal/stimulus"
)

type StimulusCompletionProvider struct {
	index *stimulus.Index
}

func NewStimulusCompletionProvider(
	index *stimulus.Index,
) *StimulusCompletionProvider {
	return &StimulusCompletionProvider{index: index}
}

func (p *StimulusCompletionProvider) GetCompletions(
	_ context.Context,
	request *lsp.CompletionRequest,
) []protocol.CompletionItem {
	if p == nil || p.index == nil || request == nil ||
		request.Root == nil || request.Node == nil ||
		request.LineIndex == nil {
		return nil
	}
	extension := strings.ToLower(filepath.Ext(request.TextDocument.URI))
	if extension != ".twig" && extension != ".html" {
		return nil
	}
	offset := request.LineIndex.OffsetUTF16(
		uint32(request.Position.Line),
		uint32(request.Position.Character),
	)
	reference, found := stimulus.ReferenceAt(
		request.Root,
		request.Node,
		offset,
	)
	if !found {
		return nil
	}
	controllers, err := p.index.Controllers()
	if err != nil {
		return nil
	}
	items := make([]protocol.CompletionItem, 0, len(controllers))
	for _, controller := range controllers {
		label := controller.Name
		if reference.Twig {
			label = controller.TwigName()
		}
		items = append(items, protocol.CompletionItem{
			Label:  label,
			Kind:   int(protocol.ModuleCompletion),
			Detail: controller.Source.String(),
			TextEdit: protocol.TextEdit{
				Range:   stimulusCompletionRange(reference.Range, request.LineIndex),
				NewText: label,
			},
		})
	}
	return items
}

func (p *StimulusCompletionProvider) GetTriggerCharacters() []string {
	return []string{"\"", "'", "-", "/", "@"}
}

func stimulusCompletionRange(
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
