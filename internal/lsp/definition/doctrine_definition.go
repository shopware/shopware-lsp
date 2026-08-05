package definition

import (
	"context"
	"path/filepath"
	"strings"

	"github.com/shopware/shopware-lsp/internal/doctrine"
	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	phpquery "github.com/shopware/shopware-lsp/internal/parser/php/query"
	"github.com/shopware/shopware-lsp/internal/php"
	"github.com/shopware/shopware-lsp/internal/uriutil"
)

type DoctrineDefinitionProvider struct {
	index    *doctrine.Index
	phpIndex *php.PHPIndex
}

func NewDoctrineDefinitionProvider(
	index *doctrine.Index,
	phpIndex *php.PHPIndex,
) *DoctrineDefinitionProvider {
	return &DoctrineDefinitionProvider{
		index:    index,
		phpIndex: phpIndex,
	}
}

func (p *DoctrineDefinitionProvider) GetDefinition(
	ctx context.Context,
	request *lsp.DefinitionRequest,
) []protocol.Location {
	if p == nil || p.index == nil || request == nil ||
		request.Root == nil || request.Document == nil {
		return nil
	}
	path, _ := uriutil.Path(request.TextDocument.URI)
	extension := strings.ToLower(filepath.Ext(path))
	offset := request.LineIndex.OffsetUTF16(
		uint32(request.Position.Line),
		uint32(request.Position.Character),
	)
	if registration, found := doctrine.TypeRegistrationReferenceAt(
		path,
		request.Root,
		offset,
	); found && registration.Class != "" && p.phpIndex != nil {
		if symbol, classFound := p.phpIndex.FindClass(
			registration.Class,
		); classFound {
			return []protocol.Location{phpSymbolLocation(symbol)}
		}
	}
	if reference, mappingFound := doctrine.MappingReferenceAt(
		path,
		request.Root,
		request.Document.Source,
		offset,
	); mappingFound {
		if locations := p.mappingDefinitions(
			reference,
			path,
		); len(locations) != 0 {
			return locations
		}
	}
	if extension != ".php" {
		return nil
	}
	if request.Node == nil {
		return nil
	}
	if _, found := php.AssistantArgumentReference(
		ctx,
		request.Node,
		"Entity",
	); found {
		return p.entityDefinitions(phpquery.StringValue(request.Node))
	}
	if reference, found := p.index.DBALReferenceAt(
		ctx,
		request.Root,
		request.Node,
	); found {
		switch reference.Role {
		case doctrine.DBALTableReference:
			model, exists, err := p.index.ModelForTable(reference.Name)
			if err == nil && exists {
				return p.entityDefinitions(model.Class)
			}
		case doctrine.DBALColumnReference:
			model, field, exists, err := p.index.FieldForColumn(
				reference.Table,
				reference.Name,
			)
			if err == nil && exists {
				return p.fieldDefinitions(model.Class, field.Name)
			}
		}
	}
	reference, found := p.index.ReferenceAt(
		ctx,
		request.Root,
		request.Node,
	)
	if !found {
		if function, _, functionFound := p.index.QueryFunctionReferenceAt(
			ctx,
			request.Root,
			request.Node,
			offset,
		); functionFound && p.phpIndex != nil {
			if symbol, exists := p.phpIndex.FindClass(
				function.Class,
			); exists {
				return []protocol.Location{phpSymbolLocation(symbol)}
			}
		}
		queryEntity, entityFound := p.index.QueryEntityReferenceAt(
			ctx,
			request.Root,
			request.Node,
			offset,
		)
		if entityFound {
			return p.entityDefinitions(queryEntity.Entity)
		}
		queryReference, queryFound := p.index.QueryFieldReferenceAt(
			ctx,
			request.Root,
			request.Node,
			offset,
		)
		if !queryFound {
			magic, magicFound := p.index.MagicMethodAt(
				ctx,
				request.Root,
				request.Node,
			)
			if !magicFound {
				return nil
			}
			var result []protocol.Location
			for _, field := range magic.Fields {
				result = append(
					result,
					p.fieldDefinitions(magic.Entity, field.Name)...,
				)
			}
			return uniqueEventLocations(result)
		}
		return p.fieldDefinitions(
			queryReference.Entity,
			queryReference.Field,
		)
	}
	switch reference.Role {
	case doctrine.EntityReference:
		return p.entityDefinitions(reference.Name)
	case doctrine.FieldReference:
		return p.fieldDefinitions(reference.Entity, reference.Name)
	default:
		return nil
	}
}

func (p *DoctrineDefinitionProvider) mappingDefinitions(
	reference doctrine.MappingReference,
	path string,
) []protocol.Location {
	if p.phpIndex == nil || reference.Name == "" {
		return nil
	}
	switch reference.Role {
	case doctrine.MappingModelClass,
		doctrine.MappingRepositoryClass,
		doctrine.MappingTargetClass,
		doctrine.MappingEmbeddedClass,
		doctrine.MappingEnumClass,
		doctrine.MappingDiscriminatorClass:
		symbol, found := p.phpIndex.FindClass(reference.Name)
		if !found {
			return nil
		}
		return []protocol.Location{phpSymbolLocation(symbol)}
	case doctrine.MappingProperty,
		doctrine.MappingConstraintField,
		doctrine.MappingConstraintColumn:
		property := reference.Name
		if reference.Field != "" {
			property = reference.Field
		}
		var result []protocol.Location
		for _, symbol := range p.phpIndex.FindProperties(
			reference.Owner,
			property,
		) {
			result = append(result, phpSymbolLocation(symbol))
		}
		return uniqueEventLocations(result)
	case doctrine.MappingLifecycleMethod:
		var result []protocol.Location
		for _, symbol := range p.phpIndex.FindMethods(
			reference.Owner,
			reference.Name,
		) {
			result = append(result, phpSymbolLocation(symbol))
		}
		return uniqueEventLocations(result)
	case doctrine.MappingType:
		var result []protocol.Location
		declarations := doctrine.TypeDeclarationsForMapping(
			path,
			p.index.TypeDeclarations(p.phpIndex),
		)
		for _, declaration := range declarations {
			if !strings.EqualFold(declaration.Name, reference.Name) {
				continue
			}
			if location, found := consoleLocation(
				declaration.File,
				declaration.Range,
			); found {
				result = append(result, location)
			}
		}
		return uniqueEventLocations(result)
	default:
		return nil
	}
}

func (p *DoctrineDefinitionProvider) entityDefinitions(
	className string,
) []protocol.Location {
	var result []protocol.Location
	if p.phpIndex != nil {
		if symbol, found := p.phpIndex.FindClass(className); found {
			result = append(result, phpSymbolLocation(symbol))
		}
	}
	declarations, err := p.index.ModelDeclarations(className)
	if err != nil {
		return result
	}
	for _, declaration := range declarations {
		if p.phpIndex != nil {
			if symbol, found := p.phpIndex.FindClass(declaration.Class); found &&
				symbol.Path == declaration.File {
				continue
			}
		}
		if location, found := consoleLocation(
			declaration.File,
			declaration.NameRange,
		); found {
			result = append(result, location)
		}
	}
	return uniqueEventLocations(result)
}

func (p *DoctrineDefinitionProvider) fieldDefinitions(
	className,
	fieldName string,
) []protocol.Location {
	fields, err := p.index.Fields(className)
	if err != nil {
		return nil
	}
	var result []protocol.Location
	for _, field := range fields {
		if !strings.EqualFold(field.Name, fieldName) {
			continue
		}
		if location, found := consoleLocation(field.File, field.Range); found {
			result = append(result, location)
		}
	}
	return uniqueEventLocations(result)
}
