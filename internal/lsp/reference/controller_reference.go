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
	"github.com/shopware/shopware-lsp/internal/php"
	"github.com/shopware/shopware-lsp/internal/php/semantic"
	"github.com/shopware/shopware-lsp/internal/symfony"
	"github.com/shopware/shopware-lsp/internal/uriutil"
)

type ControllerReferenceProvider struct {
	usages   *symfony.RouteUsageIndexer
	services *symfony.ServiceIndex
	php      *php.PHPIndex
}

func NewControllerReferenceProvider(
	usages *symfony.RouteUsageIndexer,
	services *symfony.ServiceIndex,
	phpIndex *php.PHPIndex,
) *ControllerReferenceProvider {
	return &ControllerReferenceProvider{
		usages: usages, services: services, php: phpIndex,
	}
}

func (p *ControllerReferenceProvider) GetReferences(
	ctx context.Context,
	request *lsp.ReferenceRequest,
) ([]protocol.Location, error) {
	if p == nil || p.usages == nil || p.php == nil || request == nil ||
		request.Node == nil || request.LineIndex == nil {
		return nil, nil
	}
	switch strings.ToLower(filepath.Ext(request.TextDocument.URI)) {
	case ".twig":
		return p.twigReferences(ctx, request)
	case ".php":
		return p.phpReferences(ctx, request)
	default:
		return nil, nil
	}
}

func (p *ControllerReferenceProvider) twigReferences(
	ctx context.Context,
	request *lsp.ReferenceRequest,
) ([]protocol.Location, error) {
	reference, ok := symfony.TwigControllerReferenceAt(request.Node)
	if !ok {
		return nil, nil
	}
	resolution, err := symfony.ResolveControllerReference(
		reference.ControllerReference,
		p.services,
		p.php,
	)
	if err != nil {
		return nil, err
	}
	var usages []symfony.ControllerUsage
	if resolution.MethodDeclared {
		usages, err = p.usages.ControllerUsagesForMethod(
			resolution.Class.FullyQualified,
			resolution.Method.Name,
			p.services,
			p.php,
		)
	} else {
		usages, err = p.usages.GetControllerUsages(
			reference.ControllerReference,
		)
	}
	if err != nil {
		return nil, err
	}
	var result []protocol.Location
	for _, usage := range usages {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		result = append(result, controllerUsageLocation(usage))
	}
	// The open document may contain an unsaved usage not in the persistent
	// index yet.
	result = append(result, protocol.Location{
		URI:   request.TextDocument.URI,
		Range: controllerProtocolRange(reference.Range, request.LineIndex),
	})
	if request.Context.IncludeDeclaration {
		switch {
		case resolution.MethodDeclared:
			result = append(result, controllerSymbolLocation(resolution.Method))
		case resolution.ClassFound:
			result = append(result, controllerSymbolLocation(resolution.Class))
		}
	}
	return uniqueControllerLocations(result), nil
}

func (p *ControllerReferenceProvider) phpReferences(
	ctx context.Context,
	request *lsp.ReferenceRequest,
) ([]protocol.Location, error) {
	path, err := uriutil.Path(request.TextDocument.URI)
	if err != nil {
		return nil, err
	}
	offset := request.LineIndex.OffsetUTF16(
		uint32(request.Position.Line),
		uint32(request.Position.Character),
	)
	var document *semantic.Document
	var snapshot *semantic.Snapshot
	if phpContext := php.GetPHPContext(ctx); phpContext != nil {
		document, snapshot = phpContext.Document, phpContext.Snapshot
	}
	if document == nil || snapshot == nil {
		var found bool
		document, found = p.php.SemanticDocument(path)
		if !found {
			return nil, nil
		}
		snapshot = p.php.SemanticSnapshot()
	}
	method, found := php.SymbolAt(document, snapshot, offset)
	if !found || method.Kind != semantic.MethodSymbol {
		return nil, nil
	}
	class, found := snapshot.Symbol(method.Container)
	if !found || !class.IsClassLike() {
		return nil, nil
	}
	usages, err := p.usages.ControllerUsagesForMethod(
		class.FullyQualified,
		method.Name,
		p.services,
		p.php,
	)
	if err != nil {
		return nil, err
	}
	result := make([]protocol.Location, 0, len(usages)+1)
	for _, usage := range usages {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		result = append(result, controllerUsageLocation(usage))
	}
	if request.Context.IncludeDeclaration {
		result = append(result, controllerSymbolLocation(method))
	}
	return uniqueControllerLocations(result), nil
}

func controllerUsageLocation(usage symfony.ControllerUsage) protocol.Location {
	content, err := os.ReadFile(usage.File)
	if err != nil {
		return protocol.Location{URI: uriutil.FileURI(usage.File)}
	}
	return protocol.Location{
		URI: uriutil.FileURI(usage.File),
		Range: controllerProtocolRange(
			usage.Range,
			cst.NewLineIndex(string(content)),
		),
	}
}

func controllerSymbolLocation(symbol semantic.Symbol) protocol.Location {
	content, err := os.ReadFile(symbol.Path)
	if err != nil {
		return protocol.Location{URI: uriutil.FileURI(symbol.Path)}
	}
	rng := symbol.SelectionRange
	if rng.Len() == 0 {
		rng = symbol.Range
	}
	return protocol.Location{
		URI: uriutil.FileURI(symbol.Path),
		Range: controllerProtocolRange(
			rng,
			cst.NewLineIndex(string(content)),
		),
	}
}

func controllerProtocolRange(
	rng cst.TextRange,
	lineIndex *cst.LineIndex,
) protocol.Range {
	if lineIndex == nil {
		return protocol.Range{}
	}
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

func uniqueControllerLocations(
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
