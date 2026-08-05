package reference

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	"github.com/shopware/shopware-lsp/internal/parser/cst"
	"github.com/shopware/shopware-lsp/internal/twig"
	"github.com/shopware/shopware-lsp/internal/uriutil"
)

type TwigMacroReferenceProvider struct {
	index *twig.TwigIndexer
}

func NewTwigMacroReferenceProvider(
	index *twig.TwigIndexer,
) *TwigMacroReferenceProvider {
	return &TwigMacroReferenceProvider{index: index}
}

func (p *TwigMacroReferenceProvider) GetReferences(
	_ context.Context,
	request *lsp.ReferenceRequest,
) ([]protocol.Location, error) {
	if p == nil || p.index == nil || request == nil ||
		request.Node == nil || request.Root == nil ||
		request.LineIndex == nil ||
		strings.ToLower(filepath.Ext(request.TextDocument.URI)) != ".twig" {
		return nil, nil
	}
	offset := request.LineIndex.OffsetUTF16(
		uint32(request.Position.Line),
		uint32(request.Position.Character),
	)
	currentPath, _ := uriutil.Path(request.TextDocument.URI)
	current, found := twig.MacroReferenceAt(
		currentPath,
		request.Root,
		request.Node,
		offset,
	)
	if !found {
		return nil, nil
	}
	var locations []protocol.Location
	if request.Context.IncludeDeclaration {
		for _, template := range current.Templates {
			macros, err := p.index.FindMacro(template, current.Name)
			if err != nil {
				return nil, err
			}
			for _, macro := range macros {
				if macro.FilePath == currentPath {
					continue
				}
				if location, ok := twigMacroFileLocation(
					macro.FilePath,
					macro.NameRange,
				); ok {
					locations = append(locations, location)
				}
			}
		}
	}
	for _, template := range current.Templates {
		usages, err := p.index.GetMacroUsages(template, current.Name)
		if err != nil {
			return nil, err
		}
		for _, usage := range usages {
			if usage.FilePath == currentPath {
				continue
			}
			if location, ok := twigMacroFileLocation(
				usage.FilePath,
				usage.Range,
			); ok {
				locations = append(locations, location)
			}
		}
	}
	for _, reference := range twig.MacroReferencesInDocument(
		currentPath,
		request.Root,
	) {
		if !sameMacroReference(current, reference) ||
			reference.Role == twig.MacroDeclarationReference &&
				!request.Context.IncludeDeclaration {
			continue
		}
		locations = append(locations, protocol.Location{
			URI: request.TextDocument.URI,
			Range: twigMacroReferenceRange(
				reference.Range,
				request.LineIndex,
			),
		})
	}
	return uniqueTwigMacroLocations(locations), nil
}

func sameMacroReference(
	left,
	right twig.MacroReference,
) bool {
	if !strings.EqualFold(left.Name, right.Name) {
		return false
	}
	for _, leftTemplate := range left.Templates {
		for _, rightTemplate := range right.Templates {
			if leftTemplate == rightTemplate {
				return true
			}
		}
	}
	return false
}

func twigMacroFileLocation(
	path string,
	rng cst.TextRange,
) (protocol.Location, bool) {
	source, err := os.ReadFile(path)
	if err != nil {
		return protocol.Location{}, false
	}
	return protocol.Location{
		URI: uriutil.FileURI(path),
		Range: twigMacroReferenceRange(
			rng,
			cst.NewLineIndex(string(source)),
		),
	}, true
}

func twigMacroReferenceRange(
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

func uniqueTwigMacroLocations(
	locations []protocol.Location,
) []protocol.Location {
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
