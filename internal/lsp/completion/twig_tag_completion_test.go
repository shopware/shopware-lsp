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

func TestTwigCustomTagCompletion(t *testing.T) {
	twigIndex := twigTagCompletionIndex(t)
	provider := NewTwigCompletionProvider(
		"/project",
		twigIndex,
		nil,
		nil,
	)

	source := `{% leg`
	_, request := twigCompletionAt(
		"file:///project/templates/page.html.twig",
		source,
		len(source),
	)
	items := provider.GetCompletions(context.Background(), request)
	legacy := requireCompletion(t, items, "legacy")
	assert.Equal(t, int(protocol.KeywordCompletion), legacy.Kind)
	assert.True(t, legacy.Deprecated)
	assert.Contains(t, legacy.Documentation.Value, "modern")

	typesSource := `{% typ`
	_, typesRequest := twigCompletionAt(
		"file:///project/templates/page.html.twig",
		typesSource,
		len(typesSource),
	)
	types := requireCompletion(
		t,
		provider.GetCompletions(context.Background(), typesRequest),
		"types",
	)
	assert.Equal(t, int(protocol.SnippetCompletion), types.Kind)

	closingSource := `{%- endtra`
	_, closingRequest := twigCompletionAt(
		"file:///project/templates/page.html.twig",
		closingSource,
		len(closingSource),
	)
	closing := requireCompletion(
		t,
		provider.GetCompletions(context.Background(), closingRequest),
		"endtrans",
	)
	assert.False(t, closing.Deprecated)
	assert.NotContains(
		t,
		completionLabels(provider.GetCompletions(
			context.Background(),
			closingRequest,
		)),
		"endlegacy",
	)

	shortClosingSource := `{% en`
	_, shortClosingRequest := twigCompletionAt(
		"file:///project/templates/page.html.twig",
		shortClosingSource,
		len(shortClosingSource),
	)
	requireCompletion(
		t,
		provider.GetCompletions(context.Background(), shortClosingRequest),
		"endtrans",
	)
}

func TestTwigCustomTagCompletionRespectsActiveDuplicate(t *testing.T) {
	twigIndex := twigTagCompletionIndex(t)
	require.NoError(t, twigIndex.Index(indexer.NewParsedFile(
		"/project/src/OverrideTokenParser.php",
		[]byte(`<?php
class OverrideTokenParser implements \Twig\TokenParser\TokenParserInterface {
    public function getTag(): string { return 'legacy'; }
}`),
	)))
	provider := NewTwigCompletionProvider(
		"/project",
		twigIndex,
		nil,
		nil,
	)
	source := `{% legacy`
	_, request := twigCompletionAt(
		"file:///project/templates/page.html.twig",
		source,
		strings.Index(source, "legacy")+len("legacy"),
	)
	item := requireCompletion(
		t,
		provider.GetCompletions(context.Background(), request),
		"legacy",
	)
	assert.False(t, item.Deprecated)
}

func TestTwigCustomTagCompletionSkipsVerbatimBodies(t *testing.T) {
	twigIndex := twigTagCompletionIndex(t)
	provider := NewTwigCompletionProvider(
		"/project",
		twigIndex,
		nil,
		nil,
	)
	for _, source := range []string{
		`{% raw %}{% leg`,
		`{% verbatim %}{% leg`,
		`{% set source = "{% leg`,
		`{# {% leg`,
	} {
		_, request := twigCompletionAt(
			"file:///project/templates/page.html.twig",
			source,
			len(source),
		)
		assert.NotContains(
			t,
			completionLabels(provider.GetCompletions(
				context.Background(),
				request,
			)),
			"legacy",
		)
	}
}

func twigTagCompletionIndex(t *testing.T) *twig.TwigIndexer {
	t.Helper()
	twigIndex, err := twig.NewTwigIndexer(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, twigIndex.Close()) })
	require.NoError(t, twigIndex.Index(indexer.NewParsedFile(
		"/project/src/LegacyTokenParser.php",
		[]byte(`<?php
/** @deprecated Use the modern tag instead. */
class LegacyTokenParser implements \Twig\TokenParser\TokenParserInterface {
    public function getTag(): string { return 'legacy'; }
}
class ModernTokenParser implements \Twig\TokenParser\TokenParserInterface {
    public function getTag(): string { return 'modern'; }
}`),
	)))
	return twigIndex
}
