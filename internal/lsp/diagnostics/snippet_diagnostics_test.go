package diagnostics

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/shopware/shopware-lsp/internal/indexer"
	"github.com/shopware/shopware-lsp/internal/lsp"
	twigparser "github.com/shopware/shopware-lsp/internal/parser/twig"
	twigquery "github.com/shopware/shopware-lsp/internal/parser/twig/query"
	twigsyntax "github.com/shopware/shopware-lsp/internal/parser/twig/syntax"
	"github.com/shopware/shopware-lsp/internal/snippet"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSnippetDiagnosticsPatternDetection(t *testing.T) {
	tests := []struct {
		name                  string
		code                  string
		expectedFrontendCount int
		expectedAdminCount    int
	}{
		{
			name:                  "single frontend snippet",
			code:                  `{{ 'checkout.cart.title'|trans }}`,
			expectedFrontendCount: 1,
			expectedAdminCount:    0,
		},
		{
			name:                  "single admin snippet $tc",
			code:                  `{{ $tc('sw-settings.title') }}`,
			expectedFrontendCount: 0,
			expectedAdminCount:    1,
		},
		{
			name:                  "single admin snippet $t",
			code:                  `{{ $t('global.save') }}`,
			expectedFrontendCount: 0,
			expectedAdminCount:    1,
		},
		{
			name:                  "multiple frontend snippets",
			code:                  `{{ 'first.key'|trans }} {{ 'second.key'|trans }}`,
			expectedFrontendCount: 2,
			expectedAdminCount:    0,
		},
		{
			name:                  "multiple admin snippets",
			code:                  `{{ $tc('first.key') }} {{ $t('second.key') }}`,
			expectedFrontendCount: 0,
			expectedAdminCount:    2,
		},
		{
			name:                  "no snippets",
			code:                  `{{ 'just text' }}`,
			expectedFrontendCount: 0,
			expectedAdminCount:    0,
		},
		{
			name:                  "snippet in block",
			code:                  `{% block content %}{{ $tc('block.snippet') }}{% endblock %}`,
			expectedFrontendCount: 0,
			expectedAdminCount:    1,
		},
		{
			name: "multi-line template",
			code: `{% block header %}
{{ $tc('header.title') }}
{% endblock %}
{% block content %}
{{ $tc('content.text') }}
{% endblock %}`,
			expectedFrontendCount: 0,
			expectedAdminCount:    2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			parsed := twigparser.Parse(tt.code)

			var frontendMatches []*twigsyntax.Node
			for _, candidate := range twigquery.Nodes(parsed.Tree.Root, twigsyntax.TwigLiteralString) {
				if twigquery.StringInFilter(candidate, "trans") {
					frontendMatches = append(frontendMatches, candidate)
				}
			}

			adminMatches := twigquery.StringArgumentsInFunctions(parsed.Tree.Root, "$tc", "$t")

			assert.Len(t, frontendMatches, tt.expectedFrontendCount,
				"Frontend snippet count for: %s", tt.code)
			assert.Len(t, adminMatches, tt.expectedAdminCount,
				"Admin snippet count for: %s", tt.code)
		})
	}
}

func TestAdminSnippetDiagnosticsCoverVueBoundTwigAttributes(t *testing.T) {
	root := t.TempDir()
	snippetIndex, err := snippet.NewSnippetIndexer(filepath.Join(root, "cache"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, snippetIndex.Close()) })
	require.NoError(t, snippetIndex.Index(indexer.NewParsedFile(
		filepath.Join(
			root,
			"src/Resources/app/administration/src/module/demo/snippet/en-GB.json",
		),
		[]byte(`{"admin":{"known":"Known"}}`),
	)))
	source := `<div
    :title="$t('admin.known')"
    :label="$tc('admin.missing')"
    title="$t('admin.plain-attribute')"
/>`
	document := lsp.NewTextDocument(
		"file:///project/src/Resources/app/administration/view.html.twig",
		source,
		1,
	)
	problems, err := NewSnippetAnalyzer(snippetIndex).Analyze(
		context.Background(),
		document,
	)
	require.NoError(t, err)
	require.Len(t, problems, 1)
	assert.Equal(t, "admin.snippet.missing", string(problems[0].ID))
	assert.Equal(t, "admin.missing", source[problems[0].Range.Start:problems[0].Range.End])
}

func TestAdminSnippetDiagnosticsCoverJavaScriptTranslatorForms(t *testing.T) {
	root := t.TempDir()
	snippetIndex, err := snippet.NewSnippetIndexer(filepath.Join(root, "cache"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, snippetIndex.Close()) })
	require.NoError(t, snippetIndex.Index(indexer.NewParsedFile(
		filepath.Join(
			root,
			"src/Resources/app/administration/src/module/demo/snippet/en-GB.json",
		),
		[]byte(`{"admin":{"known":"Known"}}`),
	)))
	source := `
translator.$t('admin.known');
this.$root.$tc('admin.missing-root');
Shopware.Snippet.t('admin.missing-service');
other.t('admin.not-a-translation');
this.$t(dynamicKey);
Module.register('demo', {
    title: 'admin.known',
    description: 'admin.missing-module',
    navigation: [{ label: 'admin.known' }],
    routes: { index: { meta: { label: 'admin.not-a-module-label' } } },
});
`
	document := lsp.NewTextDocument(
		"file:///project/src/Resources/app/administration/index.ts",
		source,
		1,
	)
	problems, err := NewSnippetAnalyzer(snippetIndex).Analyze(
		context.Background(),
		document,
	)
	require.NoError(t, err)
	require.Len(t, problems, 3)
	assert.Equal(t, "'admin.missing-root'", source[problems[0].Range.Start:problems[0].Range.End])
	assert.Equal(t, "'admin.missing-service'", source[problems[1].Range.Start:problems[1].Range.End])
	assert.Equal(t, "'admin.missing-module'", source[problems[2].Range.Start:problems[2].Range.End])
}

func TestSnippetDiagnosticsAdminFileDetection(t *testing.T) {
	tests := []struct {
		name     string
		uri      string
		expected bool
	}{
		{
			name:     "admin twig file",
			uri:      "/project/custom/plugins/MyPlugin/src/Resources/app/administration/src/module/my-module/component/my-component/my-component.html.twig",
			expected: true,
		},
		{
			name:     "storefront twig file",
			uri:      "/project/custom/plugins/MyPlugin/src/Resources/views/storefront/page/checkout/cart.html.twig",
			expected: false,
		},
		{
			name:     "core storefront file",
			uri:      "/project/vendor/shopware/storefront/Resources/views/storefront/base.html.twig",
			expected: false,
		},
		{
			name:     "core admin file",
			uri:      "/project/vendor/shopware/administration/Resources/app/administration/src/module/sw-product/page/sw-product-list/sw-product-list.html.twig",
			expected: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Simple check that matches the logic in snippet_diagnostics.go
			isAdminFile := containsAdminPath(tt.uri)
			assert.Equal(t, tt.expected, isAdminFile, "Admin file detection for: %s", tt.uri)
		})
	}
}

func containsAdminPath(uri string) bool {
	return contains(uri, "/Resources/app/administration/")
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && findSubstring(s, substr)
}

func findSubstring(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

func TestSnippetKeyExtraction(t *testing.T) {
	tests := []struct {
		name        string
		code        string
		expectedKey string
	}{
		{
			name:        "simple frontend key",
			code:        `{{ 'checkout.cart.title'|trans }}`,
			expectedKey: "checkout.cart.title",
		},
		{
			name:        "simple admin key",
			code:        `{{ $tc('sw-settings.index.title') }}`,
			expectedKey: "sw-settings.index.title",
		},
		{
			name:        "key with parameters",
			code:        `{{ $tc('sw-product.count', { count: 5 }) }}`,
			expectedKey: "sw-product.count",
		},
		{
			name:        "frontend key with parameters",
			code:        `{{ 'checkout.items'|trans({'%count%': count}) }}`,
			expectedKey: "checkout.items",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			parsed := twigparser.Parse(tt.code)

			var matches []*twigsyntax.Node
			for _, candidate := range twigquery.Nodes(parsed.Tree.Root, twigsyntax.TwigLiteralString) {
				if twigquery.StringInFilter(candidate, "trans") {
					matches = append(matches, candidate)
				}
			}
			if len(matches) == 0 {
				matches = twigquery.StringArgumentsInFunctions(parsed.Tree.Root, "$tc", "$t")
			}

			assert.NotEmpty(t, matches, "Should find at least one match")

			if len(matches) > 0 {
				extractedKey := twigquery.StringValue(matches[0])
				assert.Equal(t, tt.expectedKey, extractedKey, "Extracted key for: %s", tt.code)
			}
		})
	}
}
