package completion

import (
	"strings"

	"github.com/shopware/shopware-lsp/internal/admin"
	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	"github.com/shopware/shopware-lsp/internal/parser/cst"
	twigast "github.com/shopware/shopware-lsp/internal/parser/twig/ast"
	twigquery "github.com/shopware/shopware-lsp/internal/parser/twig/query"
	twigsyntax "github.com/shopware/shopware-lsp/internal/parser/twig/syntax"
)

func (p *AdminCompletionProvider) getResolvedSlotCompletions(
	node *twigsyntax.Node,
	content []byte,
	templatePath string,
	liveOwner *admin.VueComponent,
) ([]protocol.CompletionItem, bool) {
	if p.adminIndexer == nil || node == nil {
		return nil, false
	}
	attribute := twigquery.HTMLAttributeAt(node)
	if attribute == nil {
		return nil, false
	}
	attributeName := twigquery.HTMLAttributeName(attribute)
	if !strings.HasPrefix(attributeName, "#") &&
		!strings.HasPrefix(attributeName, "v-slot") {
		return nil, false
	}
	startTag := twigquery.StartingHTMLTagAt(attribute)
	components, complete, err :=
		p.adminIndexer.ResolveTwigSlotConsumerComponents(
			templatePath, startTag, liveOwner,
		)
	if err != nil || !complete || len(components) == 0 {
		return []protocol.CompletionItem{}, true
	}
	return p.getSlotCompletionsForComponents(components, node, content), true
}

func (p *AdminCompletionProvider) componentPropValueCompletions(
	params *lsp.CompletionRequest,
	offset uint32,
	templatePath string,
	liveOwner *admin.VueComponent,
) ([]protocol.CompletionItem, bool) {
	if p == nil || p.adminIndexer == nil || params == nil || params.Root == nil ||
		params.LineIndex == nil {
		return nil, false
	}
	startTag, propName, valueRange, found := componentPropValueContext(
		params.Root, params.DocumentContent, offset,
	)
	if !found {
		var field admin.VueObjectBindingField
		startTag, field, valueRange, found =
			admin.TwigComponentObjectBindingValueAtOffset(
				params.Root, offset,
			)
		propName = admin.NormalizePropName(field.Name)
	}
	if !found {
		return nil, false
	}
	components := p.componentsForPropValue(
		startTag, templatePath, liveOwner,
	)
	if len(components) == 0 {
		return nil, false
	}
	var common map[string]bool
	constrained := false
	complete := true
	for _, component := range components {
		prop, propFound := component.ComponentProp(propName)
		if !propFound {
			return nil, true
		}
		values, valuesComplete := admin.VuePropAllowedValues(prop)
		if len(values) == 0 {
			continue
		}
		complete = complete && valuesComplete
		current := make(map[string]bool, len(values))
		for _, value := range values {
			if value != "" {
				current[value] = true
			}
		}
		if !constrained {
			common = current
			constrained = true
			continue
		}
		for value := range common {
			if !current[value] {
				delete(common, value)
			}
		}
	}
	if !constrained {
		return nil, true
	}
	startLine, startCharacter := params.LineIndex.PositionUTF16(valueRange.Start)
	endLine, endCharacter := params.LineIndex.PositionUTF16(valueRange.End)
	editRange := protocol.Range{
		Start: protocol.Position{
			Line: int(startLine), Character: int(startCharacter),
		},
		End: protocol.Position{
			Line: int(endLine), Character: int(endCharacter),
		},
	}
	detail := "component prop value • " + propName
	if !complete {
		detail = "known component prop value • " + propName
	}
	if len(components) > 1 {
		detail += " • all dynamic candidates"
	}
	items := make([]protocol.CompletionItem, 0, len(common))
	for value := range common {
		items = append(items, protocol.CompletionItem{
			Label: value, Kind: int(protocol.ValueCompletion), Detail: detail,
			TextEdit: protocol.TextEdit{Range: editRange, NewText: value},
		})
	}
	sortCompletionItems(items)
	return items, true
}

func (p *AdminCompletionProvider) componentsForPropValue(
	startTag *twigsyntax.Node,
	templatePath string,
	liveOwner *admin.VueComponent,
) []admin.VueComponent {
	if selector, dynamic := admin.TwigDynamicComponentSelector(startTag); dynamic {
		_, components, complete, err :=
			p.adminIndexer.ResolveDynamicComponentContractsForOwner(
				templatePath, selector, liveOwner, startTag,
			)
		if err != nil || !complete {
			return nil
		}
		return components
	}
	name, found := admin.StaticComponentNameForTag(startTag)
	if !found {
		return nil
	}
	component, found, err := p.adminIndexer.GetComponentForTemplateTag(
		templatePath, name, liveOwner,
	)
	if err != nil || !found || component == nil {
		return nil
	}
	return []admin.VueComponent{*component}
}

func componentPropValueContext(
	root *twigsyntax.Node,
	content []byte,
	offset uint32,
) (*twigsyntax.Node, string, cst.TextRange, bool) {
	if root == nil {
		return nil, "", cst.TextRange{}, false
	}
	for _, startTag := range twigquery.Nodes(root, twigsyntax.HtmlStartingTag) {
		if offset < startTag.Range().Start || offset > startTag.Range().End {
			continue
		}
		tag, ok := twigast.CastHtmlStartingTag(startTag)
		if !ok {
			continue
		}
		for _, attribute := range tag.Attributes() {
			name := twigquery.HTMLAttributeName(attribute.Syntax())
			bound := strings.HasPrefix(name, ":") ||
				strings.HasPrefix(name, "v-bind:")
			if name == "" || strings.HasPrefix(name, "@") ||
				strings.HasPrefix(name, "#") ||
				strings.HasPrefix(name, "v-") && !bound {
				continue
			}
			if selector, dynamic := admin.TwigDynamicComponentSelector(startTag); dynamic &&
				name == selector.AttributeName {
				continue
			}
			value, valueOK := attribute.Value()
			if !valueOK {
				continue
			}
			valueRange := cst.TextRange{}
			valueText := ""
			if inner, innerOK := value.GetInner(); innerOK {
				if bound {
					_, contentStart, contentEnd, literal :=
						admin.VueStaticStringLiteral(inner.Syntax().Text())
					if !literal {
						continue
					}
					valueRange = cst.TextRange{
						Start: inner.Syntax().Range().Start + contentStart,
						End:   inner.Syntax().Range().Start + contentEnd,
					}
				} else {
					valueRange = inner.Syntax().Range()
					valueText = inner.Syntax().Text()
				}
			} else {
				if bound {
					continue
				}
				opening := value.GetOpeningQuote()
				closing := value.GetClosingQuote()
				if opening == nil || closing == nil {
					continue
				}
				valueRange = cst.TextRange{
					Start: opening.Range().End, End: closing.Range().Start,
				}
			}
			if offset < valueRange.Start || offset > valueRange.End ||
				strings.Contains(valueText, "{{") ||
				strings.Contains(valueText, "{%") ||
				strings.Contains(valueText, "{#") ||
				valueRange.End > uint32(len(content)) {
				continue
			}
			propName := admin.NormalizePropName(name)
			if propName == "" {
				continue
			}
			return startTag, propName, valueRange, true
		}
	}
	return nil, "", cst.TextRange{}, false
}

func (p *AdminCompletionProvider) objectBindingPropCompletions(
	startTag *twigsyntax.Node,
	fields []admin.VueObjectBindingField,
	templatePath string,
	liveOwner *admin.VueComponent,
) []protocol.CompletionItem {
	if p == nil || p.adminIndexer == nil || startTag == nil {
		return nil
	}
	var components []admin.VueComponent
	if selector, dynamic := admin.TwigDynamicComponentSelector(startTag); dynamic {
		_, resolved, complete, err :=
			p.adminIndexer.ResolveDynamicComponentContractsForOwner(
				templatePath, selector, liveOwner, startTag,
			)
		if err != nil || !complete {
			return nil
		}
		components = resolved
	} else {
		name, found := admin.StaticComponentNameForTag(startTag)
		if !found {
			return nil
		}
		component, found, err := p.adminIndexer.GetComponentForTemplateTag(
			templatePath, name, liveOwner,
		)
		if err != nil || !found || component == nil {
			return nil
		}
		components = []admin.VueComponent{*component}
	}
	if len(components) == 0 {
		return nil
	}
	present := make(map[string]bool, len(fields))
	for _, field := range fields {
		present[admin.NormalizePropName(field.Name)] = true
	}
	common := make(map[string][]admin.VueComponentProp)
	for index, component := range components {
		current := make(map[string]admin.VueComponentProp, len(component.Props))
		for _, prop := range component.Props {
			current[prop.Name] = prop
		}
		if index == 0 {
			for name, prop := range current {
				common[name] = []admin.VueComponentProp{prop}
			}
			continue
		}
		for name, values := range common {
			prop, found := current[name]
			if !found {
				delete(common, name)
				continue
			}
			common[name] = append(values, prop)
		}
	}
	items := make([]protocol.CompletionItem, 0, len(common))
	for name, props := range common {
		if present[name] {
			continue
		}
		types := make([]string, 0, len(props))
		seenTypes := make(map[string]bool)
		required := true
		for _, prop := range props {
			typeName := strings.TrimSpace(prop.Type)
			if typeName != "" && !seenTypes[typeName] {
				seenTypes[typeName] = true
				types = append(types, typeName)
			}
			required = required && prop.Required
		}
		detail := strings.Join(types, " | ")
		if len(components) > 1 {
			detail = strings.TrimSpace(detail + " • all dynamic candidates")
		}
		item := protocol.CompletionItem{
			Label: name, Kind: int(protocol.PropertyCompletion), Detail: detail,
			InsertText: name + ": $0", InsertTextFormat: int(protocol.SnippetTextFormat),
		}
		item.Documentation.Kind = string(protocol.Markdown)
		item.Documentation.Value = "Forward component prop `" + name + "` through `v-bind`."
		if required {
			item.Documentation.Value += "\n\nRequired by every candidate contract."
		}
		markAdminCompletionDeprecated(&item, commonAdminPropDeprecation(props))
		items = append(items, item)
	}
	sortCompletionItems(items)
	return items
}

func (p *AdminCompletionProvider) dynamicComponentAttributeCompletions(
	node *twigsyntax.Node,
	templatePath string,
	liveOwner *admin.VueComponent,
) ([]protocol.CompletionItem, bool) {
	if p == nil || p.adminIndexer == nil || node == nil {
		return nil, false
	}
	startTag := twigquery.StartingHTMLTagAt(node)
	selector, dynamic := admin.TwigDynamicComponentSelector(startTag)
	if !dynamic {
		return nil, false
	}
	_, components, complete, err :=
		p.adminIndexer.ResolveDynamicComponentContractsForOwner(
			templatePath, selector, liveOwner, startTag,
		)
	if err != nil || !complete || len(components) == 0 {
		return nil, true
	}
	common := make(map[string]protocol.CompletionItem)
	for index, component := range components {
		items := p.getComponentPropCompletionsForOwner(
			component.Name, templatePath, liveOwner,
		)
		current := make(map[string]protocol.CompletionItem, len(items))
		for _, item := range items {
			current[item.Label] = item
		}
		if index == 0 {
			common = current
			continue
		}
		for label := range common {
			if _, found := current[label]; !found {
				delete(common, label)
			}
		}
	}
	result := make([]protocol.CompletionItem, 0, len(common))
	for _, item := range common {
		item.Detail = strings.TrimSpace(item.Detail + " • all dynamic candidates")
		result = append(result, item)
	}
	sortCompletionItems(result)
	return result, true
}

func adminDynamicComponentSelectorAtCompletion(
	params *lsp.CompletionRequest,
	offset uint32,
	hasOffset bool,
) (admin.VueDynamicComponentSelector, bool) {
	if params == nil || params.Node == nil || !hasOffset {
		return admin.VueDynamicComponentSelector{}, false
	}
	attribute := twigquery.HTMLAttributeAt(params.Node)
	startTag := twigquery.StartingHTMLTagAt(params.Node)
	if attribute == nil || startTag == nil {
		return admin.VueDynamicComponentSelector{}, false
	}
	selector, found := admin.TwigDynamicComponentSelector(startTag)
	if !found || twigquery.HTMLAttributeName(attribute) != selector.AttributeName {
		return admin.VueDynamicComponentSelector{}, false
	}
	if selector.ExpressionRange.Len() > 0 &&
		(offset < selector.ExpressionRange.Start || offset > selector.ExpressionRange.End) {
		return admin.VueDynamicComponentSelector{}, false
	}
	return selector, true
}
