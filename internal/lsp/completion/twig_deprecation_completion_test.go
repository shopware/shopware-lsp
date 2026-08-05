package completion

import (
	"context"
	"strings"
	"testing"

	"github.com/shopware/shopware-lsp/internal/indexer"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	"github.com/shopware/shopware-lsp/internal/twig"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTwigCallableCompletionsMarkDeprecation(t *testing.T) {
	twigIndex, err := twig.NewTwigIndexer(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, twigIndex.Close()) })
	require.NoError(t, twigIndex.Index(indexer.NewParsedFile(
		"/project/src/AppExtension.php",
		[]byte(`<?php
use Twig\Extension\AbstractExtension;
use Twig\TwigFilter;
use Twig\TwigFunction;
class AppExtension extends AbstractExtension {
    public function getFunctions(): array {
        return [
            new TwigFunction('legacy_function', $this->legacy(...)),
            new TwigFunction('active_function', $this->active(...)),
        ];
    }
    public function getFilters(): array {
        return [
            new TwigFilter('legacy_filter', $this->legacyFilter(...)),
            new TwigFilter('active_filter', $this->activeFilter(...)),
        ];
    }
    public function legacy(): string {
        trigger_deprecation(
            'app',
            '1.0',
            'The "legacy_function" Twig function is deprecated. Use active_function instead.',
        );
        return '';
    }
    public function active(): string { return ''; }
    public function legacyFilter(string $value): string {
        trigger_deprecation(
            'app',
            '1.0',
            'The "legacy_filter" Twig filter is deprecated. Use active_filter instead.',
        );
        return $value;
    }
    public function activeFilter(string $value): string { return $value; }
}`),
	)))
	provider := NewTwigCompletionProvider(
		"/project",
		twigIndex,
		nil,
		nil,
	)

	_, functionRequest := twigCompletionAt(
		"file:///project/templates/page.html.twig",
		`{{ leg }}`,
		strings.Index(`{{ leg }}`, "leg")+3,
	)
	functions := provider.GetCompletions(
		context.Background(),
		functionRequest,
	)
	legacyFunction := completionByInsertText(
		t,
		functions,
		"legacy_function($0)",
	)
	assert.True(t, legacyFunction.Deprecated)
	assert.Equal(t, "Deprecated Twig function", legacyFunction.Detail)
	assert.Contains(
		t,
		legacyFunction.Documentation.Value,
		"active_function",
	)
	assert.False(
		t,
		completionByInsertText(t, functions, "active_function($0)").
			Deprecated,
	)

	_, filterRequest := twigCompletionAt(
		"file:///project/templates/page.html.twig",
		`{{ value|leg }}`,
		strings.Index(`{{ value|leg }}`, "leg")+3,
	)
	filters := provider.GetCompletions(context.Background(), filterRequest)
	legacyFilter := completionByInsertText(
		t,
		filters,
		"legacy_filter($0)",
	)
	assert.True(t, legacyFilter.Deprecated)
	assert.Equal(t, "Deprecated Twig filter", legacyFilter.Detail)
	assert.Contains(t, legacyFilter.Documentation.Value, "active_filter")
	assert.False(
		t,
		completionByInsertText(t, filters, "active_filter($0)").
			Deprecated,
	)
}

func completionByInsertText(
	t *testing.T,
	items []protocol.CompletionItem,
	insertText string,
) protocol.CompletionItem {
	t.Helper()
	for _, item := range items {
		if item.InsertText == insertText {
			return item
		}
	}
	t.Fatalf("completion with insert text %q not found in %#v", insertText, items)
	return protocol.CompletionItem{}
}
