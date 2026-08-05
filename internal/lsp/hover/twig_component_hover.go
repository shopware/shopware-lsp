package hover

import (
	"context"
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	"github.com/shopware/shopware-lsp/internal/parser/cst"
	"github.com/shopware/shopware-lsp/internal/twigcomponent"
	"github.com/shopware/shopware-lsp/internal/uriutil"
)

type TwigComponentHoverProvider struct {
	root  string
	index *twigcomponent.Index
}

func NewTwigComponentHoverProvider(
	root string,
	index *twigcomponent.Index,
) *TwigComponentHoverProvider {
	return &TwigComponentHoverProvider{root: root, index: index}
}

func (p *TwigComponentHoverProvider) GetHover(
	_ context.Context,
	request *lsp.HoverRequest,
) (*protocol.Hover, error) {
	if p == nil || p.index == nil || request == nil ||
		request.Root == nil || request.LineIndex == nil ||
		!strings.HasSuffix(
			strings.ToLower(request.TextDocument.URI),
			".twig",
		) {
		return nil, nil
	}
	offset := request.LineIndex.OffsetUTF16(
		uint32(request.Position.Line),
		uint32(request.Position.Character),
	)
	path, _ := uriutil.Path(request.TextDocument.URI)
	if argument, found := twigcomponent.LiveActionArgumentReferenceAt(
		path,
		request.Root,
		offset,
	); found {
		if result, argumentErr := p.liveActionArgumentHover(
			path,
			argument,
			request,
		); result != nil || argumentErr != nil {
			return result, argumentErr
		}
	}
	if action, found := twigcomponent.LiveActionReferenceAt(
		path,
		request.Root,
		offset,
	); found && action.Name != "" {
		if result, actionErr := p.liveActionHover(
			path,
			action,
			request,
		); result != nil || actionErr != nil {
			return result, actionErr
		}
	}
	if root, member, rng, found := twigcomponent.AccessorMemberAt(
		request.Node,
	); found && root == "computed" {
		if result, computedErr := p.computedHover(
			path,
			member,
			rng,
			request,
		); result != nil || computedErr != nil {
			return result, computedErr
		}
	}
	if name, rng, variable := twigcomponent.VariableAt(
		request.Node,
	); variable {
		if result, variableErr := p.variableHover(
			path,
			name,
			rng,
			request,
		); result != nil || variableErr != nil {
			return result, variableErr
		}
	}
	if block, found := twigcomponent.BlockUsageAt(
		request.Node,
		offset,
	); found {
		return p.blockHover(block, request)
	}
	usage, found := twigcomponent.UsageAt(
		path,
		request.Root,
		offset,
	)
	if !found {
		prop, propFound := twigcomponent.PropUsageAt(
			request.Root,
			request.Node,
			offset,
		)
		if !propFound {
			return nil, nil
		}
		return p.propHover(prop, request)
	}
	components, err := p.index.Find(usage.Name)
	if err != nil || len(components) == 0 {
		return nil, err
	}
	var classes []string
	var templates []string
	var templateMethods []string
	compiled := false
	for _, component := range components {
		compiled = compiled ||
			component.Source == twigcomponent.CompiledContainerSource
		if component.Class != "" &&
			!containsComponentString(classes, component.Class) {
			classes = append(classes, component.Class)
		}
		if component.Template != "" &&
			!containsComponentString(templates, component.Template) {
			templates = append(templates, component.Template)
		}
		if component.TemplateFromMethod != "" &&
			!containsComponentString(
				templateMethods,
				component.TemplateFromMethod,
			) {
			templateMethods = append(
				templateMethods,
				component.TemplateFromMethod,
			)
		}
	}
	sort.Strings(classes)
	sort.Strings(templates)
	sort.Strings(templateMethods)
	props, _ := p.index.Props(usage.Name)
	usages, _ := p.index.Usages(usage.Name)

	var markdown strings.Builder
	fmt.Fprintf(
		&markdown,
		"**Twig component** `%s`",
		escapeComponentMarkdown(usage.Name),
	)
	for _, component := range components {
		if component.Live {
			fmt.Fprint(&markdown, "\n\nLive component")
			break
		}
	}
	if compiled {
		fmt.Fprint(
			&markdown,
			"\n\nRuntime metadata: compiled Symfony container",
		)
	}
	for _, class := range classes {
		fmt.Fprintf(
			&markdown,
			"\n\nPHP class: `%s`",
			escapeComponentMarkdown(class),
		)
	}
	for _, template := range templates {
		fmt.Fprintf(
			&markdown,
			"\n\nTemplate: `%s`",
			escapeComponentMarkdown(template),
		)
	}
	for _, method := range templateMethods {
		fmt.Fprintf(
			&markdown,
			"\n\nDynamic template method: `%s`",
			escapeComponentMarkdown(method),
		)
	}
	if len(props) != 0 {
		fmt.Fprintf(&markdown, "\n\n%d prop(s)", len(props))
	}
	if len(usages) != 0 {
		fmt.Fprintf(&markdown, "\n\n%d indexed use(s)", len(usages))
	}
	if len(components) == 1 && components[0].File != "" {
		display := components[0].File
		if relative, relativeErr := filepath.Rel(
			p.root,
			display,
		); relativeErr == nil {
			display = relative
		}
		fmt.Fprintf(
			&markdown,
			"\n\nDiscovered from `%s`",
			escapeComponentMarkdown(filepath.ToSlash(display)),
		)
	}
	return &protocol.Hover{
		Contents: protocol.MarkupContent{
			Kind:  protocol.Markdown,
			Value: markdown.String(),
		},
		Range: securityProtocolRange(usage.Range, request.LineIndex),
	}, nil
}

func (p *TwigComponentHoverProvider) liveActionArgumentHover(
	path string,
	reference twigcomponent.LiveActionArgumentReference,
	request *lsp.HoverRequest,
) (*protocol.Hover, error) {
	actions, err := p.index.LiveActionsForTemplate(path)
	if err != nil {
		return nil, err
	}
	for _, action := range actions {
		if !strings.EqualFold(action.Name, reference.Action) {
			continue
		}
		for _, parameter := range action.Parameters {
			if !strings.EqualFold(parameter.Name, reference.Name) {
				continue
			}
			value := fmt.Sprintf(
				"**Live Action argument** `%s`\n\nAction: `%s::%s()`",
				escapeComponentMarkdown(parameter.Name),
				escapeComponentMarkdown(action.Class),
				escapeComponentMarkdown(action.Method),
			)
			if parameter.Type != "" {
				value += fmt.Sprintf(
					"\n\nPHP type: `%s`",
					escapeComponentMarkdown(parameter.Type),
				)
			}
			if parameter.Name != parameter.PHPName {
				value += fmt.Sprintf(
					"\n\nMapped to PHP parameter `$%s` via `#[LiveArg]`",
					escapeComponentMarkdown(parameter.PHPName),
				)
			}
			return &protocol.Hover{
				Contents: protocol.MarkupContent{
					Kind:  protocol.Markdown,
					Value: value,
				},
				Range: securityProtocolRange(
					reference.Range,
					request.LineIndex,
				),
			}, nil
		}
	}
	return nil, nil
}

func (p *TwigComponentHoverProvider) liveActionHover(
	path string,
	reference twigcomponent.LiveActionReference,
	request *lsp.HoverRequest,
) (*protocol.Hover, error) {
	actions, err := p.index.LiveActionsForTemplate(path)
	if err != nil {
		return nil, err
	}
	for _, action := range actions {
		if !strings.EqualFold(action.Name, reference.Name) {
			continue
		}
		var signature strings.Builder
		fmt.Fprintf(
			&signature,
			"%s::%s(",
			action.Class,
			action.Method,
		)
		for index, parameter := range action.Parameters {
			if index != 0 {
				signature.WriteString(", ")
			}
			if parameter.Type != "" {
				signature.WriteString(parameter.Type)
				signature.WriteByte(' ')
			}
			phpName := parameter.PHPName
			if phpName == "" {
				phpName = parameter.Name
			}
			signature.WriteByte('$')
			signature.WriteString(phpName)
			if parameter.Optional {
				signature.WriteString(" = …")
			}
		}
		signature.WriteByte(')')
		return &protocol.Hover{
			Contents: protocol.MarkupContent{
				Kind: protocol.Markdown,
				Value: fmt.Sprintf(
					"**Symfony UX Live Action** `%s`\n\n```php\n%s\n```",
					escapeComponentMarkdown(action.Name),
					signature.String(),
				),
			},
			Range: securityProtocolRange(
				reference.Range,
				request.LineIndex,
			),
		}, nil
	}
	return nil, nil
}

func (p *TwigComponentHoverProvider) blockHover(
	usage twigcomponent.ComponentBlockUsage,
	request *lsp.HoverRequest,
) (*protocol.Hover, error) {
	blocks, err := p.index.Blocks(usage.Component)
	if err != nil {
		return nil, err
	}
	for _, block := range blocks {
		if block.Name != usage.Name {
			continue
		}
		display := block.File
		if relative, relativeErr := filepath.Rel(
			p.root,
			display,
		); relativeErr == nil {
			display = relative
		}
		return &protocol.Hover{
			Contents: protocol.MarkupContent{
				Kind: protocol.Markdown,
				Value: fmt.Sprintf(
					"**Twig component block** `%s.%s`\n\nDeclared in `%s`",
					escapeComponentMarkdown(usage.Component),
					escapeComponentMarkdown(usage.Name),
					escapeComponentMarkdown(
						filepath.ToSlash(display),
					),
				),
			},
			Range: securityProtocolRange(
				usage.Range,
				request.LineIndex,
			),
		}, nil
	}
	return nil, nil
}

func (p *TwigComponentHoverProvider) variableHover(
	path,
	name string,
	rng cst.TextRange,
	request *lsp.HoverRequest,
) (*protocol.Hover, error) {
	components, props, err := p.index.ContextForTemplate(
		path,
		request.Root,
	)
	if err != nil || len(components) == 0 {
		return nil, err
	}
	if name == "this" {
		for _, component := range components {
			if component.Class == "" {
				continue
			}
			return &protocol.Hover{
				Contents: protocol.MarkupContent{
					Kind: protocol.Markdown,
					Value: fmt.Sprintf(
						"**Twig component object** `this`\n\nPHP type: `%s`",
						escapeComponentMarkdown(component.Class),
					),
				},
				Range: securityProtocolRange(rng, request.LineIndex),
			}, nil
		}
		return nil, nil
	}
	if name == "computed" {
		return &protocol.Hover{
			Contents: protocol.MarkupContent{
				Kind: protocol.Markdown,
				Value: "**Twig computed proxy** `computed`\n\n" +
					"Provides cached access to zero-argument component getters.",
			},
			Range: securityProtocolRange(rng, request.LineIndex),
		}, nil
	}
	for _, prop := range props {
		if !strings.EqualFold(prop.Name, name) {
			continue
		}
		return componentVariablePropHover(prop, rng, request), nil
	}
	return nil, nil
}

func (p *TwigComponentHoverProvider) computedHover(
	path,
	name string,
	rng cst.TextRange,
	request *lsp.HoverRequest,
) (*protocol.Hover, error) {
	computed, err := p.index.ComputedForTemplate(path)
	if err != nil {
		return nil, err
	}
	for _, prop := range computed {
		if !strings.EqualFold(prop.Name, name) {
			continue
		}
		value := fmt.Sprintf(
			"**Computed component value** `%s`",
			escapeComponentMarkdown(prop.Name),
		)
		if prop.Type != "" {
			value += fmt.Sprintf(
				"\n\nPHP type: `%s`",
				escapeComponentMarkdown(prop.Type),
			)
		}
		value += "\n\nThe getter result is cached for this component render."
		return &protocol.Hover{
			Contents: protocol.MarkupContent{
				Kind:  protocol.Markdown,
				Value: value,
			},
			Range: securityProtocolRange(rng, request.LineIndex),
		}, nil
	}
	return nil, nil
}

func componentVariablePropHover(
	prop twigcomponent.Prop,
	rng cst.TextRange,
	request *lsp.HoverRequest,
) *protocol.Hover {
	var markdown strings.Builder
	fmt.Fprintf(
		&markdown,
		"**Twig component variable** `%s`",
		escapeComponentMarkdown(prop.Name),
	)
	if prop.Type != "" {
		fmt.Fprintf(
			&markdown,
			"\n\nPHP/Twig type: `%s`",
			escapeComponentMarkdown(prop.Type),
		)
	}
	if prop.DefaultValue != "" {
		fmt.Fprintf(
			&markdown,
			"\n\nDefault: `%s`",
			escapeComponentMarkdown(prop.DefaultValue),
		)
	}
	if prop.Description != "" {
		fmt.Fprintf(&markdown, "\n\n%s", prop.Description)
	}
	if prop.Live {
		if prop.Writable {
			fmt.Fprint(
				&markdown,
				"\n\nLive prop: writable from the browser.",
			)
		} else {
			fmt.Fprint(&markdown, "\n\nLive prop: read-only.")
		}
	}
	return &protocol.Hover{
		Contents: protocol.MarkupContent{
			Kind:  protocol.Markdown,
			Value: markdown.String(),
		},
		Range: securityProtocolRange(rng, request.LineIndex),
	}
}

func (p *TwigComponentHoverProvider) propHover(
	usage twigcomponent.PropUsage,
	request *lsp.HoverRequest,
) (*protocol.Hover, error) {
	props, err := p.index.Props(usage.Component)
	if err != nil {
		return nil, err
	}
	for _, prop := range props {
		if !strings.EqualFold(prop.Name, usage.Name) {
			continue
		}
		var markdown strings.Builder
		fmt.Fprintf(
			&markdown,
			"**Twig component prop** `%s.%s`",
			escapeComponentMarkdown(usage.Component),
			escapeComponentMarkdown(prop.Name),
		)
		if prop.Type != "" {
			fmt.Fprintf(
				&markdown,
				"\n\nType: `%s`",
				escapeComponentMarkdown(prop.Type),
			)
		}
		if prop.DefaultValue != "" {
			fmt.Fprintf(
				&markdown,
				"\n\nDefault: `%s`",
				escapeComponentMarkdown(prop.DefaultValue),
			)
		}
		if prop.Description != "" {
			fmt.Fprintf(
				&markdown,
				"\n\n%s",
				prop.Description,
			)
		}
		if prop.Live {
			if prop.Writable {
				fmt.Fprint(
					&markdown,
					"\n\nLive prop: writable from the browser.",
				)
			} else {
				fmt.Fprint(&markdown, "\n\nLive prop: read-only.")
			}
		}
		return &protocol.Hover{
			Contents: protocol.MarkupContent{
				Kind:  protocol.Markdown,
				Value: markdown.String(),
			},
			Range: securityProtocolRange(
				usage.Range,
				request.LineIndex,
			),
		}, nil
	}
	return nil, nil
}

func containsComponentString(values []string, candidate string) bool {
	for _, value := range values {
		if value == candidate {
			return true
		}
	}
	return false
}

func escapeComponentMarkdown(value string) string {
	return strings.ReplaceAll(value, "`", "\\`")
}
