package completion

import (
	"context"
	"strings"
	"testing"

	"github.com/shopware/shopware-lsp/internal/indexer"
	"github.com/shopware/shopware-lsp/internal/php"
	"github.com/shopware/shopware-lsp/internal/twig"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTwigMemberCompletionsMarkDeprecatedPHPMembers(t *testing.T) {
	provider := twigMemberDeprecationCompletionProvider(t)
	for _, source := range []string{
		`{# @var bar \Foo\Bar #} {{ bar. }}`,
		`{{ ustring('value'). }}`,
		`{{ 'value'|u. }}`,
	} {
		_, request := twigCompletionAt(
			"file:///project/templates/page.html.twig",
			source,
			strings.LastIndex(source, ".")+1,
		)
		items := provider.GetCompletions(context.Background(), request)
		deprecated := requireCompletion(t, items, "deprecated")
		assert.True(t, deprecated.Deprecated, source)
		assert.Contains(t, deprecated.Documentation.Value, "Deprecated")
		active := requireCompletion(t, items, "active")
		assert.False(t, active.Deprecated, source)
	}

	source := `{# @var bar \Foo\Bar #} {{ bar. }}`
	_, request := twigCompletionAt(
		"file:///project/templates/page.html.twig",
		source,
		strings.LastIndex(source, ".")+1,
	)
	property := requireCompletion(
		t,
		provider.GetCompletions(context.Background(), request),
		"deprecatedProperty",
	)
	assert.True(t, property.Deprecated)
}

func twigMemberDeprecationCompletionProvider(
	t *testing.T,
) *TwigCompletionProvider {
	t.Helper()
	cache := t.TempDir()
	phpIndex, err := php.NewPHPIndex(cache)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, phpIndex.Close()) })
	twigIndex, err := twig.NewTwigIndexer(cache)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, twigIndex.Close()) })
	twigIndex.SetDependencies(phpIndex, nil)
	path := "/project/src/Twig/StringExtension.php"
	source := []byte(`<?php
namespace Foo {
    class Bar {
        /** @deprecated */
        public string $deprecatedProperty;

        public function getActive(): string { return ''; }

        /** @deprecated */
        public function getDeprecated(): static { return $this; }
    }
}
namespace Twig\Extra\String {
    class StringExtension extends \Twig_Extension {
        public function getFilters(): array {
            return [new \Twig_SimpleFilter('u', [$this, 'createBar'])];
        }
        public function getFunctions(): array {
            return [new \Twig_SimpleFunction('ustring', [$this, 'createBar'])];
        }
        public function createBar(?string $text = null): \Foo\Bar {
            return new \Foo\Bar();
        }
    }
}`)
	require.NoError(t, phpIndex.Index(indexer.NewParsedFile(path, source)))
	require.NoError(t, twigIndex.Index(indexer.NewParsedFile(path, source)))
	return NewTwigCompletionProvider(
		"/project",
		twigIndex,
		nil,
		phpIndex,
	)
}
