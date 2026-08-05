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

func TestTwigGuardCompletionTypesAndCallables(t *testing.T) {
	twigIndex, err := twig.NewTwigIndexer(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, twigIndex.Close()) })
	require.NoError(t, twigIndex.Index(indexer.NewParsedFile(
		"/project/src/AppExtension.php",
		[]byte(`<?php
class AppExtension extends \Twig\Extension\AbstractExtension
{
    public function getFunctions(): array
    {
        return [new \Twig\TwigFunction('asset_url', $this->asset(...))];
    }

    public function getFilters(): array
    {
        return [new \Twig\TwigFilter('money', $this->money(...))];
    }

    public function getTests(): array
    {
        return [new \Twig\TwigTest('positive', $this->positive(...))];
    }

    public function asset(string $path): string { return $path; }
    public function money(int $value): string { return (string) $value; }
    public function positive(int $value): bool { return $value > 0; }
}`),
	)))
	provider := NewTwigCompletionProvider(
		"/project",
		twigIndex,
		nil,
		nil,
	)

	typeItems := twigGuardCompletionItems(
		t,
		provider,
		`{% guard <caret> %}`,
	)
	assert.ElementsMatch(
		t,
		[]string{"function", "filter", "test"},
		completionLabels(typeItems),
	)
	partialTypes := twigGuardCompletionItems(
		t,
		provider,
		`{% guard fun<caret> %}`,
	)
	assert.Equal(t, []string{"function"}, completionLabels(partialTypes))

	for _, test := range []struct {
		source   string
		expected string
		excluded []string
	}{
		{
			source:   `{% guard function <caret> %}`,
			expected: "asset_url",
			excluded: []string{"money", "positive"},
		},
		{
			source:   `{% guard filter mon<caret> %}`,
			expected: "money",
			excluded: []string{"asset_url", "positive"},
		},
		{
			source:   `{% guard test <caret> %}`,
			expected: "positive",
			excluded: []string{"asset_url", "money"},
		},
	} {
		items := twigGuardCompletionItems(t, provider, test.source)
		requireCompletion(t, items, test.expected)
		labels := completionLabels(items)
		for _, excluded := range test.excluded {
			assert.NotContains(t, labels, excluded)
		}
	}
}

func TestTwigGuardCompletionIgnoresCommentsAndRawBodies(t *testing.T) {
	twigIndex, err := twig.NewTwigIndexer(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, twigIndex.Close()) })
	provider := NewTwigCompletionProvider(
		"/project",
		twigIndex,
		nil,
		nil,
	)
	for _, source := range []string{
		`{# {% guard <caret> #}`,
		`{% raw %}{% guard function <caret>`,
		`{% guard function complete <caret>%}`,
	} {
		assert.Empty(t, twigGuardCompletionItems(t, provider, source))
	}
}

func twigGuardCompletionItems(
	t *testing.T,
	provider *TwigCompletionProvider,
	source string,
) []protocol.CompletionItem {
	t.Helper()
	offset := strings.Index(source, "<caret>")
	require.NotEqual(t, -1, offset)
	source = strings.Replace(source, "<caret>", "", 1)
	_, request := twigCompletionAt(
		"file:///project/templates/page.html.twig",
		source,
		offset,
	)
	return provider.GetCompletions(context.Background(), request)
}
