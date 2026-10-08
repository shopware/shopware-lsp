package reference

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/shopware/shopware-lsp/internal/doctrine"
	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	"github.com/shopware/shopware-lsp/internal/parser/cst"
	"github.com/shopware/shopware-lsp/internal/php"
	"github.com/shopware/shopware-lsp/internal/uriutil"
)

type DoctrineReferenceProvider struct {
	index    *doctrine.Index
	phpIndex *php.PHPIndex
}

func NewDoctrineReferenceProvider(
	index *doctrine.Index,
	phpIndex *php.PHPIndex,
) *DoctrineReferenceProvider {
	return &DoctrineReferenceProvider{
		index:    index,
		phpIndex: phpIndex,
	}
}

func (p *DoctrineReferenceProvider) GetReferences(ctx context.Context, request *lsp.ReferenceRequest) ([]protocol.Location, error) {
	if p == nil || p.index == nil || request == nil || request.Root == nil || request.Document == nil || request.LineIndex == nil {
		return nil, nil
	}
	path, err := uriutil.Path(request.TextDocument.URI)
	if err != nil {
		return nil, err
	}
	offset := request.LineIndex.OffsetUTF16(uint32(request.Position.Line), uint32(request.Position.Character))
	reference, found := doctrine.TypeRegistrationReferenceAt(path, request.Root, offset)
	if !found || reference.Name == "" {
		return nil, nil
	}
	registrations, err := p.index.TypeRegistrations(reference.Name)
	if err != nil {
		return nil, err
	}
	var result []protocol.Location
	for _, registration := range registrations {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if registration.File == path {
			continue
		}
		if location, ok := doctrineReferenceLocation(registration.File, registration.NameRange); ok {
			result = append(result, location)
		}
	}
	for _, registration := range doctrine.TypeRegistrationsInDocument(path, request.Root) {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if strings.EqualFold(registration.Name, reference.Name) {
			result = append(result, protocol.Location{URI: request.TextDocument.URI, Range: doctrineReferenceRange(registration.NameRange, request.LineIndex)})
		}
	}
	if request.Context.IncludeDeclaration {
		for _, declaration := range p.index.TypeDeclarations(p.phpIndex) {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			if strings.EqualFold(declaration.Name, reference.Name) {
				if location, ok := doctrineReferenceLocation(declaration.File, declaration.Range); ok {
					result = append(result, location)
				}
			}
		}
	}
	return uniqueDoctrineReferenceLocations(result), nil
}

func doctrineReferenceLocation(
	path string,
	rng cst.TextRange,
) (protocol.Location, bool) {
	source, err := os.ReadFile(path)
	if err != nil {
		return protocol.Location{}, false
	}
	return protocol.Location{
		URI: uriutil.FileURI(path),
		Range: doctrineReferenceRange(
			rng,
			cst.NewLineIndex(string(source)),
		),
	}, true
}

func doctrineReferenceRange(
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

func uniqueDoctrineReferenceLocations(
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
