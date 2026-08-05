package hover

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/shopware/shopware-lsp/internal/doctrine"
	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	"github.com/shopware/shopware-lsp/internal/parser/cst"
	"github.com/shopware/shopware-lsp/internal/php"
	"github.com/shopware/shopware-lsp/internal/uriutil"
)

type DoctrineHoverProvider struct {
	index    *doctrine.Index
	phpIndex *php.PHPIndex
}

func NewDoctrineHoverProvider(
	index *doctrine.Index,
	phpIndexes ...*php.PHPIndex,
) *DoctrineHoverProvider {
	var phpIndex *php.PHPIndex
	if len(phpIndexes) != 0 {
		phpIndex = phpIndexes[0]
	}
	return &DoctrineHoverProvider{
		index:    index,
		phpIndex: phpIndex,
	}
}

func (p *DoctrineHoverProvider) GetHover(
	ctx context.Context,
	request *lsp.HoverRequest,
) (*protocol.Hover, error) {
	if p == nil || p.index == nil || request == nil ||
		request.Root == nil || request.Document == nil {
		return nil, nil
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
	); found && registration.Name != "" && registration.Class != "" {
		markdown := "**Doctrine DBAL type registration** `" +
			escapeDoctrineMarkdown(registration.Name) + "`\n\nClass: `" +
			escapeDoctrineMarkdown(registration.Class) + "`"
		if p.phpIndex != nil {
			if symbol, classFound := p.phpIndex.FindClass(
				registration.Class,
			); classFound && symbol.DocSummary != "" {
				markdown += "\n\n" +
					escapeDoctrineMarkdown(symbol.DocSummary)
			}
		}
		return doctrineHover(
			markdown,
			registration.Range,
			request.LineIndex,
		), nil
	}
	if reference, mappingFound := doctrine.MappingReferenceAt(
		path,
		request.Root,
		request.Document.Source,
		offset,
	); mappingFound {
		if result := p.mappingHover(
			reference,
			path,
			request.LineIndex,
		); result != nil {
			return result, nil
		}
	}
	if extension != ".php" {
		return nil, nil
	}
	if request.Node == nil {
		return nil, nil
	}
	if reference, found := p.index.DBALReferenceAt(
		ctx,
		request.Root,
		request.Node,
	); found {
		switch reference.Role {
		case doctrine.DBALTableReference:
			model, exists, err := p.index.ModelForTable(reference.Name)
			if err != nil || !exists {
				return nil, err
			}
			return p.entityHover(
				model.Class,
				reference.Range,
				request.LineIndex,
			)
		case doctrine.DBALColumnReference:
			model, field, exists, err := p.index.FieldForColumn(
				reference.Table,
				reference.Name,
			)
			if err != nil || !exists {
				return nil, err
			}
			return p.fieldHover(
				request,
				model.Class,
				field.Name,
				reference.Range,
			)
		case doctrine.DBALAliasReference:
			return doctrineHover(
				"**Doctrine DBAL join alias** `"+
					escapeDoctrineMarkdown(reference.Name)+"`",
				reference.Range,
				request.LineIndex,
			), nil
		}
	}
	reference, found := p.index.ReferenceAt(
		ctx,
		request.Root,
		request.Node,
	)
	if !found {
		if function, rng, functionFound := p.index.QueryFunctionReferenceAt(
			ctx,
			request.Root,
			request.Node,
			offset,
		); functionFound {
			return doctrineHover(
				"**Doctrine DQL function** `"+
					escapeDoctrineMarkdown(strings.ToUpper(function.Name))+
					"()`\n\nClass: `"+
					escapeDoctrineMarkdown(function.Class)+"`",
				rng,
				request.LineIndex,
			), nil
		}
		queryEntity, entityFound := p.index.QueryEntityReferenceAt(
			ctx,
			request.Root,
			request.Node,
			offset,
		)
		if entityFound {
			return p.entityHover(
				queryEntity.Entity,
				queryEntity.Range,
				request.LineIndex,
			)
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
				return nil, nil
			}
			markdown := fmt.Sprintf(
				"**Doctrine magic repository method** `%s`\n\nReturns `%s`",
				escapeDoctrineMarkdown(magic.Name),
				escapeDoctrineMarkdown(magic.ReturnType),
			)
			if len(magic.Fields) != 0 {
				var names []string
				for _, field := range magic.Fields {
					names = append(names, field.Name)
				}
				markdown += "\n\nCriteria: `" +
					escapeDoctrineMarkdown(strings.Join(names, "`, `")) +
					"`"
			}
			return doctrineHover(
				markdown,
				magic.NameRange,
				request.LineIndex,
			), nil
		}
		return p.fieldHover(
			request,
			queryReference.Entity,
			queryReference.Field,
			queryReference.Range,
		)
	}
	switch reference.Role {
	case doctrine.EntityReference:
		return p.entityHover(
			reference.Name,
			doctrine.ReferenceRange(reference),
			request.LineIndex,
		)
	case doctrine.FieldReference:
		return p.fieldHover(
			request,
			reference.Entity,
			reference.Name,
			doctrine.ReferenceRange(reference),
		)
	default:
		return nil, nil
	}
}

func (p *DoctrineHoverProvider) entityHover(
	entity string,
	rng cst.TextRange,
	lineIndex *cst.LineIndex,
) (*protocol.Hover, error) {
	model, exists, err := p.index.Model(entity)
	if err != nil || !exists {
		return nil, err
	}
	fields, _ := p.index.Fields(model.Class)
	markdown := fmt.Sprintf(
		"**Doctrine %s** `%s`\n\n%d mapped field(s)",
		model.Kind.String(),
		escapeDoctrineMarkdown(model.Class),
		len(fields),
	)
	if model.Table != "" {
		markdown += "\n\nTable: `" +
			escapeDoctrineMarkdown(model.Table) + "`"
	}
	if model.Repository != "" {
		markdown += "\n\nRepository: `" +
			escapeDoctrineMarkdown(model.Repository) + "`"
	}
	return doctrineHover(markdown, rng, lineIndex), nil
}

func (p *DoctrineHoverProvider) mappingHover(
	reference doctrine.MappingReference,
	path string,
	lineIndex *cst.LineIndex,
) *protocol.Hover {
	if reference.Name == "" {
		return nil
	}
	var markdown string
	switch reference.Role {
	case doctrine.MappingModelClass:
		markdown = "**Doctrine mapped class** `" +
			escapeDoctrineMarkdown(reference.Name) + "`"
	case doctrine.MappingRepositoryClass:
		markdown = "**Doctrine repository** `" +
			escapeDoctrineMarkdown(reference.Name) + "`\n\nModel: `" +
			escapeDoctrineMarkdown(reference.Owner) + "`"
	case doctrine.MappingTargetClass:
		markdown = "**Doctrine association target** `" +
			escapeDoctrineMarkdown(reference.Name) + "`\n\nField: `" +
			escapeDoctrineMarkdown(reference.Owner+"::"+reference.Field) + "`"
	case doctrine.MappingEmbeddedClass:
		markdown = "**Doctrine embedded class** `" +
			escapeDoctrineMarkdown(reference.Name) + "`\n\nField: `" +
			escapeDoctrineMarkdown(reference.Owner+"::"+reference.Field) + "`"
	case doctrine.MappingEnumClass:
		markdown = "**Doctrine enum type** `" +
			escapeDoctrineMarkdown(reference.Name) + "`\n\nField: `" +
			escapeDoctrineMarkdown(reference.Owner+"::"+reference.Field) + "`"
	case doctrine.MappingDiscriminatorClass:
		markdown = "**Doctrine discriminator class** `" +
			escapeDoctrineMarkdown(reference.Name) + "`\n\nMap: `" +
			escapeDoctrineMarkdown(reference.Field) + "` → base `" +
			escapeDoctrineMarkdown(reference.Owner) + "`"
	case doctrine.MappingDiscriminatorValue:
		markdown = "**Doctrine discriminator value** `" +
			escapeDoctrineMarkdown(reference.Name) + "`\n\nBase: `" +
			escapeDoctrineMarkdown(reference.Owner) + "`"
	case doctrine.MappingType:
		custom := doctrine.TypeDeclarationsForMapping(
			path,
			p.index.TypeDeclarations(p.phpIndex),
		)
		if !doctrine.IsKnownType(reference.Name, custom) {
			return nil
		}
		markdown = "**Doctrine mapping type** `" +
			escapeDoctrineMarkdown(reference.Name) + "`"
		for _, declaration := range custom {
			if strings.EqualFold(declaration.Name, reference.Name) {
				markdown += "\n\nCustom type class: `" +
					escapeDoctrineMarkdown(declaration.Class) + "`"
				break
			}
		}
	case doctrine.MappingProperty:
		markdown = "**Doctrine mapped property** `" +
			escapeDoctrineMarkdown(reference.Owner+"::$"+reference.Name) + "`"
		if p.phpIndex != nil {
			properties := p.phpIndex.FindProperties(
				reference.Owner,
				reference.Name,
			)
			if len(properties) != 0 &&
				!properties[0].Type.IsUnknown() {
				markdown += "\n\nPHP type: `" +
					escapeDoctrineMarkdown(
						properties[0].Type.String(),
					) + "`"
			}
		}
	case doctrine.MappingConstraintField,
		doctrine.MappingConstraintColumn:
		kind := "field"
		if reference.Role == doctrine.MappingConstraintColumn {
			kind = "column"
		}
		markdown = "**Doctrine table-constraint " + kind + "** `" +
			escapeDoctrineMarkdown(reference.Name) + "`"
		property := reference.Field
		if property == "" &&
			reference.Role == doctrine.MappingConstraintField {
			property = reference.Name
		}
		if property != "" {
			markdown += "\n\nMapped property: `" +
				escapeDoctrineMarkdown(
					reference.Owner+"::$"+property,
				) + "`"
		}
	case doctrine.MappingLifecycleMethod:
		markdown = "**Doctrine lifecycle callback** `" +
			escapeDoctrineMarkdown(reference.Owner+"::"+reference.Name+"()") +
			"`"
	default:
		return nil
	}
	return doctrineHover(markdown, reference.Range, lineIndex)
}

func doctrineHover(
	markdown string,
	rng cst.TextRange,
	lineIndex *cst.LineIndex,
) *protocol.Hover {
	startLine, startCharacter := lineIndex.PositionUTF16(rng.Start)
	endLine, endCharacter := lineIndex.PositionUTF16(rng.End)
	return &protocol.Hover{
		Contents: protocol.MarkupContent{
			Kind:  protocol.Markdown,
			Value: markdown,
		},
		Range: &protocol.Range{
			Start: protocol.Position{
				Line:      int(startLine),
				Character: int(startCharacter),
			},
			End: protocol.Position{
				Line:      int(endLine),
				Character: int(endCharacter),
			},
		},
	}
}

func (p *DoctrineHoverProvider) fieldHover(
	request *lsp.HoverRequest,
	entity,
	name string,
	rng cst.TextRange,
) (*protocol.Hover, error) {
	field, exists, err := p.field(entity, name)
	if err != nil || !exists {
		return nil, err
	}
	markdown := fmt.Sprintf(
		"**Doctrine field** `%s::%s`",
		escapeDoctrineMarkdown(entity),
		escapeDoctrineMarkdown(field.Name),
	)
	if field.Type != "" {
		markdown += "\n\nDoctrine type: `" +
			escapeDoctrineMarkdown(field.Type) + "`"
	}
	if field.PHPType != "" {
		markdown += "\n\nPHP type: `" +
			escapeDoctrineMarkdown(field.PHPType) + "`"
	}
	if field.Column != "" {
		markdown += "\n\nColumn: `" +
			escapeDoctrineMarkdown(field.Column) + "`"
	}
	if field.Relation != "" {
		markdown += "\n\n" +
			escapeDoctrineMarkdown(field.RelationType) + ": `" +
			escapeDoctrineMarkdown(field.Relation) + "`"
	}
	return doctrineHover(markdown, rng, request.LineIndex), nil
}

func (p *DoctrineHoverProvider) field(
	className,
	name string,
) (doctrine.Field, bool, error) {
	fields, err := p.index.Fields(className)
	if err != nil {
		return doctrine.Field{}, false, err
	}
	for _, field := range fields {
		if strings.EqualFold(field.Name, name) {
			return field, true, nil
		}
	}
	return doctrine.Field{}, false, nil
}

func escapeDoctrineMarkdown(value string) string {
	return strings.ReplaceAll(value, "`", "\\`")
}
