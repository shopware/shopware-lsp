package definition

import (
	"context"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	"github.com/shopware/shopware-lsp/internal/parser/cst"
	"github.com/shopware/shopware-lsp/internal/style"
	"github.com/shopware/shopware-lsp/internal/uriutil"
)

type StyleClassDefinitionProvider struct {
	index *style.Index
}

func NewStyleClassDefinitionProvider(
	index *style.Index,
) *StyleClassDefinitionProvider {
	return &StyleClassDefinitionProvider{index: index}
}

func (p *StyleClassDefinitionProvider) GetDefinition(
	_ context.Context,
	request *lsp.DefinitionRequest,
) []protocol.Location {
	if p == nil || p.index == nil || request == nil ||
		request.Root == nil || request.LineIndex == nil ||
		request.DefinitionParams == nil {
		return nil
	}
	path, err := uriutil.Path(request.TextDocument.URI)
	if err != nil || !styleClassDocument(path) {
		return nil
	}
	offset := request.LineIndex.OffsetUTF16(
		uint32(request.Position.Line),
		uint32(request.Position.Character),
	)
	current, found := style.ClassAt(path, request.Root, offset)
	if !found || current.Kind != style.ClassUsage || current.Name == "" {
		return nil
	}
	declarations, err := p.index.Declarations(current.Name)
	if err != nil {
		return nil
	}
	liveDeclarations := style.ClassDeclarations(path, request.Root)
	filtered := declarations[:0]
	for _, declaration := range declarations {
		if declaration.File != path {
			filtered = append(filtered, declaration)
		}
	}
	declarations = filtered
	for _, declaration := range liveDeclarations {
		if declaration.Name == current.Name {
			declarations = append(declarations, declaration)
		}
	}

	result := make([]protocol.Location, 0, len(declarations))
	seen := make(map[string]struct{}, len(declarations))
	for _, declaration := range declarations {
		location := styleDefinitionLocation(
			declaration,
			path,
			request.LineIndex,
		)
		key := styleLocationKey(location)
		if _, duplicate := seen[key]; duplicate {
			continue
		}
		seen[key] = struct{}{}
		result = append(result, location)
	}
	sort.Slice(result, func(left, right int) bool {
		if result[left].URI != result[right].URI {
			return result[left].URI < result[right].URI
		}
		return result[left].Range.Start.Line <
			result[right].Range.Start.Line ||
			result[left].Range.Start.Line == result[right].Range.Start.Line &&
				result[left].Range.Start.Character <
					result[right].Range.Start.Character
	})
	return result
}

func styleDefinitionLocation(
	occurrence style.ClassOccurrence,
	currentPath string,
	currentLines *cst.LineIndex,
) protocol.Location {
	location := protocol.Location{URI: uriutil.FileURI(occurrence.File)}
	if occurrence.File == currentPath && currentLines != nil {
		startLine, startCharacter := currentLines.PositionUTF16(
			occurrence.Range.Start,
		)
		endLine, endCharacter := currentLines.PositionUTF16(
			occurrence.Range.End,
		)
		location.Range = protocol.Range{
			Start: protocol.Position{
				Line: int(startLine), Character: int(startCharacter),
			},
			End: protocol.Position{
				Line: int(endLine), Character: int(endCharacter),
			},
		}
		return location
	}
	location.Range = protocol.Range{
		Start: protocol.Position{
			Line: occurrence.Start.Line, Character: occurrence.Start.Character,
		},
		End: protocol.Position{
			Line: occurrence.End.Line, Character: occurrence.End.Character,
		},
	}
	return location
}

func styleClassDocument(path string) bool {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".scss", ".twig", ".html", ".vue":
		return true
	default:
		return false
	}
}

func styleLocationKey(location protocol.Location) string {
	return location.URI + ":" +
		strconv.Itoa(location.Range.Start.Line) + ":" +
		strconv.Itoa(location.Range.Start.Character)
}
