package definition

import (
	"context"
	"os"
	"path/filepath"
	"strings"

	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	"github.com/shopware/shopware-lsp/internal/parser/cst"
	"github.com/shopware/shopware-lsp/internal/stimulus"
	"github.com/shopware/shopware-lsp/internal/uriutil"
)

type StimulusDefinitionProvider struct {
	index *stimulus.Index
}

func NewStimulusDefinitionProvider(
	index *stimulus.Index,
) *StimulusDefinitionProvider {
	return &StimulusDefinitionProvider{index: index}
}

func (p *StimulusDefinitionProvider) GetDefinition(
	_ context.Context,
	request *lsp.DefinitionRequest,
) []protocol.Location {
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
	if !found || reference.Name == "" {
		return nil
	}
	controllers, err := p.index.Find(reference.Name)
	if err != nil {
		return nil
	}
	result := make([]protocol.Location, 0, len(controllers))
	for _, controller := range controllers {
		result = append(result, stimulusControllerLocation(controller))
	}
	return result
}

func stimulusControllerLocation(
	controller stimulus.Controller,
) protocol.Location {
	location := protocol.Location{
		URI: uriutil.FileURI(controller.File),
	}
	if controller.Range.Len() == 0 {
		return location
	}
	source, err := os.ReadFile(controller.File)
	if err != nil {
		return location
	}
	location.Range = stimulusDefinitionRange(
		controller.Range,
		cst.NewLineIndex(string(source)),
	)
	return location
}

func stimulusDefinitionRange(
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
