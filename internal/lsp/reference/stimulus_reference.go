package reference

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	"github.com/shopware/shopware-lsp/internal/parser/cst"
	"github.com/shopware/shopware-lsp/internal/stimulus"
	"github.com/shopware/shopware-lsp/internal/uriutil"
)

type StimulusReferenceProvider struct {
	index *stimulus.Index
}

func NewStimulusReferenceProvider(
	index *stimulus.Index,
) *StimulusReferenceProvider {
	return &StimulusReferenceProvider{index: index}
}

func (p *StimulusReferenceProvider) GetReferences(
	_ context.Context,
	request *lsp.ReferenceRequest,
) ([]protocol.Location, error) {
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
	current, found := stimulus.ReferenceAt(
		request.Root,
		request.Node,
		offset,
	)
	if !found || current.Name == "" {
		return nil, nil
	}
	currentPath, _ := uriutil.Path(request.TextDocument.URI)
	usages, err := p.index.Usages(current.Name)
	if err != nil {
		return nil, err
	}
	filtered := make([]stimulus.Usage, 0, len(usages))
	for _, usage := range usages {
		if usage.File != currentPath {
			filtered = append(filtered, usage)
		}
	}
	for _, reference := range stimulus.References(
		currentPath,
		request.Root,
	) {
		if strings.EqualFold(
			stimulus.NormalizeName(reference.Name),
			stimulus.NormalizeName(current.Name),
		) {
			filtered = append(filtered, stimulus.Usage{
				Name:  stimulus.NormalizeName(reference.Name),
				File:  currentPath,
				Range: reference.Range,
			})
		}
	}

	var result []protocol.Location
	seen := make(map[string]struct{})
	if request.Context.IncludeDeclaration {
		controllers, findErr := p.index.Find(current.Name)
		if findErr != nil {
			return nil, findErr
		}
		for _, controller := range controllers {
			location, locationFound := stimulusFileRangeLocation(
				controller.File,
				controller.Range,
				"",
				nil,
			)
			if !locationFound && controller.Range.Len() == 0 {
				location = protocol.Location{
					URI: uriutil.FileURI(controller.File),
				}
				locationFound = true
			}
			if locationFound {
				addStimulusLocation(&result, seen, location)
			}
		}
	}
	for _, usage := range filtered {
		source := ""
		lineIndex := (*cst.LineIndex)(nil)
		if usage.File == currentPath {
			source = request.SourceString()
			lineIndex = request.LineIndex
		}
		location, locationFound := stimulusFileRangeLocation(
			usage.File,
			usage.Range,
			source,
			lineIndex,
		)
		if locationFound {
			addStimulusLocation(&result, seen, location)
		}
	}
	return result, nil
}

func stimulusFileRangeLocation(
	path string,
	rng cst.TextRange,
	source string,
	lineIndex *cst.LineIndex,
) (protocol.Location, bool) {
	if path == "" {
		return protocol.Location{}, false
	}
	if lineIndex == nil {
		if source == "" {
			data, err := os.ReadFile(path)
			if err != nil {
				return protocol.Location{}, false
			}
			source = string(data)
		}
		lineIndex = cst.NewLineIndex(source)
	}
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

func addStimulusLocation(
	result *[]protocol.Location,
	seen map[string]struct{},
	location protocol.Location,
) {
	key := location.URI + ":" +
		strconv.Itoa(location.Range.Start.Line) + ":" +
		strconv.Itoa(location.Range.Start.Character)
	if _, duplicate := seen[key]; duplicate {
		return
	}
	seen[key] = struct{}{}
	*result = append(*result, location)
}
