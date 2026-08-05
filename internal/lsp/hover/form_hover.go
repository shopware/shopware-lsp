package hover

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/shopware/shopware-lsp/internal/form"
	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	"github.com/shopware/shopware-lsp/internal/php"
	"github.com/shopware/shopware-lsp/internal/uriutil"
)

type FormHoverProvider struct {
	root     string
	index    *form.Index
	phpIndex *php.PHPIndex
}

func NewFormHoverProvider(
	root string,
	index *form.Index,
	phpIndexes ...*php.PHPIndex,
) *FormHoverProvider {
	var phpIndex *php.PHPIndex
	if len(phpIndexes) != 0 {
		phpIndex = phpIndexes[0]
	}
	return &FormHoverProvider{
		root:     root,
		index:    index,
		phpIndex: phpIndex,
	}
}

func (p *FormHoverProvider) GetHover(
	ctx context.Context,
	request *lsp.HoverRequest,
) (*protocol.Hover, error) {
	if p == nil || p.index == nil || request == nil ||
		request.Node == nil {
		return nil, nil
	}
	var reference form.Reference
	var ok bool
	switch strings.ToLower(filepath.Ext(request.TextDocument.URI)) {
	case ".php":
		reference, ok = form.ReferenceAt(ctx, request.Root, request.Node)
	case ".twig":
		viewVarHover, err := p.twigViewVarHover(request)
		if err != nil || viewVarHover != nil {
			return viewVarHover, err
		}
		reference, ok = p.twigFieldReference(request)
	default:
		return nil, nil
	}
	if !ok || reference.Node == nil {
		return nil, nil
	}

	var markdown strings.Builder
	switch reference.Role {
	case form.ReferenceType:
		current, found, err := p.index.GetType(reference.Name)
		if err != nil || !found {
			return nil, err
		}
		fmt.Fprintf(
			&markdown,
			"**Symfony form type** `%s`",
			escapeFormMarkdown(current.Class),
		)
		if current.Parent != "" {
			fmt.Fprintf(
				&markdown,
				"\n\nParent: `%s`",
				escapeFormMarkdown(current.Parent),
			)
		}
		if current.DataClass != "" {
			fmt.Fprintf(
				&markdown,
				"\n\nData class: `%s`",
				escapeFormMarkdown(current.DataClass),
			)
		}
		options, _ := p.index.EffectiveOptions(current.Class)
		fields, _ := p.index.EffectiveFields(current.Class)
		fmt.Fprintf(
			&markdown,
			"\n\n%d option(s) · %d field(s)",
			len(options),
			len(fields),
		)
	case form.ReferenceOption:
		options, err := p.index.EffectiveOptions(reference.FormType)
		if err != nil {
			return nil, err
		}
		option, found := findFormOption(options, reference.Name)
		if !found {
			return nil, nil
		}
		fmt.Fprintf(
			&markdown,
			"**Symfony form option** `%s`",
			escapeFormMarkdown(option.Name),
		)
		if option.Class != "" {
			fmt.Fprintf(
				&markdown,
				"\n\nDefined by `%s`",
				escapeFormMarkdown(option.Class),
			)
		}
		if option.Default != "" {
			fmt.Fprintf(
				&markdown,
				"\n\nDefault: `%s`",
				escapeFormMarkdown(option.Default),
			)
		}
		if len(option.AllowedTypes) != 0 {
			fmt.Fprintf(
				&markdown,
				"\n\nAllowed types: `%s`",
				escapeFormMarkdown(
					strings.Join(option.AllowedTypes, "|"),
				),
			)
		}
		p.appendSource(&markdown, option.File)
	case form.ReferenceField:
		field, dataField, found, err := p.formField(reference)
		if err != nil || !found {
			return nil, err
		}
		fmt.Fprintf(
			&markdown,
			"**Symfony form field** `%s`",
			escapeFormMarkdown(reference.Name),
		)
		if field != nil && field.Type != "" {
			fmt.Fprintf(
				&markdown,
				"\n\nForm type: `%s`",
				escapeFormMarkdown(field.Type),
			)
		}
		if dataField != nil {
			fmt.Fprintf(
				&markdown,
				"\n\nData property: `%s::$%s`",
				escapeFormMarkdown(dataField.Class),
				escapeFormMarkdown(dataField.Name),
			)
			if dataField.Type != "" {
				fmt.Fprintf(
					&markdown,
					"\n\nPHP type: `%s`",
					escapeFormMarkdown(dataField.Type),
				)
			}
			p.appendSource(&markdown, dataField.File)
		} else if field != nil {
			if field.PropertyPath != "" {
				fmt.Fprintf(
					&markdown,
					"\n\nProperty path: `%s`",
					escapeFormMarkdown(field.PropertyPath),
				)
			}
			if !field.Mapped {
				markdown.WriteString("\n\nUnmapped field")
			}
			p.appendSource(&markdown, field.File)
		}
	default:
		return nil, nil
	}

	rng := reference.Node.RangeTrimmedTrivia()
	startLine, startCharacter := request.LineIndex.PositionUTF16(rng.Start)
	endLine, endCharacter := request.LineIndex.PositionUTF16(rng.End)
	return &protocol.Hover{
		Contents: protocol.MarkupContent{
			Kind:  protocol.Markdown,
			Value: markdown.String(),
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
	}, nil
}

func (p *FormHoverProvider) twigViewVarHover(
	request *lsp.HoverRequest,
) (*protocol.Hover, error) {
	if p.phpIndex == nil || request.LineIndex == nil {
		return nil, nil
	}
	path, err := uriutil.Path(request.TextDocument.URI)
	if err != nil {
		return nil, nil
	}
	variables, err := form.TwigFormVariables(p.phpIndex, path)
	if err != nil {
		return nil, err
	}
	offset := request.LineIndex.OffsetUTF16(
		uint32(request.Position.Line),
		uint32(request.Position.Character),
	)
	reference, found := form.TwigViewVarContextAt(
		request.Node,
		offset,
		variables,
	)
	if !found || reference.Node == nil || reference.Name == "" {
		return nil, nil
	}
	var target *form.ViewVar
	for _, formType := range reference.FormTypes {
		viewVars, viewErr := p.index.EffectiveViewVars(formType)
		if viewErr != nil {
			return nil, viewErr
		}
		for index := range viewVars {
			if strings.EqualFold(viewVars[index].Name, reference.Name) {
				current := viewVars[index]
				target = &current
				break
			}
		}
		if target != nil {
			break
		}
	}
	if target == nil {
		return nil, nil
	}
	var markdown strings.Builder
	fmt.Fprintf(
		&markdown,
		"**Symfony FormView variable** `%s`",
		escapeFormMarkdown(target.Name),
	)
	if target.Type != "" {
		fmt.Fprintf(
			&markdown,
			"\n\nPHP type: `%s`",
			escapeFormMarkdown(target.Type),
		)
	}
	if target.Value != "" {
		fmt.Fprintf(
			&markdown,
			"\n\nAssigned value: `%s`",
			escapeFormMarkdown(target.Value),
		)
	}
	if target.Class != "" {
		fmt.Fprintf(
			&markdown,
			"\n\nDefined by `%s`",
			escapeFormMarkdown(target.Class),
		)
	}
	p.appendSource(&markdown, target.File)
	rng := reference.Node.RangeTrimmedTrivia()
	startLine, startCharacter := request.LineIndex.PositionUTF16(rng.Start)
	endLine, endCharacter := request.LineIndex.PositionUTF16(rng.End)
	return &protocol.Hover{
		Contents: protocol.MarkupContent{
			Kind:  protocol.Markdown,
			Value: markdown.String(),
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
	}, nil
}

func (p *FormHoverProvider) twigFieldReference(
	request *lsp.HoverRequest,
) (form.Reference, bool) {
	if p.phpIndex == nil || request.LineIndex == nil {
		return form.Reference{}, false
	}
	path, err := uriutil.Path(request.TextDocument.URI)
	if err != nil {
		return form.Reference{}, false
	}
	variables, err := form.TwigFormVariables(p.phpIndex, path)
	if err != nil {
		return form.Reference{}, false
	}
	offset := request.LineIndex.OffsetUTF16(
		uint32(request.Position.Line),
		uint32(request.Position.Character),
	)
	twigReference, found := form.TwigFieldContextAt(
		request.Node,
		offset,
		variables,
	)
	if !found || twigReference.Node == nil || twigReference.Name == "" {
		return form.Reference{}, false
	}
	for _, formType := range twigReference.FormTypes {
		reference := form.Reference{
			Role:     form.ReferenceField,
			Origin:   form.OriginFieldAccess,
			Name:     twigReference.Name,
			Node:     twigReference.Node,
			FormType: formType,
		}
		_, _, exists, fieldErr := p.formField(reference)
		if fieldErr == nil && exists {
			return reference, true
		}
	}
	return form.Reference{}, false
}

func (p *FormHoverProvider) formField(
	reference form.Reference,
) (*form.Field, *form.DataField, bool, error) {
	fields, err := p.index.EffectiveFields(reference.FormType)
	if err != nil {
		return nil, nil, false, err
	}
	var foundField *form.Field
	for index := range fields {
		if hoverFormFieldNameMatches(fields[index].Name, reference.Name) {
			current := fields[index]
			foundField = &current
			break
		}
	}
	dataFields, err := p.index.DataFieldsFor(reference.FormType)
	if err != nil {
		return foundField, nil, foundField != nil, err
	}
	for index := range dataFields {
		target := reference.Name
		if foundField != nil && foundField.PropertyPath != "" {
			target = foundField.PropertyPath
		}
		if hoverFormFieldNameMatches(dataFields[index].Name, target) {
			current := dataFields[index]
			return foundField, &current, true, nil
		}
	}
	return foundField, nil, foundField != nil, nil
}

func (p *FormHoverProvider) appendSource(
	markdown *strings.Builder,
	path string,
) {
	if path == "" {
		return
	}
	display, err := filepath.Rel(p.root, path)
	if err != nil {
		display = path
	}
	fmt.Fprintf(
		markdown,
		"\n\nDefined in `%s`",
		escapeFormMarkdown(filepath.ToSlash(display)),
	)
}

func findFormOption(
	options []form.Option,
	name string,
) (form.Option, bool) {
	for _, option := range options {
		if strings.EqualFold(option.Name, name) {
			return option, true
		}
	}
	return form.Option{}, false
}

func hoverFormFieldNameMatches(left, right string) bool {
	return strings.EqualFold(
		strings.ReplaceAll(left, "_", ""),
		strings.ReplaceAll(right, "_", ""),
	)
}

func escapeFormMarkdown(value string) string {
	return strings.ReplaceAll(value, "`", "\\`")
}
