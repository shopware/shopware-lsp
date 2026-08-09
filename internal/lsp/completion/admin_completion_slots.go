package completion

import (
	"sort"
	"strings"

	"github.com/shopware/shopware-lsp/internal/admin"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	twigsyntax "github.com/shopware/shopware-lsp/internal/parser/twig/syntax"
)

func (p *AdminCompletionProvider) GetTriggerCharacters() []string {
	return []string{"'", "\"", "<", " ", "#", ".", "-"}
}

// getSlotCompletions returns completion items for slot names of a component
func (p *AdminCompletionProvider) getSlotCompletions(
	componentName string,
	node *twigsyntax.Node,
	content []byte,
	templatePaths ...string,
) []protocol.CompletionItem {
	if p.adminIndexer == nil {
		return []protocol.CompletionItem{}
	}

	component, found, err := p.adminIndexer.GetComponentForTemplateTag(
		optionalTemplatePath(templatePaths), componentName,
	)
	if err != nil || !found || component == nil {
		return []protocol.CompletionItem{}
	}
	return p.getSlotCompletionsForComponents(
		[]admin.VueComponent{*component}, node, content,
	)
}

func (p *AdminCompletionProvider) getSlotCompletionsForComponents(
	components []admin.VueComponent,
	node *twigsyntax.Node,
	content []byte,
) []protocol.CompletionItem {
	_ = content
	if len(components) == 0 {
		return []protocol.CompletionItem{}
	}

	// Check if the node already contains a closing >
	// If so, we just insert the slot name (without > or </template>)
	var hasClosingBracket bool
	if node != nil {
		nodeText := node.Text()
		if strings.HasPrefix(nodeText, "#") && strings.Contains(nodeText, ">") {
			hasClosingBracket = true
		}
	}

	counts := make(map[string]int)
	slots := make(map[string]admin.VueComponentSlot)
	for _, comp := range components {
		seenComponentSlots := make(map[string]bool)
		for _, slot := range comp.Slots {
			if slot.IsDynamicName() || slot.Name == "" ||
				seenComponentSlots[slot.Name] {
				continue
			}
			seenComponentSlots[slot.Name] = true
			counts[slot.Name]++
			slots[slot.Name] = slot
		}
	}

	names := make([]string, 0, len(slots))
	for name := range slots {
		if counts[name] == len(components) {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	componentNames := make([]string, 0, len(components))
	for _, component := range components {
		componentNames = append(componentNames, component.Name)
	}
	sort.Strings(componentNames)

	items := make([]protocol.CompletionItem, 0, len(names))
	for _, name := range names {
		slot := slots[name]

		item := protocol.CompletionItem{
			Label:            slot.Name,
			Kind:             int(protocol.PropertyCompletion),
			Detail:           "slot",
			InsertTextFormat: int(protocol.SnippetTextFormat),
		}
		if len(components) > 1 {
			item.Detail = "slot • all dynamic candidates"
		}

		// If there's already a closing >, just insert the slot name
		// Otherwise insert the full snippet with > and </template>
		if hasClosingBracket {
			item.InsertText = slot.Name
			item.InsertTextFormat = int(protocol.PlainTextFormat)
		} else {
			item.InsertText = slot.Name + ">$0</template>"
		}

		// Add documentation
		doc := "**Slot:** `" + slot.Name + "`\n\n"
		if len(componentNames) == 1 {
			doc += "**Component:** `" + componentNames[0] + "`"
		} else {
			doc += "**Components:** `" + strings.Join(componentNames, "`, `") + "`"
		}
		item.Documentation.Kind = "markdown"
		item.Documentation.Value = doc

		items = append(items, item)
	}

	return items
}

func optionalTemplatePath(paths []string) string {
	if len(paths) == 0 {
		return ""
	}
	return paths[0]
}

func uniqueSortedStrings(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		if value == "" {
			continue
		}
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}
