package reference

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

type StyleClassReferenceProvider struct {
	index *style.Index
}

func NewStyleClassReferenceProvider(
	index *style.Index,
) *StyleClassReferenceProvider {
	return &StyleClassReferenceProvider{index: index}
}

func (p *StyleClassReferenceProvider) GetReferences(
	_ context.Context,
	request *lsp.ReferenceRequest,
) ([]protocol.Location, error) {
	if p == nil || p.index == nil || request == nil ||
		request.Root == nil || request.LineIndex == nil ||
		request.ReferenceParams == nil {
		return nil, nil
	}
	path, err := uriutil.Path(request.TextDocument.URI)
	if err != nil || !styleReferenceDocument(path) {
		return nil, nil
	}
	offset := request.LineIndex.OffsetUTF16(
		uint32(request.Position.Line),
		uint32(request.Position.Character),
	)
	current, found := style.ClassAt(path, request.Root, offset)
	if !found || current.Name == "" {
		return nil, nil
	}

	occurrences, err := p.index.Usages(current.Name)
	if err != nil {
		return nil, err
	}
	occurrences = withoutStylePath(occurrences, path)
	for _, usage := range style.ClassUsages(path, request.Root) {
		if usage.Name == current.Name {
			occurrences = append(occurrences, usage)
		}
	}
	if request.Context.IncludeDeclaration {
		declarations, declarationErr := p.index.Declarations(current.Name)
		if declarationErr != nil {
			return nil, declarationErr
		}
		occurrences = append(
			occurrences,
			withoutStylePath(declarations, path)...,
		)
		for _, declaration := range style.ClassDeclarations(path, request.Root) {
			if declaration.Name == current.Name {
				occurrences = append(occurrences, declaration)
			}
		}
	}

	result := make([]protocol.Location, 0, len(occurrences))
	seen := make(map[string]struct{}, len(occurrences))
	for _, occurrence := range occurrences {
		location := styleReferenceLocation(
			occurrence,
			path,
			request.LineIndex,
		)
		key := styleReferenceLocationKey(location)
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
		if result[left].Range.Start.Line != result[right].Range.Start.Line {
			return result[left].Range.Start.Line <
				result[right].Range.Start.Line
		}
		return result[left].Range.Start.Character <
			result[right].Range.Start.Character
	})
	return result, nil
}

func withoutStylePath(
	occurrences []style.ClassOccurrence,
	path string,
) []style.ClassOccurrence {
	result := make([]style.ClassOccurrence, 0, len(occurrences))
	for _, occurrence := range occurrences {
		if occurrence.File != path {
			result = append(result, occurrence)
		}
	}
	return result
}

func styleReferenceLocation(
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

func styleReferenceDocument(path string) bool {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".scss", ".twig", ".html", ".vue":
		return true
	default:
		return false
	}
}

func styleReferenceLocationKey(location protocol.Location) string {
	return location.URI + ":" +
		strconv.Itoa(location.Range.Start.Line) + ":" +
		strconv.Itoa(location.Range.Start.Character)
}
