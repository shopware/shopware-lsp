package reference

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	"github.com/shopware/shopware-lsp/internal/parser/cst"
	phpsyntax "github.com/shopware/shopware-lsp/internal/parser/php/syntax"
	"github.com/shopware/shopware-lsp/internal/php"
	"github.com/shopware/shopware-lsp/internal/php/semantic"
	"github.com/shopware/shopware-lsp/internal/twig"
	"github.com/shopware/shopware-lsp/internal/uriutil"
)

// TwigPHPReferenceProvider adds typed Twig member and direct class-name usages
// to Find References for PHP declarations.
type TwigPHPReferenceProvider struct {
	index    *twig.TwigIndexer
	phpIndex *php.PHPIndex
}

func NewTwigPHPReferenceProvider(
	index *twig.TwigIndexer,
	phpIndex *php.PHPIndex,
) *TwigPHPReferenceProvider {
	return &TwigPHPReferenceProvider{
		index:    index,
		phpIndex: phpIndex,
	}
}

func (p *TwigPHPReferenceProvider) GetReferences(
	ctx context.Context,
	request *lsp.ReferenceRequest,
) ([]protocol.Location, error) {
	if p == nil || p.index == nil || p.phpIndex == nil ||
		request == nil || request.ReferenceParams == nil ||
		request.Document == nil || request.Root == nil ||
		request.LineIndex == nil {
		return nil, nil
	}
	path, err := uriutil.Path(request.TextDocument.URI)
	if err != nil {
		return nil, nil
	}
	offset := request.LineIndex.OffsetUTF16(
		uint32(request.Position.Line),
		uint32(request.Position.Character),
	)
	switch strings.ToLower(filepath.Ext(path)) {
	case ".twig":
		return p.twigExtensionReferences(
			request,
			path,
			offset,
		)
	case ".php":
		return p.phpReferences(ctx, request, path, offset)
	default:
		return nil, nil
	}
}

func (p *TwigPHPReferenceProvider) phpReferences(
	ctx context.Context,
	request *lsp.ReferenceRequest,
	path string,
	offset uint32,
) ([]protocol.Location, error) {
	document, snapshot := p.phpSemanticState(ctx, request, path)
	if document == nil || snapshot == nil {
		return nil, nil
	}
	symbol, found := php.SymbolAt(document, snapshot, offset)
	if !found {
		return nil, nil
	}
	target, found := twig.PHPUsageTargetForSymbol(snapshot, symbol)
	if !found {
		return nil, nil
	}
	references, err := p.index.GetPHPUsageReferences(target)
	if err != nil {
		return nil, err
	}
	locations := make([]protocol.Location, 0, len(references))
	for _, reference := range references {
		location, found := twigPHPUsageLocation(reference)
		if found {
			locations = append(locations, location)
		}
	}
	extensionUsages, err := p.index.GetExtensionUsagesForPHPSymbol(
		p.phpIndex,
		symbol,
	)
	if err != nil {
		return nil, err
	}
	for _, usage := range extensionUsages {
		location, found := twigExtensionUsageLocation(
			usage,
			"",
			nil,
		)
		if found {
			locations = append(locations, location)
		}
	}
	return uniqueTwigPHPUsageLocations(locations), nil
}

func (p *TwigPHPReferenceProvider) twigExtensionReferences(
	request *lsp.ReferenceRequest,
	path string,
	offset uint32,
) ([]protocol.Location, error) {
	target, found := twig.ExtensionUsageAt(
		path,
		request.Root,
		offset,
	)
	if !found {
		return nil, nil
	}
	indexed, err := p.index.GetExtensionUsages(
		target.Kind,
		target.Name,
	)
	if err != nil {
		return nil, err
	}
	current := twig.ExtensionUsagesInDocument(path, request.Root)
	var locations []protocol.Location
	for _, usage := range indexed {
		if filepath.Clean(usage.FilePath) == filepath.Clean(path) {
			continue
		}
		location, found := twigExtensionUsageLocation(
			usage,
			"",
			nil,
		)
		if found {
			locations = append(locations, location)
		}
	}
	for _, usage := range current {
		if usage.Kind != target.Kind ||
			!strings.EqualFold(usage.Name, target.Name) {
			continue
		}
		location, found := twigExtensionUsageLocation(
			usage,
			path,
			request.LineIndex,
		)
		if found {
			locations = append(locations, location)
		}
	}
	return uniqueTwigPHPUsageLocations(locations), nil
}

func (p *TwigPHPReferenceProvider) phpSemanticState(
	ctx context.Context,
	request *lsp.ReferenceRequest,
	path string,
) (*semantic.Document, *semantic.Snapshot) {
	if phpContext := php.GetPHPContext(ctx); phpContext != nil &&
		phpContext.Document != nil && phpContext.Snapshot != nil {
		return phpContext.Document, phpContext.Snapshot
	}
	if document, found := p.phpIndex.SemanticDocument(path); found {
		return document, p.phpIndex.SemanticSnapshot()
	}
	document := p.phpIndex.AnalyzeDocument(
		path,
		request.Document.Version,
		(*phpsyntax.Node)(request.Root),
	)
	return document, p.phpIndex.SemanticSnapshot().WithDocument(document)
}

func twigPHPUsageLocation(
	reference twig.PHPUsageReference,
) (protocol.Location, bool) {
	content, err := os.ReadFile(reference.FilePath)
	if err != nil {
		return protocol.Location{}, false
	}
	lineIndex := cst.NewLineIndex(string(content))
	startLine, startCharacter := lineIndex.PositionUTF16(
		reference.Range.Start,
	)
	endLine, endCharacter := lineIndex.PositionUTF16(reference.Range.End)
	return protocol.Location{
		URI: uriutil.FileURI(reference.FilePath),
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

func twigExtensionUsageLocation(
	usage twig.ExtensionUsage,
	currentPath string,
	currentLineIndex *cst.LineIndex,
) (protocol.Location, bool) {
	lineIndex := currentLineIndex
	if lineIndex == nil ||
		filepath.Clean(usage.FilePath) != filepath.Clean(currentPath) {
		content, err := os.ReadFile(usage.FilePath)
		if err != nil {
			return protocol.Location{}, false
		}
		lineIndex = cst.NewLineIndex(string(content))
	}
	startLine, startCharacter := lineIndex.PositionUTF16(usage.Range.Start)
	endLine, endCharacter := lineIndex.PositionUTF16(usage.Range.End)
	return protocol.Location{
		URI: uriutil.FileURI(usage.FilePath),
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

func uniqueTwigPHPUsageLocations(
	locations []protocol.Location,
) []protocol.Location {
	seen := make(map[string]struct{}, len(locations))
	result := make([]protocol.Location, 0, len(locations))
	for _, location := range locations {
		key := fmt.Sprintf(
			"%s\x00%d:%d:%d:%d",
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
	return result
}
