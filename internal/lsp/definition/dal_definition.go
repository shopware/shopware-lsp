package definition

import (
	"context"
	"path/filepath"
	"strings"

	"github.com/shopware/shopware-lsp/internal/language"
	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	jsquery "github.com/shopware/shopware-lsp/internal/parser/javascript/query"
	phpquery "github.com/shopware/shopware-lsp/internal/parser/php/query"
	twigquery "github.com/shopware/shopware-lsp/internal/parser/twig/query"
	"github.com/shopware/shopware-lsp/internal/shopware/dal"
	"github.com/shopware/shopware-lsp/internal/uriutil"
)

type DALDefinitionProvider struct {
	index *dal.Index
}

func NewDALDefinitionProvider(index *dal.Index) *DALDefinitionProvider {
	return &DALDefinitionProvider{index: index}
}

func (p *DALDefinitionProvider) GetDefinition(
	_ context.Context,
	params *lsp.DefinitionRequest,
) []protocol.Location {
	if p == nil || p.index == nil || params == nil || params.Node == nil {
		return nil
	}
	switch strings.ToLower(filepath.Ext(params.TextDocument.URI)) {
	case ".js", ".ts", ".vue":
		if strings.HasSuffix(
			strings.ToLower(params.TextDocument.URI), ".vue",
		) && lsp.EffectiveSyntaxLanguage(language.Vue, params.Node) !=
			language.JavaScript {
			return nil
		}
		if dal.IsJSEntityReference(params.Node) {
			definitions, err := p.index.Definition(jsquery.StringValue(params.Node))
			if err != nil {
				return nil
			}
			locations := make([]protocol.Location, 0, len(definitions))
			for _, definition := range definitions {
				locations = append(locations, dalLineLocation(definition.File, definition.Line))
			}
			return locations
		}
		referenceKind := dal.JSFieldReferenceAt(params.Node)
		if referenceKind == dal.JSFieldReferenceNone {
			return nil
		}
		offset := uint32(0)
		if params.LineIndex != nil && params.DefinitionParams != nil {
			offset = params.LineIndex.OffsetUTF16(
				uint32(params.Position.Line),
				uint32(params.Position.Character),
			)
		}
		name, associationSegment := dal.JSFieldReferenceSegment(
			params.Node,
			offset,
		)
		fields, err := p.index.FieldDefinitions(
			name,
			referenceKind == dal.JSFieldReferenceAssociation ||
				associationSegment,
		)
		if err != nil {
			return nil
		}
		locations := make([]protocol.Location, 0, len(fields))
		for _, field := range fields {
			locations = append(
				locations,
				dalLineLocation(field.File, field.Field.Line),
			)
		}
		return locations
	case ".twig":
		if !strings.Contains(filepath.ToSlash(params.TextDocument.URI), "/Resources/scripts/") ||
			!dal.IsTwigEntityReference(params.Node) {
			return nil
		}
		definitions, err := p.index.Definition(twigquery.StringValue(params.Node))
		if err != nil {
			return nil
		}
		locations := make([]protocol.Location, 0, len(definitions))
		for _, definition := range definitions {
			locations = append(locations, dalLineLocation(definition.File, definition.Line))
		}
		return locations
	case ".php":
		if !dal.IsPHPFieldReference(params.Node) {
			return nil
		}
		name := phpquery.StringValue(params.Node)
		definitions, err := p.index.Definitions()
		if err != nil {
			return nil
		}
		var locations []protocol.Location
		for _, definition := range definitions {
			for _, field := range definition.Fields {
				if field.Name == name {
					locations = append(locations, dalLineLocation(definition.File, field.Line))
				}
			}
		}
		return locations
	}
	return nil
}

func dalLineLocation(path string, line int) protocol.Location {
	if line < 1 {
		line = 1
	}
	return protocol.Location{
		URI: uriutil.FileURI(path),
		Range: protocol.Range{
			Start: protocol.Position{Line: line - 1},
			End:   protocol.Position{Line: line - 1},
		},
	}
}
