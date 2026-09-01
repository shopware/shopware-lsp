package definition

import (
	"context"
	"os"

	"github.com/shopware/shopware-lsp/internal/environment"
	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	"github.com/shopware/shopware-lsp/internal/parser/cst"
	"github.com/shopware/shopware-lsp/internal/uriutil"
)

type EnvironmentDefinitionProvider struct {
	index *environment.Index
}

func NewEnvironmentDefinitionProvider(
	index *environment.Index,
) *EnvironmentDefinitionProvider {
	return &EnvironmentDefinitionProvider{index: index}
}

func (p *EnvironmentDefinitionProvider) GetDefinition(
	_ context.Context,
	request *lsp.DefinitionRequest,
) []protocol.Location {
	if p == nil || p.index == nil || request == nil ||
		request.DefinitionParams == nil || request.LineIndex == nil {
		return nil
	}
	offset := request.LineIndex.OffsetUTF16(
		uint32(request.Position.Line),
		uint32(request.Position.Character),
	)
	path, _ := uriutil.Path(request.TextDocument.URI)
	occurrence, found := environment.PHPOccurrenceAt(
		request.Node,
		offset,
	)
	if !found {
		occurrence, found = environment.OccurrenceAt(
			path,
			request.SourceString(),
			offset,
		)
	}
	if !found {
		return nil
	}
	variable, found, err := p.index.Variable(occurrence.Name)
	if err != nil || !found {
		return nil
	}
	result := make([]protocol.Location, 0, len(variable.Declarations))
	for _, declaration := range variable.Declarations {
		if location, exists := environmentDefinitionLocation(
			declaration.File,
			declaration.NameRange,
		); exists {
			result = append(result, location)
		}
	}
	return result
}

func environmentDefinitionLocation(
	path string,
	rng cst.TextRange,
) (protocol.Location, bool) {
	source, err := os.ReadFile(path)
	if err != nil {
		return protocol.Location{}, false
	}
	lineIndex := cst.NewLineIndex(string(source))
	startLine, startCharacter := lineIndex.PositionUTF16(rng.Start)
	endLine, endCharacter := lineIndex.PositionUTF16(rng.End)
	return protocol.Location{
		URI: uriutil.FileURI(path),
		Range: protocol.Range{
			Start: protocol.Position{
				Line:      int(startLine),
				Character: int(startCharacter),
			},
			End: protocol.Position{
				Line:      int(endLine),
				Character: int(endCharacter),
			},
		},
	}, true
}
