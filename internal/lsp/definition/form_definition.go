package definition

import (
	"context"
	"path/filepath"
	"strings"

	"github.com/shopware/shopware-lsp/internal/form"
	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	phpquery "github.com/shopware/shopware-lsp/internal/parser/php/query"
	"github.com/shopware/shopware-lsp/internal/php"
	"github.com/shopware/shopware-lsp/internal/uriutil"
)

type FormDefinitionProvider struct {
	index    *form.Index
	phpIndex *php.PHPIndex
}

func NewFormDefinitionProvider(
	index *form.Index,
	phpIndex *php.PHPIndex,
) *FormDefinitionProvider {
	return &FormDefinitionProvider{index: index, phpIndex: phpIndex}
}

func (p *FormDefinitionProvider) GetDefinition(
	ctx context.Context,
	request *lsp.DefinitionRequest,
) []protocol.Location {
	if p == nil || p.index == nil || request == nil ||
		request.Node == nil {
		return nil
	}
	if strings.ToLower(filepath.Ext(request.TextDocument.URI)) == ".twig" {
		return p.twigFieldDefinitions(request)
	}
	if strings.ToLower(filepath.Ext(request.TextDocument.URI)) != ".php" {
		return nil
	}
	if _, found := php.AssistantArgumentReference(
		ctx,
		request.Node,
		"FormType",
	); found {
		return p.formTypeDefinition(phpquery.StringValue(request.Node))
	}
	reference, ok := form.ReferenceAt(ctx, request.Root, request.Node)
	if !ok {
		return nil
	}
	switch reference.Role {
	case form.ReferenceType:
		return p.formTypeDefinition(reference.Name)
	case form.ReferenceOption:
		options, err := p.index.EffectiveOptions(reference.FormType)
		if err != nil {
			return nil
		}
		var result []protocol.Location
		for _, option := range options {
			if !strings.EqualFold(option.Name, reference.Name) {
				continue
			}
			if location, exists := consoleLocation(
				option.File,
				option.Range,
			); exists {
				result = append(result, location)
			}
		}
		return result
	case form.ReferenceField:
		return p.fieldDefinitions(reference)
	}
	return nil
}

func (p *FormDefinitionProvider) formTypeDefinition(
	name string,
) []protocol.Location {
	current, found, err := p.index.GetType(name)
	if err != nil || !found {
		return nil
	}
	if p.phpIndex != nil {
		if symbol, exists := p.phpIndex.FindClass(current.Class); exists {
			return []protocol.Location{phpSymbolLocation(symbol)}
		}
	}
	if location, exists := consoleLocation(
		current.File,
		current.NameRange,
	); exists {
		return []protocol.Location{location}
	}
	return nil
}

func (p *FormDefinitionProvider) twigFieldDefinitions(
	request *lsp.DefinitionRequest,
) []protocol.Location {
	if p.phpIndex == nil || request.LineIndex == nil {
		return nil
	}
	path, err := uriutil.Path(request.TextDocument.URI)
	if err != nil {
		return nil
	}
	variables, err := form.TwigFormVariables(p.phpIndex, path)
	if err != nil {
		return nil
	}
	offset := request.LineIndex.OffsetUTF16(
		uint32(request.Position.Line),
		uint32(request.Position.Character),
	)
	if reference, found := form.TwigViewVarContextAt(
		request.Node,
		offset,
		variables,
	); found {
		return p.twigViewVarDefinitions(reference)
	}
	reference, found := form.TwigFieldContextAt(
		request.Node,
		offset,
		variables,
	)
	if !found || reference.Node == nil || reference.Name == "" {
		return nil
	}
	var result []protocol.Location
	for _, formType := range reference.FormTypes {
		result = append(result, p.fieldDefinitions(form.Reference{
			Role:     form.ReferenceField,
			Origin:   form.OriginFieldAccess,
			Name:     reference.Name,
			Node:     reference.Node,
			FormType: formType,
		})...)
	}
	return uniqueEventLocations(result)
}

func (p *FormDefinitionProvider) twigViewVarDefinitions(
	reference form.TwigViewVarReference,
) []protocol.Location {
	if reference.Node == nil || reference.Name == "" {
		return nil
	}
	var result []protocol.Location
	for _, formType := range reference.FormTypes {
		viewVars, err := p.index.EffectiveViewVars(formType)
		if err != nil {
			return nil
		}
		for _, viewVar := range viewVars {
			if !strings.EqualFold(viewVar.Name, reference.Name) {
				continue
			}
			if location, exists := consoleLocation(
				viewVar.File,
				viewVar.Range,
			); exists {
				result = append(result, location)
			}
		}
	}
	return uniqueEventLocations(result)
}

func (p *FormDefinitionProvider) fieldDefinitions(
	reference form.Reference,
) []protocol.Location {
	var result []protocol.Location
	fields, err := p.index.EffectiveFields(reference.FormType)
	if err != nil {
		return nil
	}
	for _, field := range fields {
		if !formFieldNameMatches(field.Name, reference.Name) {
			continue
		}
		if location, exists := consoleLocation(
			field.File,
			field.Range,
		); exists {
			result = append(result, location)
		}
	}
	dataFields, err := p.index.DataFieldsFor(reference.FormType)
	if err != nil {
		return result
	}
	for _, field := range dataFields {
		if !formFieldNameMatches(field.Name, reference.Name) {
			continue
		}
		if p.phpIndex != nil && field.Symbol.ID != "" {
			result = append(result, phpSymbolLocation(field.Symbol))
			continue
		}
		if location, exists := consoleLocation(
			field.File,
			field.Range,
		); exists {
			result = append(result, location)
		}
	}
	return uniqueEventLocations(result)
}

func formFieldNameMatches(left, right string) bool {
	normalize := func(value string) string {
		return strings.ToLower(strings.ReplaceAll(value, "_", ""))
	}
	return normalize(left) == normalize(right)
}
