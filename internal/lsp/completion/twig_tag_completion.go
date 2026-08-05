package completion

import (
	"bytes"
	"sort"
	"strings"

	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	"github.com/shopware/shopware-lsp/internal/twig"
)

type twigTagCompletionStatus struct {
	allDeprecated bool
	message       string
	class         string
}

func (p *TwigCompletionProvider) twigTagCompletions(
	request *lsp.CompletionRequest,
) []protocol.CompletionItem {
	if p == nil || p.twigIndexer == nil || request == nil ||
		request.CompletionParams == nil || request.LineIndex == nil {
		return nil
	}
	offset := request.LineIndex.OffsetUTF16(
		uint32(request.Position.Line),
		uint32(request.Position.Character),
	)
	prefix, found := twigTagCompletionPrefix(
		request.DocumentContent,
		offset,
	)
	if !found {
		return nil
	}
	tags, err := p.twigIndexer.GetAllTwigTags()
	if err != nil {
		return nil
	}
	statuses := make(map[string]twigTagCompletionStatus)
	for _, tag := range tags {
		status, exists := statuses[tag.Name]
		if !exists {
			status.allDeprecated = true
		}
		status.allDeprecated = status.allDeprecated && tag.Deprecated
		if status.message == "" {
			status.message = tag.Deprecation
		}
		if status.class == "" {
			status.class = tag.Class
		}
		statuses[tag.Name] = status
	}
	items := make([]protocol.CompletionItem, 0, len(statuses)+3)
	for name, status := range statuses {
		label := name
		if prefix != "" && !strings.HasPrefix(
			strings.ToLower(label),
			strings.ToLower(prefix),
		) {
			continue
		}
		item := protocol.CompletionItem{
			Label:      label,
			InsertText: label,
			Kind:       int(protocol.KeywordCompletion),
			Detail:     "Twig custom tag",
		}
		item.Documentation.Kind = string(protocol.Markdown)
		item.Documentation.Value = "Provided by `" + status.class + "`."
		if status.allDeprecated {
			item.Deprecated = true
			item.Detail = "Deprecated Twig custom tag"
			item.Documentation.Value = "**Deprecated Twig custom tag**"
			if status.message != "" {
				item.Documentation.Value += "\n\n" + status.message
			}
			if status.class != "" {
				item.Documentation.Value += "\n\nProvided by `" +
					status.class + "`."
			}
		}
		items = append(items, item)
	}
	for _, label := range []string{"endtrans", "endtranschoice"} {
		if _, declared := statuses[label]; declared ||
			!strings.HasPrefix(
				strings.ToLower(label),
				strings.ToLower(prefix),
			) {
			continue
		}
		items = append(items, protocol.CompletionItem{
			Label:      label,
			InsertText: label,
			Kind:       int(protocol.KeywordCompletion),
			Detail:     "Twig closing tag",
		})
	}
	if _, declared := statuses["types"]; !declared &&
		strings.HasPrefix("types", strings.ToLower(prefix)) {
		items = append(items, protocol.CompletionItem{
			Label:      "types",
			InsertText: "types { ${1:variable}: '${2:Type}' }",
			Kind:       int(protocol.SnippetCompletion),
			Detail:     "Twig type declaration tag",
			InsertTextFormat: int(
				protocol.SnippetTextFormat,
			),
		})
	}
	sort.Slice(items, func(left, right int) bool {
		return strings.ToLower(items[left].Label) <
			strings.ToLower(items[right].Label)
	})
	return items
}

func twigTagCompletionPrefix(
	content []byte,
	offset uint32,
) (string, bool) {
	if int(offset) > len(content) {
		return "", false
	}
	before := content[:offset]
	open := bytes.LastIndex(before, []byte("{%"))
	if open < 0 {
		return "", false
	}
	if close := bytes.LastIndex(before, []byte("%}")); close > open {
		return "", false
	}
	cursor := open + 2
	if cursor < len(before) && before[cursor] == '-' {
		cursor++
	}
	for cursor < len(before) && twigTagCompletionSpace(before[cursor]) {
		cursor++
	}
	start := cursor
	for cursor < len(before) && twigTagCompletionNameByte(before[cursor]) {
		cursor++
	}
	if cursor != len(before) {
		return "", false
	}
	probe := append(bytes.Clone(before), 'x')
	for _, usage := range twig.TwigTagUsages(probe) {
		if usage.Range.Start == uint32(start) &&
			usage.Range.End == uint32(len(probe)) {
			return string(before[start:cursor]), true
		}
	}
	return "", false
}

func twigTagCompletionSpace(value byte) bool {
	return value == ' ' || value == '\t' ||
		value == '\r' || value == '\n'
}

func twigTagCompletionNameByte(value byte) bool {
	return value == '_' || value == '-' ||
		value >= 'a' && value <= 'z' ||
		value >= 'A' && value <= 'Z' ||
		value >= '0' && value <= '9' ||
		value >= 0x80
}
