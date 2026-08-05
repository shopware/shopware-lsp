package hover

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	"github.com/shopware/shopware-lsp/internal/parser/cst"
	"github.com/shopware/shopware-lsp/internal/stimulus"
)

type StimulusHoverProvider struct {
	root  string
	index *stimulus.Index
}

func NewStimulusHoverProvider(
	root string,
	index *stimulus.Index,
) *StimulusHoverProvider {
	return &StimulusHoverProvider{root: root, index: index}
}

func (p *StimulusHoverProvider) GetHover(
	_ context.Context,
	request *lsp.HoverRequest,
) (*protocol.Hover, error) {
	if p == nil || p.index == nil || request == nil ||
		request.Root == nil || request.Node == nil ||
		request.LineIndex == nil {
		return nil, nil
	}
	extension := strings.ToLower(filepath.Ext(request.TextDocument.URI))
	if extension != ".twig" && extension != ".html" {
		return nil, nil
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
	if !found || reference.Name == "" {
		return nil, nil
	}
	controllers, err := p.index.Find(reference.Name)
	if err != nil || len(controllers) == 0 {
		return nil, err
	}
	var markdown strings.Builder
	fmt.Fprintf(
		&markdown,
		"**Stimulus controller** `%s`",
		escapeStimulusMarkdown(reference.Name),
	)
	for _, controller := range controllers {
		display := controller.File
		if relative, pathErr := filepath.Rel(p.root, display); pathErr == nil {
			display = filepath.ToSlash(relative)
		}
		fmt.Fprintf(
			&markdown,
			"\n\n- %s · `%s`",
			controller.Source.String(),
			escapeStimulusMarkdown(display),
		)
		if controller.OriginalName != "" &&
			!strings.EqualFold(reference.Name, controller.OriginalName) {
			fmt.Fprintf(
				&markdown,
				" · Twig `%s`",
				escapeStimulusMarkdown(controller.OriginalName),
			)
		}
	}
	hoverRange := stimulusHoverRange(reference.Range, request)
	return &protocol.Hover{
		Contents: protocol.MarkupContent{
			Kind:  protocol.Markdown,
			Value: markdown.String(),
		},
		Range: &hoverRange,
	}, nil
}

func stimulusHoverRange(
	rng cst.TextRange,
	request *lsp.HoverRequest,
) protocol.Range {
	startLine, startCharacter := request.LineIndex.PositionUTF16(rng.Start)
	endLine, endCharacter := request.LineIndex.PositionUTF16(rng.End)
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

func escapeStimulusMarkdown(value string) string {
	return strings.ReplaceAll(value, "`", "\\`")
}
