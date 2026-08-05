package diagnostics

import (
	"context"
	"testing"

	"github.com/shopware/shopware-lsp/internal/indexer"
	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	"github.com/shopware/shopware-lsp/internal/twig"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTwigDeprecationDiagnosticsForFunctionsFiltersAndTests(t *testing.T) {
	twigIndex := twigDeprecationIndex(t)
	document := lsp.NewTextDocument(
		"file:///project/templates/page.html.twig",
		`{{ legacy_function() }}
{{ active_function() }}
{{ value|legacy_filter }}
{{ value|active_filter }}
{% if value is legacy_test %}{% endif %}
{% if value is active_test %}{% endif %}
`,
		1,
	)
	result, err := NewTwigDeprecationAnalyzer(twigIndex).
		Analyze(context.Background(), document)
	require.NoError(t, err)
	require.Len(t, result, 3)
	assert.Equal(t, deprecatedTwigFunctionCode, result[0].ID)
	assert.Equal(
		t,
		"legacy_function",
		problemRangeText(document, result[0].Range),
	)
	assert.Contains(t, result[0].Message, "Use active_function")
	assert.Equal(t, deprecatedTwigFilterCode, result[1].ID)
	assert.Equal(
		t,
		"legacy_filter",
		problemRangeText(document, result[1].Range),
	)
	assert.Equal(t, deprecatedTwigTestCode, result[2].ID)
	assert.Equal(
		t,
		"legacy_test",
		problemRangeText(document, result[2].Range),
	)
	for _, diagnostic := range result {
		assert.Equal(
			t,
			protocol.DiagnosticSeverityHint,
			diagnostic.Severity,
		)
		assert.Equal(
			t,
			[]protocol.DiagnosticTag{protocol.DiagnosticTagDeprecated},
			diagnostic.Tags,
		)
	}
}

func TestTwigDeprecationDiagnosticsRespectActiveDuplicate(t *testing.T) {
	twigIndex := twigDeprecationIndex(t)
	require.NoError(t, twigIndex.Index(indexer.NewParsedFile(
		"/project/src/OverrideExtension.php",
		[]byte(`<?php
use Twig\Extension\AbstractExtension;
use Twig\TwigFunction;
class OverrideExtension extends AbstractExtension {
    public function getFunctions(): array {
        return [new TwigFunction('legacy_function', $this->active(...))];
    }
    public function active(): string { return ''; }
}`),
	)))
	document := lsp.NewTextDocument(
		"file:///project/templates/page.html.twig",
		`{{ legacy_function() }}`,
		1,
	)
	result, err := NewTwigDeprecationAnalyzer(twigIndex).
		Analyze(context.Background(), document)
	require.NoError(t, err)
	assert.Empty(t, result)
}

func TestTwigDeprecationDiagnosticsForCustomTags(t *testing.T) {
	twigIndex := twigDeprecationIndex(t)
	document := lsp.NewTextDocument(
		"file:///project/templates/page.html.twig",
		`{% legacy_tag %}
{% endlegacy_tag %}
{% active_tag %}
{% raw %}{% legacy_tag %}{% endraw %}
{% verbatim %}{% legacy_tag %}{% endverbatim %}
`,
		1,
	)
	result, err := NewTwigDeprecationAnalyzer(twigIndex).
		Analyze(context.Background(), document)
	require.NoError(t, err)
	require.Len(t, result, 2)
	for index, expected := range []string{"legacy_tag", "endlegacy_tag"} {
		assert.Equal(t, deprecatedTwigTagCode, result[index].ID)
		assert.Equal(
			t,
			expected,
			problemRangeText(document, result[index].Range),
		)
		assert.Contains(t, result[index].Message, "modern_tag")
		assert.Equal(
			t,
			protocol.DiagnosticSeverityHint,
			result[index].Severity,
		)
		assert.Equal(
			t,
			[]protocol.DiagnosticTag{
				protocol.DiagnosticTagDeprecated,
			},
			result[index].Tags,
		)
	}
}

func TestTwigTagDeprecationDiagnosticsRespectActiveDuplicate(t *testing.T) {
	twigIndex := twigDeprecationIndex(t)
	require.NoError(t, twigIndex.Index(indexer.NewParsedFile(
		"/project/src/OverrideTokenParser.php",
		[]byte(`<?php
class OverrideTokenParser implements \Twig\TokenParser\TokenParserInterface {
    public function getTag(): string { return 'legacy_tag'; }
}`),
	)))
	document := lsp.NewTextDocument(
		"file:///project/templates/page.html.twig",
		`{% legacy_tag %}`,
		1,
	)
	result, err := NewTwigDeprecationAnalyzer(twigIndex).
		Analyze(context.Background(), document)
	require.NoError(t, err)
	assert.Empty(t, result)
}

func twigDeprecationIndex(t *testing.T) *twig.TwigIndexer {
	t.Helper()
	twigIndex, err := twig.NewTwigIndexer(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, twigIndex.Close()) })
	require.NoError(t, twigIndex.Index(indexer.NewParsedFile(
		"/project/src/AppExtension.php",
		[]byte(`<?php
use Twig\Extension\AbstractExtension;
use Twig\TwigFilter;
use Twig\TwigFunction;
use Twig\TwigTest;
class AppExtension extends AbstractExtension {
    public function getFunctions(): array {
        return [
            new TwigFunction('legacy_function', $this->legacyFunction(...)),
            new TwigFunction('active_function', $this->activeFunction(...)),
        ];
    }
    public function getFilters(): array {
        return [
            new TwigFilter('legacy_filter', $this->legacyFilter(...)),
            new TwigFilter('active_filter', $this->activeFilter(...)),
        ];
    }
    public function getTests(): array {
        return [
            new TwigTest('legacy_test', $this->legacyTest(...), [
                'deprecated' => '1.0',
            ]),
            new TwigTest('active_test', $this->activeTest(...)),
        ];
    }
    public function legacyFunction(): string {
        trigger_deprecation(
            'app',
            '1.0',
            'The "legacy_function" Twig function is deprecated. Use active_function instead.',
        );
        return '';
    }
    public function activeFunction(): string { return ''; }
    public function legacyFilter(string $value): string {
        Feature::triggerDeprecationOrThrow(
            'v2',
            'The "legacy_filter" Twig filter is deprecated. Use active_filter instead.',
        );
        return $value;
    }
    public function activeFilter(string $value): string { return $value; }
    public function legacyTest(string $value): bool { return false; }
    public function activeTest(string $value): bool { return true; }
}`),
	)))
	require.NoError(t, twigIndex.Index(indexer.NewParsedFile(
		"/project/src/LegacyTokenParser.php",
		[]byte(`<?php
/** @deprecated Use modern_tag instead. */
class LegacyTokenParser implements \Twig\TokenParser\TokenParserInterface {
    public function getTag(): string { return 'legacy_tag'; }
}
class ActiveTokenParser implements \Twig\TokenParser\TokenParserInterface {
    public function getTag(): string { return 'active_tag'; }
}`),
	)))
	return twigIndex
}
