package reference

import (
	"context"
	"fmt"
	"os"

	"github.com/shopware/shopware-lsp/internal/environment"
	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	"github.com/shopware/shopware-lsp/internal/parser/cst"
	"github.com/shopware/shopware-lsp/internal/uriutil"
)

type EnvironmentReferenceProvider struct {
	index *environment.Index
}

func NewEnvironmentReferenceProvider(
	index *environment.Index,
) *EnvironmentReferenceProvider {
	return &EnvironmentReferenceProvider{index: index}
}

func (p *EnvironmentReferenceProvider) GetReferences(
	_ context.Context,
	request *lsp.ReferenceRequest,
) ([]protocol.Location, error) {
	if p == nil || p.index == nil || request == nil ||
		request.ReferenceParams == nil || request.LineIndex == nil {
		return nil, nil
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
		return nil, nil
	}
	variable, found, err := p.index.Variable(occurrence.Name)
	if err != nil || !found {
		return nil, err
	}
	occurrences := variable.References
	if request.Context.IncludeDeclaration {
		occurrences = append(
			append([]environment.Occurrence(nil), variable.Declarations...),
			occurrences...,
		)
	}
	result := make([]protocol.Location, 0, len(occurrences))
	seen := make(map[string]struct{}, len(occurrences))
	for _, current := range occurrences {
		location, exists := environmentReferenceLocation(
			current.File,
			current.NameRange,
		)
		if !exists {
			continue
		}
		key := fmt.Sprintf(
			"%s:%d:%d",
			location.URI,
			location.Range.Start.Line,
			location.Range.Start.Character,
		)
		if _, duplicate := seen[key]; duplicate {
			continue
		}
		seen[key] = struct{}{}
		result = append(result, location)
	}
	return result, nil
}

func environmentReferenceLocation(
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
