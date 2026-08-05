package hover

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/shopware/shopware-lsp/internal/indexer"
	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	twigparser "github.com/shopware/shopware-lsp/internal/parser/twig"
	twigquery "github.com/shopware/shopware-lsp/internal/parser/twig/query"
	twigsyntax "github.com/shopware/shopware-lsp/internal/parser/twig/syntax"
	"github.com/shopware/shopware-lsp/internal/snippet"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestExtractLocaleFromPath(t *testing.T) {
	tests := []struct {
		name     string
		path     string
		expected string
	}{
		{
			name:     "locale in filename with dash",
			path:     "src/Storefront/Resources/snippet/de_DE/storefront.de-DE.json",
			expected: "de-DE",
		},
		{
			name:     "locale in filename with underscore",
			path:     "src/Storefront/Resources/snippet/en_GB/storefront.en_GB.json",
			expected: "en-GB",
		},
		{
			name:     "locale in directory with dash",
			path:     "src/Core/Resources/snippet/de-DE/messages.json",
			expected: "de-DE",
		},
		{
			name:     "locale in directory with underscore",
			path:     "src/Core/Resources/snippet/de_DE/messages.json",
			expected: "de-DE",
		},
		{
			name:     "locale in directory after snippet folder",
			path:     "vendor/shopware/core/Resources/snippet/en_GB/storefront.json",
			expected: "en-GB",
		},
		{
			name:     "short locale code in directory",
			path:     "src/Resources/snippet/de/messages.json",
			expected: "de",
		},
		{
			name:     "short locale code in filename",
			path:     "src/Resources/snippet/translations.de.json",
			expected: "de",
		},
		{
			name:     "no locale found",
			path:     "src/Resources/translations/messages.json",
			expected: "unknown",
		},
		{
			name:     "multiple locale patterns - prefer filename",
			path:     "src/Resources/snippet/de_DE/storefront.en-GB.json",
			expected: "en-GB",
		},
		{
			name:     "windows path with locale in directory",
			path:     "src\\Storefront\\Resources\\snippet\\de_DE\\storefront.json",
			expected: "de-DE",
		},
		{
			name:     "windows path with locale in filename",
			path:     "src\\Storefront\\Resources\\snippet\\translations\\storefront.de-DE.json",
			expected: "de-DE",
		},
		{
			name:     "locale with different case",
			path:     "src/Resources/snippet/DE_DE/messages.json",
			expected: "DE-DE",
		},
		{
			name:     "complex filename with multiple dots",
			path:     "src/snippet/storefront.frontend.de-DE.min.json",
			expected: "de-DE",
		},
		{
			name:     "locale at root level",
			path:     "de-DE/messages.json",
			expected: "de-DE",
		},
		{
			name:     "deeply nested path",
			path:     "vendor/shopware/platform/src/Storefront/Resources/snippet/de_DE/storefront.json",
			expected: "de-DE",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := extractLocaleFromPath(tt.path)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestIsLocalePattern(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected bool
	}{
		{
			name:     "valid locale with dash",
			input:    "de-DE",
			expected: true,
		},
		{
			name:     "valid locale with underscore",
			input:    "en_GB",
			expected: true,
		},
		{
			name:     "valid short locale",
			input:    "de",
			expected: true,
		},
		{
			name:     "valid short locale uppercase",
			input:    "FR",
			expected: true,
		},
		{
			name:     "invalid - too long",
			input:    "deutsch",
			expected: false,
		},
		{
			name:     "invalid - too short",
			input:    "d",
			expected: false,
		},
		{
			name:     "invalid - wrong separator position",
			input:    "d-eDE",
			expected: false,
		},
		{
			name:     "invalid - no separator",
			input:    "deDE",
			expected: false,
		},
		{
			name:     "invalid - wrong length with separator",
			input:    "de-D",
			expected: false,
		},
		{
			name:     "empty string",
			input:    "",
			expected: false,
		},
		{
			name:     "numbers only",
			input:    "12-34",
			expected: true, // technically matches pattern
		},
		{
			name:     "mixed case locale",
			input:    "De-dE",
			expected: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := isLocalePattern(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestNormalizeLocale(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "underscore to dash",
			input:    "de_DE",
			expected: "de-DE",
		},
		{
			name:     "already normalized",
			input:    "en-GB",
			expected: "en-GB",
		},
		{
			name:     "multiple underscores",
			input:    "de_DE_formal",
			expected: "de-DE-formal",
		},
		{
			name:     "no underscores",
			input:    "de",
			expected: "de",
		},
		{
			name:     "empty string",
			input:    "",
			expected: "",
		},
		{
			name:     "mixed separators",
			input:    "de_DE-CH",
			expected: "de-DE-CH",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := normalizeLocale(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestSnippetHoverPatternMatching(t *testing.T) {
	tests := []struct {
		name           string
		code           string
		expectFrontend bool
		expectAdmin    bool
	}{
		{
			name:           "frontend trans filter hover",
			code:           `{{ 'product.detail.title'|trans }}`,
			expectFrontend: true,
			expectAdmin:    false,
		},
		{
			name:           "admin $tc hover",
			code:           `{{ $tc('sw-product.detail.title') }}`,
			expectFrontend: false,
			expectAdmin:    true,
		},
		{
			name:           "admin $t hover",
			code:           `{{ $t('global.button.save') }}`,
			expectFrontend: false,
			expectAdmin:    true,
		},
		{
			name:           "non-snippet string",
			code:           `{{ 'just text' }}`,
			expectFrontend: false,
			expectAdmin:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := twigparser.Parse(tt.code)
			strings := twigquery.Nodes(result.Tree.Root, twigsyntax.TwigLiteralString)
			if len(strings) == 0 {
				t.Fatal("Could not find string node")
			}

			frontendMatch := twigquery.StringInFilter(strings[0], "trans")
			adminMatch := twigquery.StringInFunction(strings[0], "$tc", "$t")

			assert.Equal(t, tt.expectFrontend, frontendMatch, "Frontend pattern for: %s", tt.code)
			assert.Equal(t, tt.expectAdmin, adminMatch, "Admin pattern for: %s", tt.code)
		})
	}
}

func TestAdminSnippetHoverInsideVueBoundTwigAttribute(t *testing.T) {
	root := t.TempDir()
	snippetIndex, err := snippet.NewSnippetIndexer(filepath.Join(root, "cache"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, snippetIndex.Close()) })
	require.NoError(t, snippetIndex.Index(indexer.NewParsedFile(
		filepath.Join(
			root,
			"src/Resources/app/administration/src/module/demo/snippet/en-GB.json",
		),
		[]byte(`{"admin":{"settings":{"title":"Settings"}}}`),
	)))
	source := `<mt-button :label="$t('admin.settings.title')" />`
	document := lsp.NewTextDocument(
		"file:///project/src/Resources/app/administration/view.html.twig",
		source,
		1,
	)
	offset := uint32(strings.Index(source, "admin.settings.title") + 3)
	line, character := document.LineIndex.PositionUTF16(offset)
	params := &protocol.HoverParams{}
	params.TextDocument.URI = document.URI
	params.Position.Line = int(line)
	params.Position.Character = int(character)
	result, err := NewSnippetHoverProvider(root, snippetIndex).GetHover(
		context.Background(),
		&lsp.HoverRequest{
			HoverParams: params,
			SyntaxContext: lsp.SyntaxContext{
				Document: document, DocumentContent: document.Text,
				DocumentTree: document.SyntaxTree,
				LineIndex:    document.LineIndex,
				Root:         document.SyntaxTree.Root,
				Node: document.SyntaxTree.Root.NodeAtOffset(
					offset,
				),
			},
		},
	)
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Contains(t, result.Contents.Value, "admin.settings.title")
	assert.Contains(t, result.Contents.Value, "Settings")
}

func TestAdminSnippetHoverInJavaScriptReferences(t *testing.T) {
	root := t.TempDir()
	snippetIndex, err := snippet.NewSnippetIndexer(filepath.Join(root, "cache"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, snippetIndex.Close()) })
	require.NoError(t, snippetIndex.Index(indexer.NewParsedFile(
		filepath.Join(
			root,
			"src/Resources/app/administration/src/module/demo/snippet/en-GB.json",
		),
		[]byte(`{"admin":{"settings":{"title":"Settings"}}}`),
	)))
	for name, source := range map[string]string{
		"root translator": `this.$root.$t('admin.settings.title')`,
		"navigation label": `Module.register('demo', {
            navigation: [{ label: 'admin.settings.title' }],
        });`,
	} {
		t.Run(name, func(t *testing.T) {
			document := lsp.NewTextDocument(
				"file:///project/src/Resources/app/administration/index.js",
				source,
				1,
			)
			offset := uint32(strings.Index(source, "admin.settings.title") + 3)
			line, character := document.LineIndex.PositionUTF16(offset)
			params := &protocol.HoverParams{}
			params.TextDocument.URI = document.URI
			params.Position.Line = int(line)
			params.Position.Character = int(character)
			result, err := NewSnippetHoverProvider(root, snippetIndex).GetHover(
				context.Background(),
				&lsp.HoverRequest{
					HoverParams: params,
					SyntaxContext: lsp.SyntaxContext{
						Document: document, DocumentContent: document.Text,
						DocumentTree: document.SyntaxTree,
						LineIndex:    document.LineIndex,
						Root:         document.SyntaxTree.Root,
						Node: document.SyntaxTree.Root.NodeAtOffset(
							offset,
						),
					},
				},
			)
			require.NoError(t, err)
			require.NotNil(t, result)
			assert.Contains(t, result.Contents.Value, "admin.settings.title")
			assert.Contains(t, result.Contents.Value, "Settings")
		})
	}
}
