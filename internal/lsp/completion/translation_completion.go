package completion

import (
	"context"
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	"github.com/shopware/shopware-lsp/internal/parser/cst"
	"github.com/shopware/shopware-lsp/internal/php"
	"github.com/shopware/shopware-lsp/internal/translation"
)

type TranslationCompletionProvider struct {
	index    *translation.Index
	phpIndex *php.PHPIndex
}

func NewTranslationCompletionProvider(
	index *translation.Index,
	phpIndex *php.PHPIndex,
) *TranslationCompletionProvider {
	return &TranslationCompletionProvider{
		index:    index,
		phpIndex: phpIndex,
	}
}

func (p *TranslationCompletionProvider) GetCompletions(
	ctx context.Context,
	params *lsp.CompletionRequest,
) []protocol.CompletionItem {
	if p == nil || p.index == nil || params == nil || params.Node == nil {
		return nil
	}
	extension := strings.ToLower(filepath.Ext(params.TextDocument.URI))
	if extension != ".php" && extension != ".twig" {
		return nil
	}
	if extension == ".php" {
		if reference, found := php.AssistantArgumentReference(
			ctx,
			params.Node,
			"TranslationDomain",
		); found {
			return assistantTranslationCompletionEdits(
				p.domainCompletions(),
				reference,
				params,
			)
		}
		if reference, found := php.AssistantArgumentReference(
			ctx,
			params.Node,
			"TranslationKey",
		); found {
			domain := "messages"
			if sibling, siblingFound := php.AssistantSiblingStringArgument(
				ctx,
				params.Node,
				"TranslationDomain",
			); siblingFound {
				domain = sibling
			}
			return assistantTranslationCompletionEdits(
				p.keyCompletions(domain),
				reference,
				params,
			)
		}
	}
	reference, ok := translation.ReferenceAt(
		params.TextDocument.URI,
		params.Node,
		params.DocumentContent,
	)
	if !ok || extension == ".php" &&
		!translation.ValidatePHPReference(
			ctx,
			reference,
			p.phpIndex,
			params.DocumentContent,
		) {
		return nil
	}

	switch reference.Role {
	case translation.ReferenceDomain:
		return p.domainCompletions()
	case translation.ReferenceKey:
		return p.keyCompletions(reference.Domain)
	case translation.ReferencePlaceholder:
		return p.placeholderCompletions(reference.Domain, reference.Key)
	default:
		return nil
	}
}

func assistantTranslationCompletionEdits(
	items []protocol.CompletionItem,
	reference cst.TextRange,
	params *lsp.CompletionRequest,
) []protocol.CompletionItem {
	if params == nil || params.LineIndex == nil {
		return items
	}
	replacement := namedArgumentCompletionRange(reference, params.LineIndex)
	for index := range items {
		items[index].TextEdit = protocol.TextEdit{
			Range:   replacement,
			NewText: items[index].Label,
		}
	}
	return items
}

func (p *TranslationCompletionProvider) placeholderCompletions(
	domain,
	key string,
) []protocol.CompletionItem {
	placeholders, err := p.index.GetPlaceholders(domain, key)
	if err != nil {
		return nil
	}
	items := make([]protocol.CompletionItem, 0, len(placeholders))
	for _, placeholder := range placeholders {
		items = append(items, protocol.CompletionItem{
			Label:  placeholder,
			Kind:   int(protocol.VariableCompletion),
			Detail: "Translation placeholder",
		})
	}
	return items
}

func (p *TranslationCompletionProvider) domainCompletions() []protocol.CompletionItem {
	domains, err := p.index.GetDomains()
	if err != nil {
		return nil
	}
	items := make([]protocol.CompletionItem, 0, len(domains))
	for _, domain := range domains {
		keys, _ := p.index.GetKeys(domain)
		items = append(items, protocol.CompletionItem{
			Label:  domain,
			Kind:   int(protocol.ModuleCompletion),
			Detail: translationCountLabel(len(keys)),
		})
	}
	return items
}

func (p *TranslationCompletionProvider) keyCompletions(
	domain string,
) []protocol.CompletionItem {
	messages, err := p.index.GetDomainMessages(domain)
	if err != nil {
		return nil
	}
	preferred := make(map[string]translation.Message)
	for _, message := range messages {
		current, exists := preferred[message.Key]
		if !exists || translationMessagePriority(message) >
			translationMessagePriority(current) {
			preferred[message.Key] = message
		}
	}
	keys := make([]string, 0, len(preferred))
	for key := range preferred {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	items := make([]protocol.CompletionItem, 0, len(keys))
	for _, key := range keys {
		message := preferred[key]
		item := protocol.CompletionItem{
			Label:  key,
			Kind:   int(protocol.ValueCompletion),
			Detail: truncateText(message.Text, 70),
		}
		if message.Text != "" {
			item.Documentation.Kind = string(protocol.Markdown)
			item.Documentation.Value = "**" + message.Locale + "**: " +
				message.Text
		}
		items = append(items, item)
	}
	return items
}

func translationMessagePriority(message translation.Message) int {
	priority := 0
	locale := strings.ToLower(strings.ReplaceAll(message.Locale, "_", "-"))
	if locale == "en" || strings.HasPrefix(locale, "en-") {
		priority += 2
	}
	normalized := strings.ToLower(filepath.ToSlash(message.File))
	if !strings.Contains(normalized, "/var/cache/") &&
		!strings.Contains(normalized, "/app/cache/") {
		priority++
	}
	return priority
}

func translationCountLabel(count int) string {
	if count == 1 {
		return "1 translation key"
	}
	return fmt.Sprintf("%d translation keys", count)
}

func (p *TranslationCompletionProvider) GetTriggerCharacters() []string {
	return nil
}
