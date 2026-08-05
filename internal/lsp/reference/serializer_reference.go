package reference

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	"github.com/shopware/shopware-lsp/internal/parser/cst"
	"github.com/shopware/shopware-lsp/internal/php"
	"github.com/shopware/shopware-lsp/internal/php/semantic"
	"github.com/shopware/shopware-lsp/internal/serializer"
	"github.com/shopware/shopware-lsp/internal/uriutil"
)

type SerializerReferenceProvider struct {
	index    *serializer.Index
	phpIndex *php.PHPIndex
}

func NewSerializerReferenceProvider(
	index *serializer.Index,
	phpIndex *php.PHPIndex,
) *SerializerReferenceProvider {
	return &SerializerReferenceProvider{
		index:    index,
		phpIndex: phpIndex,
	}
}

func (p *SerializerReferenceProvider) GetReferences(
	ctx context.Context,
	request *lsp.ReferenceRequest,
) ([]protocol.Location, error) {
	if p == nil || p.index == nil || request == nil ||
		request.Node == nil || request.LineIndex == nil ||
		!strings.HasSuffix(
			strings.ToLower(request.TextDocument.URI),
			".php",
		) {
		return nil, nil
	}
	offset := request.LineIndex.OffsetUTF16(
		uint32(request.Position.Line),
		uint32(request.Position.Character),
	)
	className := ""
	if usage, found := serializer.UsageAt(request.Root, offset); found {
		className = usage.Class
	} else if phpContext := php.GetPHPContext(ctx); phpContext != nil {
		symbol, found := php.SymbolAt(
			phpContext.Document,
			phpContext.Snapshot,
			offset,
		)
		if found && symbol.IsClassLike() {
			className = symbol.FullyQualified
		}
	}
	if className == "" {
		return nil, nil
	}
	usages, err := p.index.Usages(className)
	if err != nil {
		return nil, err
	}
	currentPath, _ := uriutil.Path(request.TextDocument.URI)
	filtered := make([]serializer.Usage, 0, len(usages))
	for _, usage := range usages {
		if usage.File != currentPath {
			filtered = append(filtered, usage)
		}
	}
	for _, usage := range serializer.UsagesInDocument(
		currentPath,
		request.Root,
	) {
		if strings.EqualFold(usage.Class, className) {
			filtered = append(filtered, usage)
		}
	}

	seen := make(map[string]struct{}, len(filtered)+1)
	var result []protocol.Location
	if request.Context.IncludeDeclaration && p.phpIndex != nil {
		if symbol, found := p.phpIndex.FindClass(className); found {
			location := serializerSymbolLocation(symbol)
			result = append(result, location)
			seen[serializerLocationKey(location)] = struct{}{}
		}
	}
	for _, usage := range filtered {
		location, found := serializerUsageLocation(
			usage,
			currentPath,
			request,
		)
		if !found {
			continue
		}
		key := serializerLocationKey(location)
		if _, duplicate := seen[key]; duplicate {
			continue
		}
		seen[key] = struct{}{}
		result = append(result, location)
	}
	return result, nil
}

func serializerUsageLocation(
	usage serializer.Usage,
	currentPath string,
	request *lsp.ReferenceRequest,
) (protocol.Location, bool) {
	if usage.File == currentPath {
		return protocol.Location{
			URI: request.TextDocument.URI,
			Range: serializerRange(
				usage.Range,
				request.LineIndex,
			),
		}, true
	}
	source, err := os.ReadFile(usage.File)
	if err != nil {
		return protocol.Location{}, false
	}
	return protocol.Location{
		URI: uriutil.FileURI(usage.File),
		Range: serializerRange(
			usage.Range,
			cst.NewLineIndex(string(source)),
		),
	}, true
}

func serializerSymbolLocation(symbol semantic.Symbol) protocol.Location {
	source, err := os.ReadFile(symbol.Path)
	if err != nil {
		return protocol.Location{URI: uriutil.FileURI(symbol.Path)}
	}
	rng := symbol.SelectionRange
	if rng.Len() == 0 {
		rng = symbol.Range
	}
	return protocol.Location{
		URI:   uriutil.FileURI(symbol.Path),
		Range: serializerRange(rng, cst.NewLineIndex(string(source))),
	}
}

func serializerRange(
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

func serializerLocationKey(location protocol.Location) string {
	return fmt.Sprintf(
		"%s:%d:%d:%d:%d",
		location.URI,
		location.Range.Start.Line,
		location.Range.Start.Character,
		location.Range.End.Line,
		location.Range.End.Character,
	)
}
