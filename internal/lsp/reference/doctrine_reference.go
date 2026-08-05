package reference

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/shopware/shopware-lsp/internal/doctrine"
	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	"github.com/shopware/shopware-lsp/internal/parser/cst"
	"github.com/shopware/shopware-lsp/internal/php"
	"github.com/shopware/shopware-lsp/internal/php/semantic"
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

func (p *DoctrineReferenceProvider) GetReferences(
	ctx context.Context,
	request *lsp.ReferenceRequest,
) ([]protocol.Location, error) {
	if p == nil || p.index == nil || request == nil ||
		request.Document == nil || request.Root == nil ||
		request.LineIndex == nil {
		return nil, nil
	}
	path, err := uriutil.Path(request.TextDocument.URI)
	if err != nil {
		return nil, err
	}
	offset := request.LineIndex.OffsetUTF16(
		uint32(request.Position.Line),
		uint32(request.Position.Character),
	)
	if typeName, typeFound := doctrineTypeReferenceTargetAt(
		path,
		request.Root,
		request.Document.Source,
		offset,
	); typeFound {
		return p.typeReferences(request, path, typeName)
	}
	target, includeDoctrineDeclarations, found := p.targetAt(
		ctx,
		request,
		path,
		offset,
	)
	if !found {
		return nil, nil
	}

	usages, err := p.index.DQLUsages(target)
	if err != nil {
		return nil, err
	}
	current := doctrine.ValidatedDQLReferencesInDocument(
		p.index,
		ctx,
		request.Root,
		path,
	)
	filtered := make([]doctrine.DQLReference, 0, len(usages)+len(current))
	for _, usage := range usages {
		if usage.File != path {
			filtered = append(filtered, usage)
		}
	}
	for _, usage := range current {
		if doctrine.DQLReferenceKey(usage) ==
			doctrine.DQLReferenceKey(target) {
			filtered = append(filtered, usage)
		}
	}
	if target.Role == doctrine.DQLFieldReference {
		for _, mapping := range doctrine.MappingReferencesInDocument(
			path,
			request.Root,
			request.Document.Source,
		) {
			if mapping.Role != doctrine.MappingConstraintField &&
				mapping.Role != doctrine.MappingConstraintColumn {
				continue
			}
			field := mapping.Field
			if field == "" &&
				mapping.Role ==
					doctrine.MappingConstraintField {
				field = mapping.Name
			}
			if strings.EqualFold(mapping.Owner, target.Entity) &&
				strings.EqualFold(field, target.Field) {
				filtered = append(filtered, doctrine.DQLReference{
					Role:   doctrine.DQLFieldReference,
					Entity: target.Entity,
					Field:  target.Field,
					File:   path,
					Range:  mapping.Range,
				})
			}
		}
	}

	var result []protocol.Location
	if request.Context.IncludeDeclaration && includeDoctrineDeclarations {
		result = append(result, p.declarations(target)...)
	}
	for _, usage := range filtered {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if location, ok := doctrineDQLLocation(
			usage,
			path,
			request,
		); ok {
			result = append(result, location)
		}
	}
	return uniqueDoctrineReferenceLocations(result), nil
}

func doctrineTypeReferenceTargetAt(
	path string,
	root *cst.Node,
	source string,
	offset uint32,
) (string, bool) {
	if registration, found := doctrine.TypeRegistrationReferenceAt(
		path,
		root,
		offset,
	); found && registration.Role == doctrine.TypeRegistrationName &&
		registration.Name != "" {
		return registration.Name, true
	}
	if reference, found := doctrine.MappingReferenceAt(
		path,
		root,
		source,
		offset,
	); found && reference.Role == doctrine.MappingType &&
		reference.Name != "" {
		return reference.Name, true
	}
	return "", false
}

func (p *DoctrineReferenceProvider) typeReferences(
	request *lsp.ReferenceRequest,
	currentPath,
	name string,
) ([]protocol.Location, error) {
	usages, err := p.index.TypeUsages(name)
	if err != nil {
		return nil, err
	}
	registrations, err := p.index.TypeRegistrations(name)
	if err != nil {
		return nil, err
	}
	currentUsages := doctrine.MappingReferencesInDocument(
		currentPath,
		request.Root,
		request.Document.Source,
	)
	currentRegistrations := doctrine.TypeRegistrationsInDocument(
		currentPath,
		request.Root,
	)

	var result []protocol.Location
	if request.Context.IncludeDeclaration {
		for _, declaration := range p.index.TypeDeclarations(p.phpIndex) {
			if !strings.EqualFold(declaration.Name, name) {
				continue
			}
			if location, found := doctrineReferenceLocation(
				declaration.File,
				declaration.Range,
			); found {
				result = append(result, location)
			}
		}
		for _, registration := range registrations {
			if registration.File == currentPath {
				continue
			}
			if location, found := doctrineReferenceLocation(
				registration.File,
				registration.NameRange,
			); found {
				result = append(result, location)
			}
		}
		for _, registration := range currentRegistrations {
			if !strings.EqualFold(registration.Name, name) {
				continue
			}
			result = append(result, protocol.Location{
				URI: request.TextDocument.URI,
				Range: doctrineReferenceRange(
					registration.NameRange,
					request.LineIndex,
				),
			})
		}
	}
	for _, usage := range usages {
		if usage.File == currentPath {
			continue
		}
		if location, found := doctrineReferenceLocation(
			usage.File,
			usage.Range,
		); found {
			result = append(result, location)
		}
	}
	for _, usage := range currentUsages {
		if usage.Role != doctrine.MappingType ||
			!strings.EqualFold(usage.Name, name) {
			continue
		}
		result = append(result, protocol.Location{
			URI: request.TextDocument.URI,
			Range: doctrineReferenceRange(
				usage.Range,
				request.LineIndex,
			),
		})
	}
	return uniqueDoctrineReferenceLocations(result), nil
}

func (p *DoctrineReferenceProvider) targetAt(
	ctx context.Context,
	request *lsp.ReferenceRequest,
	path string,
	offset uint32,
) (doctrine.DQLReference, bool, bool) {
	if strings.EqualFold(filepath.Ext(path), ".php") {
		if mapping, found := doctrine.MappingReferenceAt(
			path,
			request.Root,
			request.Document.Source,
			offset,
		); found && mapping.Role == doctrine.MappingDiscriminatorClass {
			return doctrine.DQLReference{
				Role:   doctrine.DQLEntityReference,
				Entity: mapping.Name,
			}, true, true
		}
		if reference, found := p.index.DQLReferenceAt(
			ctx,
			request.Root,
			request.Node,
			offset,
			path,
		); found {
			return reference, true, true
		}
		return p.phpSymbolTarget(ctx, path, offset)
	}
	reference, found := doctrine.MappingReferenceAt(
		path,
		request.Root,
		request.Document.Source,
		offset,
	)
	if !found {
		return doctrine.DQLReference{}, false, false
	}
	switch reference.Role {
	case doctrine.MappingModelClass:
		return doctrine.DQLReference{
			Role:   doctrine.DQLEntityReference,
			Entity: reference.Name,
		}, true, true
	case doctrine.MappingDiscriminatorClass:
		return doctrine.DQLReference{
			Role:   doctrine.DQLEntityReference,
			Entity: reference.Name,
		}, true, true
	case doctrine.MappingProperty,
		doctrine.MappingConstraintField,
		doctrine.MappingConstraintColumn:
		field := reference.Name
		if reference.Field != "" {
			field = reference.Field
		}
		return doctrine.DQLReference{
			Role:   doctrine.DQLFieldReference,
			Entity: reference.Owner,
			Field:  field,
		}, true, true
	default:
		return doctrine.DQLReference{}, false, false
	}
}

func (p *DoctrineReferenceProvider) phpSymbolTarget(
	ctx context.Context,
	path string,
	offset uint32,
) (doctrine.DQLReference, bool, bool) {
	if p.phpIndex == nil {
		return doctrine.DQLReference{}, false, false
	}
	var document *semantic.Document
	var snapshot *semantic.Snapshot
	if phpContext := php.GetPHPContext(ctx); phpContext != nil {
		document = phpContext.Document
		snapshot = phpContext.Snapshot
	}
	if document == nil || snapshot == nil {
		var found bool
		document, found = p.phpIndex.SemanticDocument(path)
		if !found {
			return doctrine.DQLReference{}, false, false
		}
		snapshot = p.phpIndex.SemanticSnapshot()
	}
	symbol, found := php.SymbolAt(document, snapshot, offset)
	if !found {
		return doctrine.DQLReference{}, false, false
	}
	if symbol.IsClassLike() {
		return doctrine.DQLReference{
			Role:   doctrine.DQLEntityReference,
			Entity: symbol.FullyQualified,
		}, false, true
	}
	if symbol.Kind != semantic.PropertySymbol {
		return doctrine.DQLReference{}, false, false
	}
	class, found := snapshot.Symbol(symbol.Container)
	if !found || !class.IsClassLike() {
		return doctrine.DQLReference{}, false, false
	}
	return doctrine.DQLReference{
		Role:   doctrine.DQLFieldReference,
		Entity: class.FullyQualified,
		Field:  strings.TrimPrefix(symbol.Name, "$"),
	}, false, true
}

func (p *DoctrineReferenceProvider) declarations(
	target doctrine.DQLReference,
) []protocol.Location {
	var result []protocol.Location
	switch target.Role {
	case doctrine.DQLEntityReference:
		if p.phpIndex != nil {
			if symbol, found := p.phpIndex.FindClass(target.Entity); found {
				result = append(result, doctrineSymbolLocation(symbol))
			}
		}
		declarations, err := p.index.ModelDeclarations(target.Entity)
		if err != nil {
			return result
		}
		for _, declaration := range declarations {
			if location, found := doctrineReferenceLocation(
				declaration.File,
				declaration.NameRange,
			); found {
				result = append(result, location)
			}
		}
	case doctrine.DQLFieldReference:
		fields, err := p.index.Fields(target.Entity)
		if err != nil {
			return nil
		}
		for _, field := range fields {
			if !strings.EqualFold(field.Name, target.Field) {
				continue
			}
			if location, found := doctrineReferenceLocation(
				field.File,
				field.Range,
			); found {
				result = append(result, location)
			}
		}
	}
	return uniqueDoctrineReferenceLocations(result)
}

func doctrineDQLLocation(
	usage doctrine.DQLReference,
	currentPath string,
	request *lsp.ReferenceRequest,
) (protocol.Location, bool) {
	if usage.File == currentPath {
		return protocol.Location{
			URI: request.TextDocument.URI,
			Range: doctrineReferenceRange(
				usage.Range,
				request.LineIndex,
			),
		}, true
	}
	return doctrineReferenceLocation(usage.File, usage.Range)
}

func doctrineSymbolLocation(symbol semantic.Symbol) protocol.Location {
	rng := symbol.SelectionRange
	if rng.Len() == 0 {
		rng = symbol.Range
	}
	location, found := doctrineReferenceLocation(symbol.Path, rng)
	if found {
		return location
	}
	return protocol.Location{URI: uriutil.FileURI(symbol.Path)}
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
