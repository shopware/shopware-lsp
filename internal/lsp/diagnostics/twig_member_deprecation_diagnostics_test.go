package diagnostics

import (
	"context"
	"testing"

	"github.com/shopware/shopware-lsp/internal/indexer"
	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	"github.com/shopware/shopware-lsp/internal/php"
	"github.com/shopware/shopware-lsp/internal/twig"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTwigMemberDeprecationDiagnostics(t *testing.T) {
	phpIndex, twigIndex := twigMemberDeprecationIndexes(t)
	document := lsp.NewTextDocument(
		"file:///project/templates/page.html.twig",
		`{# @var bar \Foo\Bar #}
{{ bar.next.deprecated }}
{{ bar.deprecatedProperty }}
{{ bar.deprecatedAttributeProperty }}
{{ ustring('value').deprecated }}
{{ 'value'|u.deprecated }}
`,
		1,
	)
	result, err := NewTwigMemberDeprecationAnalyzer(
		twigIndex,
		phpIndex,
	).Analyze(context.Background(), document)
	require.NoError(t, err)
	require.Len(t, result, 5)

	messages := make([]string, 0, len(result))
	ranges := make([]string, 0, len(result))
	for _, diagnostic := range result {
		assert.Equal(t, deprecatedTwigMemberCode, diagnostic.ID)
		assert.Equal(t, protocol.DiagnosticSeverityHint, diagnostic.Severity)
		assert.Equal(
			t,
			[]protocol.DiagnosticTag{protocol.DiagnosticTagDeprecated},
			diagnostic.Tags,
		)
		messages = append(messages, diagnostic.Message)
		ranges = append(
			ranges,
			problemRangeText(document, diagnostic.Range),
		)
	}
	assert.Contains(t, messages, "Method 'Bar::getDeprecated' is deprecated")
	assert.Contains(t, messages, "Field 'Bar::$deprecatedProperty' is deprecated")
	assert.Contains(
		t,
		messages,
		"Field 'Bar::$deprecatedAttributeProperty' is deprecated",
	)
	assert.Equal(
		t,
		[]string{
			"deprecated",
			"deprecatedProperty",
			"deprecatedAttributeProperty",
			"deprecated",
			"deprecated",
		},
		ranges,
	)
}

func twigMemberDeprecationIndexes(
	t *testing.T,
) (*php.PHPIndex, *twig.TwigIndexer) {
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

        #[\Deprecated]
        public string $deprecatedAttributeProperty;

        public function getNext(): Bar { return new Bar(); }

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
	return phpIndex, twigIndex
}
