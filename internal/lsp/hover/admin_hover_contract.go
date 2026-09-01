package hover

import (
	"fmt"
	"strings"

	"github.com/shopware/shopware-lsp/internal/admin"
	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	twigquery "github.com/shopware/shopware-lsp/internal/parser/twig/query"
	twigsyntax "github.com/shopware/shopware-lsp/internal/parser/twig/syntax"
	"github.com/shopware/shopware-lsp/internal/uriutil"
)

func (p *AdminHoverProvider) componentEventHover(
	attribute *twigsyntax.Node,
	templatePath string,
	owners ...*admin.VueComponent,
) (*protocol.Hover, error) {
	eventName := admin.NormalizeEventName(
		twigquery.HTMLAttributeName(attribute),
	)
	startTag := twigquery.StartingHTMLTagAt(attribute)
	components, err := p.componentsForMarkupTag(
		startTag, templatePath, owners...,
	)
	if err != nil {
		return nil, err
	}
	var sections []string
	for _, component := range components {
		event, found := component.ComponentEvent(eventName)
		if !found {
			continue
		}
		value := "**event** `" + admin.CanonicalEventName(event.Name) + "`"
		if event.Type != "" {
			value += ": `" + event.Type + "`"
		}
		value += "\n\nEmitted by Administration component `" + component.Name + "`."
		if documentation := strings.TrimSpace(event.Documentation); documentation != "" {
			value += "\n\n" + documentation
		}
		if event.FilePath != "" {
			path := p.makeRelativePath(event.FilePath)
			if event.Line > 0 {
				value += fmt.Sprintf("\n\nDefined in `%s:%d`.", path, event.Line)
			} else {
				value += fmt.Sprintf("\n\nDefined in `%s`.", path)
			}
		}
		sections = append(sections, value)
	}
	if len(sections) == 0 {
		return nil, nil
	}
	return &protocol.Hover{Contents: protocol.MarkupContent{
		Kind: protocol.Markdown, Value: strings.Join(sections, "\n\n---\n\n"),
	}}, nil
}

func (p *AdminHoverProvider) componentSlotHover(
	attribute *twigsyntax.Node,
	templatePath string,
	owners ...*admin.VueComponent,
) (*protocol.Hover, error) {
	attributeName := twigquery.HTMLAttributeName(attribute)
	slotName := admin.NormalizeSlotName(attributeName)
	startTag := twigquery.StartingHTMLTagAt(attribute)
	if slotName == "" || startTag == nil {
		return nil, nil
	}
	components, complete, err :=
		p.adminIndexer.ResolveTwigSlotConsumerComponents(
			templatePath, startTag, owners...,
		)
	if err != nil || !complete {
		return nil, err
	}
	sections := make([]string, 0, len(components))
	seen := make(map[string]bool)
	for _, component := range components {
		slot, found := component.ComponentSlot(slotName)
		if !found {
			continue
		}
		key := component.Name + "\x00" + slot.FilePath + "\x00" + slot.Name
		if seen[key] {
			continue
		}
		seen[key] = true
		value := "**slot** `" + slotName + "`\n\nProvided by Administration component `" + component.Name + "`."
		if slot.IsDynamicName() {
			value += "\n\nDynamic slot family: `" + slot.DisplayName() + "`."
		}
		if len(slot.Members) > 0 {
			value += "\n\nScoped payload:"
			for _, member := range slot.Members {
				value += "\n\n- `" + member.Name + "`"
				if member.Type != "" {
					value += ": `" + member.Type + "`"
				}
			}
		} else if slot.PayloadType != "" {
			value += "\n\nScoped payload: `" + slot.PayloadType + "`"
		}
		if slot.FilePath != "" {
			value += fmt.Sprintf(
				"\n\nDefined in `%s:%d`.",
				p.makeRelativePath(slot.FilePath), slot.Line,
			)
		}
		sections = append(sections, value)
	}
	if len(sections) == 0 {
		return nil, nil
	}
	return &protocol.Hover{Contents: protocol.MarkupContent{
		Kind: protocol.Markdown, Value: strings.Join(sections, "\n\n---\n\n"),
	}}, nil
}

func (p *AdminHoverProvider) componentPropHover(
	attribute *twigsyntax.Node,
	templatePath string,
	owners ...*admin.VueComponent,
) (*protocol.Hover, error) {
	name := admin.NormalizePropName(twigquery.HTMLAttributeName(attribute))
	if name == "" {
		return nil, nil
	}
	startTag := twigquery.StartingHTMLTagAt(attribute)
	return p.componentPropHoverByName(
		startTag, name, templatePath, owners...,
	)
}

func (p *AdminHoverProvider) componentPropHoverByName(
	startTag *twigsyntax.Node,
	name,
	templatePath string,
	owners ...*admin.VueComponent,
) (*protocol.Hover, error) {
	if name == "" || startTag == nil {
		return nil, nil
	}
	components, err := p.componentsForMarkupTag(
		startTag, templatePath, owners...,
	)
	if err != nil {
		return nil, err
	}
	var sections []string
	for _, component := range components {
		for _, prop := range component.Props {
			if prop.Name != name {
				continue
			}
			sections = append(
				sections, adminPropMarkdown(component.Name, prop),
			)
		}
	}
	if len(sections) == 0 {
		return nil, nil
	}
	return &protocol.Hover{Contents: protocol.MarkupContent{
		Kind: protocol.Markdown, Value: strings.Join(sections, "\n\n---\n\n"),
	}}, nil
}

func (p *AdminHoverProvider) componentsForMarkupTag(
	startTag *twigsyntax.Node,
	templatePath string,
	owners ...*admin.VueComponent,
) ([]admin.VueComponent, error) {
	if p == nil || p.adminIndexer == nil || startTag == nil {
		return nil, nil
	}
	if selector, dynamic := admin.TwigDynamicComponentSelector(startTag); dynamic {
		_, components, complete, err :=
			p.adminIndexer.ResolveDynamicComponentContractsForOwner(
				templatePath, selector, firstHoverOwner(owners), startTag,
			)
		if err != nil || !complete {
			return nil, err
		}
		return components, nil
	}
	name, found := admin.StaticComponentNameForTag(startTag)
	if !found {
		return nil, nil
	}
	component, found, err := p.adminIndexer.GetComponentForTemplateTag(
		templatePath, name, owners...,
	)
	if err != nil || !found || component == nil {
		return nil, err
	}
	return []admin.VueComponent{*component}, nil
}

func (p *AdminHoverProvider) templateMemberHover(
	params *lsp.HoverRequest,
) (*protocol.Hover, error) {
	name := ""
	if twigquery.ClosestNodeOfKind(params.Node, twigsyntax.TwigVar) != nil {
		name = adminHoverTemplateRootName(
			params.Node,
			params.Token,
			params.DocumentContent,
		)
	} else if params.LineIndex != nil && params.HoverParams != nil {
		offset := params.LineIndex.OffsetUTF16(
			uint32(params.Position.Line), uint32(params.Position.Character),
		)
		name, _, _ = admin.ExpressionRootIdentifierAtOffset(
			params.DocumentContent, offset,
		)
	}
	if name == "" {
		return nil, nil
	}
	path, err := uriutil.Path(params.TextDocument.URI)
	if err != nil {
		return nil, nil
	}
	component, err := p.adminIndexer.GetComponentForDocument(
		path, params.Root, params.SourceString(), params.LineIndex,
	)
	if err != nil || component == nil {
		return nil, err
	}
	if scopeMember, block, scoped := admin.TwigBlockScopeMemberAt(
		*component, params.Node, name,
	); scoped {
		value := "**Twig block scope** `" + scopeMember.Name + "`"
		if scopeMember.Type != "" {
			value += ": `" + scopeMember.Type + "`"
		}
		value += "\n\nProvided by Administration Twig block `" +
			block.Name + "`."
		if scopeMember.FilePath != "" {
			value += fmt.Sprintf(
				"\n\nDeclared in `%s:%d`.",
				p.makeRelativePath(scopeMember.FilePath), scopeMember.Line,
			)
		}
		return &protocol.Hover{Contents: protocol.MarkupContent{
			Kind: protocol.Markdown, Value: value,
		}}, nil
	}
	member, found := component.TemplateMember(name)
	origin := "Administration component `" + component.Name + "`"
	componentMember := found
	if !found {
		if builtin, builtinFound := admin.VueBuiltinMember(name); builtinFound {
			member = builtin
			found = true
			origin = "the Administration Vue component instance"
		} else if global, globalFound := admin.VueTemplateGlobal(name); globalFound {
			member = global
			found = true
			origin = "the JavaScript template runtime"
		}
	}
	if !found {
		return nil, nil
	}
	value := fmt.Sprintf("**%s** `%s`", member.Kind, member.Name)
	if member.Type != "" {
		value += ": `" + member.Type + "`"
	}
	value += "\n\nProvided by " + origin + "."
	if componentMember && member.Kind == admin.ComponentMemberProp {
		for _, prop := range component.Props {
			if prop.Name == member.Name {
				value = adminPropMarkdown(component.Name, prop)
				break
			}
		}
	} else if member.Deprecated != "" {
		value += "\n\n**Deprecated:** " + member.Deprecated
	}
	return &protocol.Hover{Contents: protocol.MarkupContent{
		Kind: protocol.Markdown, Value: value,
	}}, nil
}

func adminPropMarkdown(componentName string, prop admin.VueComponentProp) string {
	value := "**prop** `" + prop.Name + "`"
	if prop.Type != "" {
		value += ": `" + prop.Type + "`"
	}
	value += "\n\nComponent: `" + componentName + "`"
	if prop.Deprecated != "" {
		value += "\n\n**Deprecated:** " + prop.Deprecated
	}
	if documentation := strings.TrimSpace(prop.Documentation); documentation != "" {
		value += "\n\n" + documentation
	}
	if prop.Required {
		value += "\n\nRequired."
	}
	if prop.Default != "" {
		value += "\n\nDefault: `" + prop.Default + "`"
	}
	if values, complete := admin.VuePropAllowedValues(prop); len(values) > 0 {
		label := "Allowed values: "
		if !complete {
			label = "Known values: "
		}
		formatted := make([]string, 0, len(values))
		for _, allowed := range values {
			display := allowed
			if display == "" {
				display = "(empty)"
			}
			formatted = append(
				formatted,
				"`"+strings.ReplaceAll(display, "`", "\\`")+"`",
			)
		}
		value += "\n\n" + label + strings.Join(formatted, ", ")
	}
	return value
}

func adminHoverTemplateRootName(
	node *twigsyntax.Node,
	token *twigsyntax.Token,
	content []byte,
) string {
	if node == nil || token == nil {
		return ""
	}
	accessor := twigquery.ClosestNodeOfKind(node, twigsyntax.TwigAccessor)
	if accessor != nil {
		start := accessor.RangeTrimmedTrivia().Start
		end := token.Range().Start
		if start < end && int(end) <= len(content) &&
			strings.Contains(string(content[start:end]), ".") {
			return ""
		}
	}
	return strings.TrimSpace(token.Text())
}
