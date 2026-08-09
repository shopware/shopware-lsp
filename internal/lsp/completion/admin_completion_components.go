package completion

import (
	"path/filepath"
	"strings"

	"github.com/shopware/shopware-lsp/internal/admin"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	jsquery "github.com/shopware/shopware-lsp/internal/parser/javascript/query"
	jssyntax "github.com/shopware/shopware-lsp/internal/parser/javascript/syntax"
	twigquery "github.com/shopware/shopware-lsp/internal/parser/twig/query"
	twigsyntax "github.com/shopware/shopware-lsp/internal/parser/twig/syntax"
)

func (p *AdminCompletionProvider) isInHTMLTagName(node *twigsyntax.Node, token *twigsyntax.Token, content []byte) bool {
	if node == nil || token == nil {
		return false
	}

	parent := token.Parent()
	if parent != nil && (parent.Kind() == twigsyntax.HtmlStartingTag || parent.Kind() == twigsyntax.HtmlEndingTag) {
		for child := range parent.ChildTokens() {
			if (child.Kind() == twigsyntax.TkWord || child.Kind() == twigsyntax.TkTwigComponentName) &&
				child.Range() == token.Range() {
				return true
			}
		}
	}

	if token.Kind() == twigsyntax.TkLessThan || token.Kind() == twigsyntax.TkLessThanSlash {
		return true
	}

	beforeCursor := content
	if end := int(token.Range().End); end < len(beforeCursor) {
		beforeCursor = beforeCursor[:end]
	}
	lastLT := strings.LastIndex(string(beforeCursor), "<")
	if lastLT >= 0 {
		partial := string(beforeCursor[lastLT+1:])
		return !strings.ContainsAny(partial, "> \t\n\r")
	}

	return false
}

// getComponentTagCompletions returns completion items for component tags in Twig
func (p *AdminCompletionProvider) getComponentTagCompletions(
	templatePaths ...string,
) []protocol.CompletionItem {
	return p.getComponentTagCompletionsForOwner(
		optionalTemplatePath(templatePaths), nil,
	)
}

func (p *AdminCompletionProvider) getComponentTagCompletionsForOwner(
	templatePath string,
	owner *admin.VueComponent,
) []protocol.CompletionItem {
	componentNames, err := p.adminIndexer.GetAllComponentNames()
	if err != nil {
		return []protocol.CompletionItem{}
	}
	if templatePath != "" {
		if owner == nil {
			owner, _ = p.adminIndexer.GetComponentByTemplatePath(templatePath)
		}
		if owner != nil {
			for _, local := range owner.LocalComponents {
				componentNames = append(componentNames, local.Name)
			}
		}
	}
	componentNames = uniqueSortedStrings(componentNames)

	items := make([]protocol.CompletionItem, 0, len(componentNames))
	for _, name := range componentNames {
		// Create snippet: <component-name>$0</component-name>
		// $0 is the cursor position after insertion
		snippet := name + ">$0</" + name + ">"

		item := protocol.CompletionItem{
			Label:            name,
			Kind:             int(protocol.ClassCompletion),
			InsertText:       snippet,
			InsertTextFormat: int(protocol.SnippetTextFormat),
		}

		// Try to get component details for documentation
		component, found, resolveErr := p.adminIndexer.GetComponentForTemplateTag(
			templatePath, name, owner,
		)
		if resolveErr == nil && found && component != nil {
			comp := *component
			doc := "**Shopware Admin Component**\n\n"

			if comp.ExtendsComponent != "" {
				doc += "**Extends:** `" + comp.ExtendsComponent + "`\n\n"
			}

			if len(comp.Props) > 0 {
				doc += "**Props:** "
				propNames := make([]string, 0, len(comp.Props))
				for _, prop := range comp.Props {
					propNames = append(propNames, prop.Name)
				}
				doc += strings.Join(propNames, ", ") + "\n"
			}

			item.Documentation.Kind = "markdown"
			item.Documentation.Value = doc
			markAdminCompletionDeprecated(&item, comp.Deprecated)
		}

		items = append(items, item)
	}

	// Add template tag with slot shorthand
	// Don't close the template yet - the slot completion will close it
	templateItem := protocol.CompletionItem{
		Label:            "template",
		Kind:             int(protocol.ClassCompletion),
		Detail:           "slot template",
		InsertText:       "template #",
		InsertTextFormat: int(protocol.SnippetTextFormat),
	}
	templateItem.Documentation.Kind = "markdown"
	templateItem.Documentation.Value = "**Vue Slot Template**\n\nUsed to fill named slots in parent components.\n\nExample: `<template #default>...</template>`"
	items = append(items, templateItem)

	return items
}

// isInExtendParentArgument checks if cursor is in the parent component argument of Component.extend
// Pattern: Component.extend('name', '<caret>', ...)
func (p *AdminCompletionProvider) isInExtendParentArgument(node *jssyntax.Node) bool {
	if !p.isSecondStringArgument(node) {
		return false
	}
	name := jsquery.CallName(node)
	return name == "Component.extend" || name == "Shopware.Component.extend"
}

func (p *AdminCompletionProvider) isSecondStringArgument(node *jssyntax.Node) bool {
	return jsquery.StringAt(node) != nil && jsquery.StringArgumentIndex(node) == 1
}

// getComponentCompletions returns completion items for all registered components
func (p *AdminCompletionProvider) getComponentCompletions() []protocol.CompletionItem {
	componentNames, err := p.adminIndexer.GetAllComponentNames()
	if err != nil {
		return []protocol.CompletionItem{}
	}

	items := make([]protocol.CompletionItem, 0, len(componentNames))
	for _, name := range componentNames {
		item := protocol.CompletionItem{
			Label: name,
			Kind:  int(protocol.ClassCompletion),
		}

		// Try to get component details for documentation
		comp, err := p.adminIndexer.GetEffectiveComponent(name)
		if err == nil && comp != nil {
			doc := "**Shopware Admin Component**\n\n"

			if comp.ExtendsComponent != "" {
				doc += "**Extends:** `" + comp.ExtendsComponent + "`\n\n"
			}

			if comp.FilePath != "" {
				doc += "**Registered in:** `" + filepath.Base(comp.FilePath) + "`\n"
			}

			item.Documentation.Kind = "markdown"
			item.Documentation.Value = doc
			markAdminCompletionDeprecated(&item, comp.Deprecated)
		}

		items = append(items, item)
	}

	return items
}

func (p *AdminCompletionProvider) getDynamicComponentCompletions(
	templatePath string,
	selector admin.VueDynamicComponentSelector,
	offset uint32,
	owners ...*admin.VueComponent,
) []protocol.CompletionItem {
	items := p.getComponentCompletions()
	seen := make(map[string]bool, len(items))
	for _, item := range items {
		seen[item.Label] = true
	}
	if templatePath != "" {
		var owner *admin.VueComponent
		if len(owners) > 0 {
			owner = owners[0]
		}
		if owner == nil {
			owner, _ = p.adminIndexer.GetComponentByTemplatePath(templatePath)
		}
		if owner != nil {
			for _, local := range owner.LocalComponents {
				if local.Name == "" || seen[local.Name] {
					continue
				}
				seen[local.Name] = true
				items = append(items, protocol.CompletionItem{
					Label:  local.Name,
					Kind:   int(protocol.ClassCompletion),
					Detail: "Template-local Administration component",
				})
			}
		}
	}
	_, insideLiteral := selector.CandidateAt(offset)
	if !insideLiteral && offset > 0 {
		_, insideLiteral = selector.CandidateAt(offset - 1)
	}
	for index := range items {
		items[index].Detail = "Vue dynamic component"
		if items[index].Deprecated {
			items[index].Detail = "Deprecated Vue dynamic component"
		}
		items[index].InsertText = items[index].Label
		items[index].InsertTextFormat = int(protocol.PlainTextFormat)
		if selector.AttributeName != "is" && !insideLiteral {
			items[index].InsertText = "'" + items[index].Label + "'"
		}
	}
	sortCompletionItems(items)
	return items
}

// getComponentNameForAttributeCompletion checks if we're in a position to complete attributes
// and returns the component name if so, empty string otherwise
func (p *AdminCompletionProvider) getComponentNameForAttributeCompletion(node *twigsyntax.Node, content []byte) string {
	if !p.isInHTMLAttributeCompletionPosition(node) {
		return ""
	}

	startTag := twigquery.StartingHTMLTagAt(node)
	if startTag == nil {
		return ""
	}

	componentName := twigquery.HTMLTagName(startTag)
	if !admin.IsComponentTag(componentName) {
		var found bool
		componentName, found = admin.StaticComponentNameForTag(startTag)
		if !found {
			return ""
		}
	}

	return componentName
}

func (p *AdminCompletionProvider) isInHTMLAttributeCompletionPosition(
	node *twigsyntax.Node,
) bool {
	if node == nil {
		return false
	}
	startTag := twigquery.StartingHTMLTagAt(node)
	if startTag == nil {
		return false
	}
	if node.Kind() == twigsyntax.HtmlStartingTag ||
		twigquery.HTMLAttributeAt(node) != nil ||
		node.Kind() == twigsyntax.Error {
		return true
	}
	for child := range startTag.ChildTokens() {
		if (child.Kind() == twigsyntax.TkWord || child.Kind() == twigsyntax.TkTwigComponentName) &&
			node.Range().Start >= child.Range().Start && node.Range().End <= child.Range().End {
			return false
		}
	}
	return true
}

// getComponentPropCompletions returns completion items for component props
func (p *AdminCompletionProvider) getComponentPropCompletions(
	componentName string,
	templatePaths ...string,
) []protocol.CompletionItem {
	return p.getComponentPropCompletionsForOwner(
		componentName, optionalTemplatePath(templatePaths), nil,
	)
}

func (p *AdminCompletionProvider) getComponentPropCompletionsForOwner(
	componentName,
	templatePath string,
	owner *admin.VueComponent,
) []protocol.CompletionItem {
	component, found, err := p.adminIndexer.GetComponentForTemplateTag(
		templatePath, componentName, owner,
	)
	if err != nil || !found || component == nil {
		return []protocol.CompletionItem{}
	}

	var items []protocol.CompletionItem

	for _, comp := range []admin.VueComponent{*component} {
		// Add props
		for _, prop := range comp.Props {
			attributeName := admin.CamelToKebab(prop.Name)
			// Regular prop
			item := protocol.CompletionItem{
				Label:  attributeName,
				Kind:   int(protocol.PropertyCompletion),
				Detail: prop.Type,
			}

			// Build documentation
			doc := strings.TrimSpace(prop.Documentation)
			if doc != "" {
				doc += "\n\n"
			}
			if prop.Type != "" {
				doc += "**Type:** `" + prop.Type + "`\n\n"
			}
			if prop.Required {
				doc += "**Required**\n\n"
			}
			if prop.Default != "" {
				doc += "**Default:** `" + prop.Default + "`\n"
			}
			if values, complete := admin.VuePropAllowedValues(prop); len(values) > 0 {
				label := "**Allowed values:** "
				if !complete {
					label = "**Known values:** "
				}
				doc += label + adminCompletionValueList(values) + "\n"
			}

			if doc != "" {
				item.Documentation.Kind = "markdown"
				item.Documentation.Value = doc
			}
			markAdminCompletionDeprecated(&item, prop.Deprecated)

			items = append(items, item)

			// Also add Vue binding shorthand (:prop)
			bindingItem := protocol.CompletionItem{
				Label:            ":" + attributeName,
				Kind:             int(protocol.PropertyCompletion),
				Detail:           prop.Type + " (v-bind)",
				InsertText:       ":" + attributeName + "=\"$0\"",
				InsertTextFormat: int(protocol.SnippetTextFormat),
			}
			if doc != "" {
				bindingItem.Documentation.Kind = "markdown"
				bindingItem.Documentation.Value = doc
			}
			markAdminCompletionDeprecated(&bindingItem, prop.Deprecated)
			items = append(items, bindingItem)
		}

		// Add events (emits)
		for _, event := range comp.ComponentEvents() {
			eventName := admin.CanonicalEventName(event.Name)
			if eventName == "" {
				continue
			}
			detail := "event"
			if event.Type != "" {
				detail += " • " + event.Type
			}
			item := protocol.CompletionItem{
				Label:            "@" + eventName,
				Kind:             int(protocol.EventCompletion),
				Detail:           detail,
				InsertText:       "@" + eventName + "=\"$0\"",
				InsertTextFormat: int(protocol.SnippetTextFormat),
			}
			documentation := strings.TrimSpace(event.Documentation)
			if event.Type != "" {
				if documentation != "" {
					documentation += "\n\n"
				}
				documentation += "**Payload:** `" + event.Type + "`"
			}
			if documentation != "" {
				item.Documentation.Kind = string(protocol.Markdown)
				item.Documentation.Value = documentation
			}
			items = append(items, item)
		}

		// A model completion represents both halves of Vue's public contract:
		// the readable prop and its matching update event.
		for _, model := range comp.ComponentModels() {
			detail := "v-model • " + model.PropName + " / " + model.EventName
			if valueType := admin.VuePropValueType(model.Prop.Type); valueType != "" {
				detail += " • " + valueType
			}
			item := protocol.CompletionItem{
				Label:            model.AttributeName,
				Kind:             int(protocol.PropertyCompletion),
				Detail:           detail,
				InsertText:       model.AttributeName + "=\"$0\"",
				InsertTextFormat: int(protocol.SnippetTextFormat),
			}
			item.Documentation.Kind = string(protocol.Markdown)
			item.Documentation.Value = "Two-way binding for prop `" +
				model.PropName + "` through event `" + model.EventName + "`."
			markAdminCompletionDeprecated(&item, model.Prop.Deprecated)
			items = append(items, item)
		}
	}

	return items
}

func adminCompletionValueList(values []string) string {
	formatted := make([]string, 0, len(values))
	for _, value := range values {
		display := value
		if display == "" {
			display = "(empty)"
		}
		formatted = append(formatted, "`"+strings.ReplaceAll(display, "`", "\\`")+"`")
	}
	return strings.Join(formatted, ", ")
}

func markAdminCompletionDeprecated(
	item *protocol.CompletionItem,
	message string,
) {
	message = strings.TrimSpace(message)
	if item == nil || message == "" {
		return
	}
	item.Deprecated = true
	if item.Detail == "" {
		item.Detail = "Deprecated Administration API"
	} else if !strings.HasPrefix(strings.ToLower(item.Detail), "deprecated") {
		item.Detail = "Deprecated • " + item.Detail
	}
	deprecation := "**Deprecated:** " + message
	if item.Documentation.Value == "" {
		item.Documentation.Kind = string(protocol.Markdown)
		item.Documentation.Value = deprecation
	} else {
		item.Documentation.Value = deprecation + "\n\n" + item.Documentation.Value
	}
}

func commonAdminPropDeprecation(props []admin.VueComponentProp) string {
	if len(props) == 0 {
		return ""
	}
	seen := make(map[string]bool)
	var messages []string
	for _, prop := range props {
		message := strings.TrimSpace(prop.Deprecated)
		if message == "" {
			return ""
		}
		if !seen[message] {
			seen[message] = true
			messages = append(messages, message)
		}
	}
	return strings.Join(messages, " / ")
}

// GetTriggerCharacters returns the characters that trigger this completion provider
