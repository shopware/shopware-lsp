package completion

import (
	"path/filepath"
	"sort"
	"strings"

	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	"github.com/shopware/shopware-lsp/internal/twig"
)

var twigBuiltinTests = []string{
	"constant",
	"defined",
	"divisible by",
	"empty",
	"even",
	"false",
	"iterable",
	"mapping",
	"none",
	"null",
	"odd",
	"same as",
	"sequence",
	"string",
	"true",
}

func (p *TwigCompletionProvider) twigTestCompletions(
	request *lsp.CompletionRequest,
) []protocol.CompletionItem {
	if p == nil || request == nil || request.CompletionParams == nil ||
		request.LineIndex == nil || request.Root == nil {
		return nil
	}
	offset := request.LineIndex.OffsetUTF16(
		uint32(request.Position.Line),
		uint32(request.Position.Character),
	)
	position, found := twig.TestCompletionAt(
		request.Root,
		request.DocumentContent,
		offset,
	)
	if !found {
		return nil
	}
	prefix := strings.ToLower(position.Name)
	itemsByName := make(map[string]protocol.CompletionItem)
	if p.includeBuiltinCompletions {
		for _, name := range twigBuiltinTests {
			if strings.HasPrefix(strings.ToLower(name), prefix) {
				itemsByName[strings.ToLower(name)] = protocol.CompletionItem{
					Label:      name,
					InsertText: name,
					Kind:       int(protocol.FunctionCompletion),
					Detail:     "Built-in Twig test",
				}
			}
		}
	}
	if p.twigIndexer != nil {
		tests, _ := p.twigIndexer.GetAllTwigTests()
		statuses := make(map[string]twigCallableCompletionDeprecation)
		displayNames := make(map[string]string)
		usages := make(map[string]string)
		files := make(map[string]string)
		for _, test := range tests {
			if strings.Contains(test.Name, "*") ||
				!strings.HasPrefix(strings.ToLower(test.Name), prefix) {
				continue
			}
			key := strings.ToLower(test.Name)
			status, exists := statuses[key]
			if !exists {
				status.all = true
				displayNames[key] = test.Name
				usages[key] = test.Usage
				files[key] = filepath.Base(test.FilePath)
			}
			status.all = status.all && test.Deprecated
			if status.message == "" {
				status.message = test.Deprecation
			}
			statuses[key] = status
		}
		for key, status := range statuses {
			name := displayNames[key]
			item := protocol.CompletionItem{
				Label:      name,
				InsertText: name,
				Kind:       int(protocol.FunctionCompletion),
				Detail:     "Custom Twig test",
			}
			if usage := usages[key]; usage != "" {
				item.Documentation.Kind = string(protocol.Markdown)
				item.Documentation.Value = "`" + usage + "`"
				if files[key] != "" {
					item.Documentation.Value += "\n\nDeclared in `" +
						files[key] + "`."
				}
			}
			applyTwigCallableCompletionDeprecation(
				&item,
				"test",
				status,
			)
			itemsByName[key] = item
		}
	}
	items := make([]protocol.CompletionItem, 0, len(itemsByName))
	for _, item := range itemsByName {
		items = append(items, item)
	}
	sort.Slice(items, func(left, right int) bool {
		leftName := strings.ToLower(items[left].Label)
		rightName := strings.ToLower(items[right].Label)
		if leftName != rightName {
			return leftName < rightName
		}
		return items[left].Label < items[right].Label
	})
	return items
}
