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
	phpsyntax "github.com/shopware/shopware-lsp/internal/parser/php/syntax"
	twigsyntax "github.com/shopware/shopware-lsp/internal/parser/twig/syntax"
	"github.com/shopware/shopware-lsp/internal/twig"
	"github.com/shopware/shopware-lsp/internal/uriutil"
)

type TwigTemplateReferenceProvider struct {
	index *twig.TwigIndexer
}

func NewTwigTemplateReferenceProvider(
	index *twig.TwigIndexer,
) *TwigTemplateReferenceProvider {
	return &TwigTemplateReferenceProvider{index: index}
}

func (p *TwigTemplateReferenceProvider) GetReferences(
	_ context.Context,
	request *lsp.ReferenceRequest,
) ([]protocol.Location, error) {
	if p == nil || p.index == nil || request == nil ||
		request.Root == nil || request.LineIndex == nil {
		return nil, nil
	}
	path, err := uriutil.Path(request.TextDocument.URI)
	if err != nil {
		return nil, nil
	}
	currentReferences := templateReferencesInDocument(path, request.Root)
	offset := request.LineIndex.OffsetUTF16(
		uint32(request.Position.Line),
		uint32(request.Position.Character),
	)
	current, found := twig.TemplateReferenceAt(currentReferences, offset)
	if !found {
		return nil, nil
	}

	targetNames := []string{current.Template}
	files, fileErr := p.index.GetTwigFilesByRelPath(current.Template)
	if fileErr != nil {
		return nil, fileErr
	}
	uniqueTargets := make(map[string]struct{})
	for _, file := range files {
		uniqueTargets[filepath.Clean(file.Path)] = struct{}{}
	}
	if len(uniqueTargets) == 1 {
		for path := range uniqueTargets {
			targetNames = twig.TemplateNames(path)
		}
	}
	references, err := p.index.GetTemplateReferences(targetNames...)
	if err != nil {
		return nil, err
	}
	var locations []protocol.Location
	for _, reference := range references {
		if reference.FilePath == path {
			continue
		}
		if location, ok := templateReferenceFileLocation(reference); ok {
			locations = append(locations, location)
		}
	}
	for _, reference := range currentReferences {
		if !templateNameMatchesAny(reference.Template, targetNames) {
			continue
		}
		locations = append(locations, protocol.Location{
			URI: request.TextDocument.URI,
			Range: templateReferenceProtocolRange(
				reference.Range,
				request.LineIndex,
			),
		})
	}
	if request.Context.IncludeDeclaration {
		for _, file := range files {
			locations = append(locations, protocol.Location{
				URI:   uriutil.FileURI(file.Path),
				Range: protocol.Range{},
			})
		}
	}
	return uniqueTemplateReferenceLocations(locations), nil
}

func templateReferencesInDocument(
	path string,
	root *cst.Node,
) []twig.TemplateReference {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".twig":
		return twig.TwigTemplateReferences(path, (*twigsyntax.Node)(root))
	case ".php":
		return twig.PHPTemplateReferences(path, (*phpsyntax.Node)(root))
	default:
		return nil
	}
}

func templateReferenceFileLocation(
	reference twig.TemplateReference,
) (protocol.Location, bool) {
	source, err := os.ReadFile(reference.FilePath)
	if err != nil {
		return protocol.Location{}, false
	}
	return protocol.Location{
		URI: uriutil.FileURI(reference.FilePath),
		Range: templateReferenceProtocolRange(
			reference.Range,
			cst.NewLineIndex(string(source)),
		),
	}, true
}

func templateReferenceProtocolRange(
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

func sameTemplateName(left, right string) bool {
	return strings.TrimPrefix(filepath.ToSlash(strings.TrimSpace(left)), "/") ==
		strings.TrimPrefix(filepath.ToSlash(strings.TrimSpace(right)), "/")
}

func templateNameMatchesAny(template string, names []string) bool {
	for _, name := range names {
		if sameTemplateName(template, name) {
			return true
		}
	}
	return false
}

func uniqueTemplateReferenceLocations(
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
