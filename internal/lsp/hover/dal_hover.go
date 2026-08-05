package hover

import (
	"context"
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/shopware/shopware-lsp/internal/language"
	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	"github.com/shopware/shopware-lsp/internal/shopware/dal"
)

type DALHoverProvider struct {
	index *dal.Index
}

func NewDALHoverProvider(index *dal.Index) *DALHoverProvider {
	return &DALHoverProvider{index: index}
}

func (p *DALHoverProvider) GetHover(
	_ context.Context,
	request *lsp.HoverRequest,
) (*protocol.Hover, error) {
	if p == nil || p.index == nil || request == nil ||
		request.Node == nil || request.LineIndex == nil ||
		request.HoverParams == nil {
		return nil, nil
	}
	extension := strings.ToLower(filepath.Ext(request.TextDocument.URI))
	if extension != ".js" && extension != ".ts" && extension != ".vue" {
		return nil, nil
	}
	if extension == ".vue" && lsp.EffectiveSyntaxLanguage(
		language.Vue, request.Node,
	) != language.JavaScript {
		return nil, nil
	}
	if reference, found := dal.JSEntityReferenceAt(request.Node); found {
		return p.entityHover(reference.Name)
	}
	referenceKind := dal.JSFieldReferenceAt(request.Node)
	if referenceKind == dal.JSFieldReferenceNone {
		return nil, nil
	}
	offset := request.LineIndex.OffsetUTF16(
		uint32(request.Position.Line),
		uint32(request.Position.Character),
	)
	name, associationSegment := dal.JSFieldReferenceSegment(
		request.Node,
		offset,
	)
	fields, err := p.index.FieldDefinitions(
		name,
		referenceKind == dal.JSFieldReferenceAssociation ||
			associationSegment,
	)
	if err != nil || len(fields) == 0 {
		return nil, err
	}
	sort.Slice(fields, func(left, right int) bool {
		if fields[left].Entity != fields[right].Entity {
			return fields[left].Entity < fields[right].Entity
		}
		return fields[left].Field.Type < fields[right].Field.Type
	})
	kind := "field"
	if referenceKind == dal.JSFieldReferenceAssociation ||
		associationSegment {
		kind = "association"
	}
	var markdown strings.Builder
	fmt.Fprintf(&markdown, "**Shopware DAL %s** `%s`", kind, name)
	const limit = 12
	for index, field := range fields {
		if index == limit {
			fmt.Fprintf(
				&markdown,
				"\n\n- …and %d more definitions",
				len(fields)-limit,
			)
			break
		}
		fmt.Fprintf(
			&markdown,
			"\n\n- `%s` — `%s` (`%s`)",
			field.Entity,
			field.Field.Type,
			field.Class,
		)
	}
	return &protocol.Hover{Contents: protocol.MarkupContent{
		Kind: protocol.Markdown, Value: markdown.String(),
	}}, nil
}

func (p *DALHoverProvider) entityHover(
	name string,
) (*protocol.Hover, error) {
	definitions, err := p.index.Definition(name)
	if err != nil || len(definitions) == 0 {
		return nil, err
	}
	sort.Slice(definitions, func(left, right int) bool {
		if definitions[left].Class != definitions[right].Class {
			return definitions[left].Class < definitions[right].Class
		}
		return definitions[left].File < definitions[right].File
	})
	sections := make([]string, 0, len(definitions))
	for _, definition := range definitions {
		section := fmt.Sprintf(
			"**Shopware DAL entity** `%s`\n\nPHP definition: `%s`.",
			definition.Name, definition.Class,
		)
		if len(definition.Fields) > 0 {
			section += fmt.Sprintf(
				"\n\nIndexed fields: %d.", len(definition.Fields),
			)
		}
		sections = append(sections, section)
	}
	return &protocol.Hover{Contents: protocol.MarkupContent{
		Kind: protocol.Markdown, Value: strings.Join(sections, "\n\n---\n\n"),
	}}, nil
}

var _ lsp.HoverProvider = (*DALHoverProvider)(nil)
