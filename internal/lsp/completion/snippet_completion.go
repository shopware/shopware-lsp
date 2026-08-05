package completion

import (
	"context"
	"path/filepath"
	"strings"

	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	phpquery "github.com/shopware/shopware-lsp/internal/parser/php/query"
	twigquery "github.com/shopware/shopware-lsp/internal/parser/twig/query"
	"github.com/shopware/shopware-lsp/internal/snippet"
)

type SnippetCompletionProvider struct {
	snippetIndexer *snippet.SnippetIndexer
}

func NewSnippetCompletionProvider(snippetIndexer *snippet.SnippetIndexer) *SnippetCompletionProvider {
	return &SnippetCompletionProvider{snippetIndexer: snippetIndexer}
}

func (s *SnippetCompletionProvider) GetCompletions(ctx context.Context, params *lsp.CompletionRequest) []protocol.CompletionItem {
	switch strings.ToLower(filepath.Ext(params.TextDocument.URI)) {
	case ".twig":
		if params.Node == nil {
			return []protocol.CompletionItem{}
		}
		return s.twigCompletion(ctx, params)
	case ".php":
		if params.Node == nil {
			return []protocol.CompletionItem{}
		}
		return s.phpCompletion(ctx, params)
	case ".js", ".ts":
		if params.Node == nil {
			return []protocol.CompletionItem{}
		}
		return s.jsCompletion(ctx, params)
	default:
		return []protocol.CompletionItem{}
	}
}

func (s *SnippetCompletionProvider) twigCompletion(ctx context.Context, params *lsp.CompletionRequest) []protocol.CompletionItem {
	if params.Root != nil && params.LineIndex != nil &&
		params.CompletionParams != nil {
		offset := params.LineIndex.OffsetUTF16(
			uint32(params.Position.Line),
			uint32(params.Position.Character),
		)
		if _, found := snippet.AdminTwigReferenceAtOffset(
			params.Root,
			offset,
		); found {
			return s.getAdminSnippetCompletions()
		}
	}
	// Check for frontend snippet pattern: {{ 'key'|trans }}
	if twigquery.StringInFilter(params.Node, "trans") {
		return s.getFrontendSnippetCompletions()
	}

	// Check for admin snippet pattern: {{ $tc('key') }} or {{ $t('key') }}
	if twigquery.StringInFunction(params.Node, "$tc", "$t") {
		return s.getAdminSnippetCompletions()
	}

	return []protocol.CompletionItem{}
}

func (s *SnippetCompletionProvider) phpCompletion(ctx context.Context, params *lsp.CompletionRequest) []protocol.CompletionItem {
	if phpquery.StringInCall(params.Node, 0, "trans") {
		return s.getFrontendSnippetCompletions()
	}

	return []protocol.CompletionItem{}
}

func (s *SnippetCompletionProvider) jsCompletion(ctx context.Context, params *lsp.CompletionRequest) []protocol.CompletionItem {
	if snippet.AdminJavaScriptStringReference(params.Node) {
		return s.getAdminSnippetCompletions()
	}

	return []protocol.CompletionItem{}
}

func (s *SnippetCompletionProvider) getFrontendSnippetCompletions() []protocol.CompletionItem {
	snippets, _ := s.snippetIndexer.GetFrontendSnippetsWithText()

	var completionItems []protocol.CompletionItem
	for key, text := range snippets {
		item := protocol.CompletionItem{
			Label:  key,
			Detail: truncateText(text, 50),
			Kind:   int(protocol.TextCompletion),
		}
		if text != "" {
			item.Documentation.Kind = "plaintext"
			item.Documentation.Value = text
		}
		completionItems = append(completionItems, item)
	}

	return completionItems
}

func (s *SnippetCompletionProvider) getAdminSnippetCompletions() []protocol.CompletionItem {
	snippets, _ := s.snippetIndexer.GetAdminSnippetsWithText()

	var completionItems []protocol.CompletionItem
	for key, text := range snippets {
		item := protocol.CompletionItem{
			Label:  key,
			Detail: truncateText(text, 50),
			Kind:   int(protocol.TextCompletion),
		}
		if text != "" {
			item.Documentation.Kind = "plaintext"
			item.Documentation.Value = text
		}
		completionItems = append(completionItems, item)
	}

	return completionItems
}

func truncateText(text string, maxLen int) string {
	if len(text) <= maxLen {
		return text
	}
	return text[:maxLen-3] + "..."
}

func (s *SnippetCompletionProvider) GetTriggerCharacters() []string {
	return []string{}
}
