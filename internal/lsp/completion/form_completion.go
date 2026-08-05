package completion

import (
	"context"
	"path/filepath"
	"sort"
	"strings"

	"github.com/shopware/shopware-lsp/internal/form"
	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	"github.com/shopware/shopware-lsp/internal/parser/cst"
	"github.com/shopware/shopware-lsp/internal/php"
	"github.com/shopware/shopware-lsp/internal/uriutil"
)

type FormCompletionProvider struct {
	index    *form.Index
	phpIndex *php.PHPIndex
}

func NewFormCompletionProvider(
	index *form.Index,
	phpIndexes ...*php.PHPIndex,
) *FormCompletionProvider {
	var phpIndex *php.PHPIndex
	if len(phpIndexes) != 0 {
		phpIndex = phpIndexes[0]
	}
	return &FormCompletionProvider{index: index, phpIndex: phpIndex}
}

func (p *FormCompletionProvider) GetCompletions(
	ctx context.Context,
	request *lsp.CompletionRequest,
) []protocol.CompletionItem {
	if p == nil || p.index == nil || request == nil ||
		request.Node == nil {
		return nil
	}
	if strings.ToLower(filepath.Ext(request.TextDocument.URI)) == ".twig" {
		return p.twigFieldCompletions(request)
	}
	if strings.ToLower(filepath.Ext(request.TextDocument.URI)) != ".php" {
		return nil
	}
	if reference, found := php.AssistantArgumentReference(
		ctx,
		request.Node,
		"FormType",
	); found {
		return p.assistantFormTypeCompletions(request, reference)
	}
	reference, ok := form.ReferenceAt(ctx, request.Root, request.Node)
	if !ok {
		return nil
	}
	switch reference.Role {
	case form.ReferenceType:
		return p.typeCompletions()
	case form.ReferenceOption:
		return p.optionCompletions(request, reference)
	case form.ReferenceField:
		return p.fieldCompletions(request, reference)
	default:
		return nil
	}
}

func (p *FormCompletionProvider) assistantFormTypeCompletions(
	request *lsp.CompletionRequest,
	reference cst.TextRange,
) []protocol.CompletionItem {
	items := p.typeCompletions()
	if request == nil || request.LineIndex == nil {
		return items
	}
	replacement := namedArgumentCompletionRange(reference, request.LineIndex)
	for index := range items {
		items[index].TextEdit = protocol.TextEdit{
			Range:   replacement,
			NewText: items[index].Label,
		}
	}
	return items
}

func (p *FormCompletionProvider) twigFieldCompletions(
	request *lsp.CompletionRequest,
) []protocol.CompletionItem {
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
		return p.twigViewVarCompletions(reference.FormTypes)
	}
	reference, found := form.TwigFieldContextAt(
		request.Node,
		offset,
		variables,
	)
	if !found {
		return nil
	}
	byName := make(map[string]protocol.CompletionItem)
	for _, formType := range reference.FormTypes {
		fields, fieldErr := p.index.EffectiveFields(formType)
		if fieldErr != nil {
			return nil
		}
		for _, field := range fields {
			key := strings.ToLower(field.Name)
			if _, exists := byName[key]; exists {
				continue
			}
			detail := field.Type
			if detail == "" {
				detail = field.Class
			} else if field.Class != "" {
				detail += " · " + field.Class
			}
			byName[key] = protocol.CompletionItem{
				Label:  field.Name,
				Kind:   int(protocol.FieldCompletion),
				Detail: detail,
			}
		}
	}
	result := make([]protocol.CompletionItem, 0, len(byName))
	for _, item := range byName {
		result = append(result, item)
	}
	sort.Slice(result, func(left, right int) bool {
		return strings.ToLower(result[left].Label) <
			strings.ToLower(result[right].Label)
	})
	return result
}

func (p *FormCompletionProvider) twigViewVarCompletions(
	formTypes []string,
) []protocol.CompletionItem {
	byName := make(map[string]protocol.CompletionItem)
	for _, formType := range formTypes {
		viewVars, err := p.index.EffectiveViewVars(formType)
		if err != nil {
			return nil
		}
		for _, viewVar := range viewVars {
			key := strings.ToLower(viewVar.Name)
			if _, exists := byName[key]; exists {
				continue
			}
			detail := viewVar.Type
			if viewVar.Value != "" {
				detail += " · " + viewVar.Value
			}
			if viewVar.Class != "" {
				detail += " · " + viewVar.Class
			}
			byName[key] = protocol.CompletionItem{
				Label:  viewVar.Name,
				Kind:   int(protocol.PropertyCompletion),
				Detail: strings.Trim(detail, " ·"),
			}
		}
	}
	result := make([]protocol.CompletionItem, 0, len(byName))
	for _, item := range byName {
		result = append(result, item)
	}
	sort.Slice(result, func(left, right int) bool {
		return strings.ToLower(result[left].Label) <
			strings.ToLower(result[right].Label)
	})
	return result
}

func (p *FormCompletionProvider) typeCompletions() []protocol.CompletionItem {
	types, err := p.index.GetTypes()
	if err != nil {
		return nil
	}
	seen := make(map[string]struct{})
	var result []protocol.CompletionItem
	for _, current := range types {
		labels := current.Aliases
		if len(labels) == 0 {
			labels = []string{current.Class}
		}
		for _, label := range labels {
			key := strings.ToLower(label)
			if _, exists := seen[key]; exists {
				continue
			}
			seen[key] = struct{}{}
			detail := current.Class
			if current.Parent != "" {
				detail += " · parent " + current.Parent
			}
			result = append(result, protocol.CompletionItem{
				Label:  label,
				Kind:   int(protocol.ClassCompletion),
				Detail: detail,
			})
		}
	}
	return result
}

func (p *FormCompletionProvider) optionCompletions(
	request *lsp.CompletionRequest,
	reference form.Reference,
) []protocol.CompletionItem {
	formType := reference.FormType
	if formType == "" {
		formType = "Symfony\\Component\\Form\\Extension\\Core\\Type\\FormType"
	}
	var options []form.Option
	var err error
	if current, found := currentDocumentFormType(
		request.TextDocument.URI,
		request.Root,
		reference,
	); found && formTypeMatches(current, formType) {
		options, err = p.index.EffectiveOptionsFor(current)
	} else {
		options, err = p.index.EffectiveOptions(formType)
	}
	if err != nil {
		return nil
	}
	result := make([]protocol.CompletionItem, 0, len(options))
	for _, option := range options {
		detail := option.Class
		if option.Default != "" {
			detail += " · default " + option.Default
		}
		if len(option.AllowedTypes) != 0 {
			detail += " · " + strings.Join(option.AllowedTypes, "|")
		}
		result = append(result, protocol.CompletionItem{
			Label:  option.Name,
			Kind:   int(protocol.PropertyCompletion),
			Detail: detail,
		})
	}
	return result
}

func (p *FormCompletionProvider) fieldCompletions(
	request *lsp.CompletionRequest,
	reference form.Reference,
) []protocol.CompletionItem {
	if reference.FormType == "" {
		return nil
	}
	if reference.Origin == form.OriginFieldAccess {
		fields, err := p.index.EffectiveFields(reference.FormType)
		if err != nil {
			return nil
		}
		result := make([]protocol.CompletionItem, 0, len(fields))
		for _, field := range fields {
			result = append(result, protocol.CompletionItem{
				Label:  field.Name,
				Kind:   int(protocol.FieldCompletion),
				Detail: field.Type,
			})
		}
		return result
	}
	var fields []form.DataField
	if current, found := currentDocumentFormType(
		request.TextDocument.URI,
		request.Root,
		reference,
	); found && formTypeMatches(current, reference.FormType) &&
		current.DataClass != "" {
		fields = p.index.DataFieldsForClass(current.DataClass)
	} else {
		var err error
		fields, err = p.index.DataFieldsFor(reference.FormType)
		if err != nil {
			return nil
		}
	}
	result := make([]protocol.CompletionItem, 0, len(fields))
	for _, field := range fields {
		result = append(result, protocol.CompletionItem{
			Label:  field.Name,
			Kind:   int(protocol.PropertyCompletion),
			Detail: field.Type,
		})
	}
	return result
}

func (p *FormCompletionProvider) GetTriggerCharacters() []string {
	return []string{"'", "\""}
}

func currentDocumentFormType(
	uri string,
	root *cst.Node,
	reference form.Reference,
) (form.Type, bool) {
	if root == nil || reference.Class == "" {
		return form.Type{}, false
	}
	path, _ := uriutil.Path(uri)
	return form.TypeInDocument(path, root, reference.Class)
}

func formTypeMatches(current form.Type, name string) bool {
	if strings.EqualFold(current.Class, strings.TrimPrefix(name, `\`)) {
		return true
	}
	for _, alias := range current.Aliases {
		if strings.EqualFold(alias, name) {
			return true
		}
	}
	return false
}
