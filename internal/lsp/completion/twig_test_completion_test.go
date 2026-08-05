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

func TestTwigTestCompletionIncludesBuiltinsAndIndexedTests(t *testing.T) {
	twigIndex, err := twig.NewTwigIndexer(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, twigIndex.Close()) })
	require.NoError(t, twigIndex.Index(indexer.NewParsedFile(
		"/project/src/AppExtension.php",
		[]byte(`<?php
use Twig\Attribute\AsTwigTest;
class AppExtension extends \Twig\Extension\AbstractExtension
{
    public function getTests(): array
    {
        return [
            new \Twig\TwigTest('positive', $this->positive(...)),
            new \Twig\TwigTest('legacy_test', $this->legacy(...), [
                'deprecated' => '3.0',
            ]),
        ];
    }
    public function positive(int $value): bool { return $value > 0; }
    public function legacy(int $value): bool { return false; }

    #[AsTwigTest('uuid')]
    public function uuid(string $value): bool { return true; }
}`),
	)))
	provider := NewTwigCompletionProvider(
		"/project",
		twigIndex,
		nil,
		nil,
	)

	items := twigTestCompletionItems(
		t,
		provider,
		`{% if value is <caret> %}`,
	)
	labels := completionLabels(items)
	assert.Contains(t, labels, "defined")
	assert.Contains(t, labels, "same as")
	assert.Contains(t, labels, "positive")
	assert.Contains(t, labels, "uuid")

	partial := twigTestCompletionItems(
		t,
		provider,
		`{{ value is po<caret> }}`,
	)
	assert.Equal(t, []string{"positive"}, completionLabels(partial))

	negated := twigTestCompletionItems(
		t,
		provider,
		`{% set valid = value is not uu<caret> %}`,
	)
	assert.Equal(t, []string{"uuid"}, completionLabels(negated))

	deprecated := requireCompletion(t, items, "legacy_test")
	assert.True(t, deprecated.Deprecated)
	assert.Equal(t, "Deprecated Twig test", deprecated.Detail)
}

func TestTwigTestCompletionDoesNotLeakIntoOtherExpressions(t *testing.T) {
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
		`{% if <caret>value %}`,
		`{% if value <caret> %}`,
		`{{ value <caret> }}`,
		`{# value is <caret> #}`,
		`{% if value is positive <caret> %}`,
	} {
		labels := completionLabels(
			twigTestCompletionItems(t, provider, source),
		)
		assert.NotContains(t, labels, "defined", source)
		assert.NotContains(t, labels, "same as", source)
	}
}

func twigTestCompletionItems(
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
