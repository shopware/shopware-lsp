package definition

import (
	"context"
	"fmt"
	"os"
	"sort"

	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	"github.com/shopware/shopware-lsp/internal/parser/cst"
	"github.com/shopware/shopware-lsp/internal/symfonyconfig"
	"github.com/shopware/shopware-lsp/internal/uriutil"
)

type SymfonyConfigDefinitionProvider struct {
	index *symfonyconfig.Index
}

func NewSymfonyConfigDefinitionProvider(
	index *symfonyconfig.Index,
) *SymfonyConfigDefinitionProvider {
	return &SymfonyConfigDefinitionProvider{index: index}
}

func (p *SymfonyConfigDefinitionProvider) GetDefinition(
	_ context.Context,
	request *lsp.DefinitionRequest,
) []protocol.Location {
	if p == nil || p.index == nil || request == nil ||
		request.Node == nil {
		return nil
	}
	if reference, found := symfonyconfig.RootReferenceAt(
		request.Node,
	); found {
		return p.rootDefinitions(request, reference)
	}
	if reference, found := symfonyconfig.ResourceReferenceAt(
		request.Node,
	); found {
		return resourceDefinitions(request, reference)
	}
	return nil
}

func (p *SymfonyConfigDefinitionProvider) rootDefinitions(
	request *lsp.DefinitionRequest,
	reference symfonyconfig.RootReference,
) []protocol.Location {
	roots, err := p.index.Roots(reference.Name)
	if err != nil {
		return nil
	}
	currentPath, _ := uriutil.Path(request.TextDocument.URI)
	var result []protocol.Location
	for _, root := range roots {
		if root.File == currentPath && request.LineIndex != nil {
			result = append(result, protocol.Location{
				URI: request.TextDocument.URI,
				Range: symfonyConfigDefinitionRange(
					root.Range,
					request.LineIndex,
				),
			})
			continue
		}
		source, readErr := os.ReadFile(root.File)
		if readErr != nil {
			continue
		}
		result = append(result, protocol.Location{
			URI: uriutil.FileURI(root.File),
			Range: symfonyConfigDefinitionRange(
				root.Range,
				cst.NewLineIndex(string(source)),
			),
		})
	}
	return uniqueSymfonyConfigLocations(result)
}

func resourceDefinitions(
	request *lsp.DefinitionRequest,
	reference symfonyconfig.ResourceReference,
) []protocol.Location {
	currentPath, err := uriutil.Path(request.TextDocument.URI)
	if err != nil {
		return nil
	}
	var result []protocol.Location
	for _, path := range symfonyconfig.ResourceFiles(
		currentPath,
		reference.Path,
	) {
		result = append(result, protocol.Location{
			URI:   uriutil.FileURI(path),
			Range: protocol.Range{},
		})
	}
	return uniqueSymfonyConfigLocations(result)
}

func symfonyConfigDefinitionRange(
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

func uniqueSymfonyConfigLocations(
	locations []protocol.Location,
) []protocol.Location {
	sort.Slice(locations, func(left, right int) bool {
		if locations[left].URI != locations[right].URI {
			return locations[left].URI < locations[right].URI
		}
		if locations[left].Range.Start.Line !=
			locations[right].Range.Start.Line {
			return locations[left].Range.Start.Line <
				locations[right].Range.Start.Line
		}
		return locations[left].Range.Start.Character <
			locations[right].Range.Start.Character
	})
	seen := make(map[string]struct{}, len(locations))
	result := make([]protocol.Location, 0, len(locations))
	for _, location := range locations {
		key := fmt.Sprintf(
			"%s:%d:%d:%d:%d",
			location.URI,
			location.Range.Start.Line,
			location.Range.Start.Character,
			location.Range.End.Line,
			location.Range.End.Character,
		)
		if _, duplicate := seen[key]; duplicate {
			continue
		}
		seen[key] = struct{}{}
		result = append(result, location)
	}
	return result
}
