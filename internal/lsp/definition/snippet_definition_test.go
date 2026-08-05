package definition

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
	"github.com/shopware/shopware-lsp/internal/uriutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSnippetDefinitionPatternMatching(t *testing.T) {
	tests := []struct {
		name           string
		code           string
		snippetKey     string
		expectFrontend bool
		expectAdmin    bool
	}{
		{
			name:           "frontend trans filter",
			code:           `{{ 'checkout.cart.title'|trans }}`,
			snippetKey:     "checkout.cart.title",
			expectFrontend: true,
			expectAdmin:    false,
		},
		{
			name:           "admin $tc function",
			code:           `{{ $tc('sw-settings.index.title') }}`,
			snippetKey:     "sw-settings.index.title",
			expectFrontend: false,
			expectAdmin:    true,
		},
		{
			name:           "admin $t function",
			code:           `{{ $t('global.actions.save') }}`,
			snippetKey:     "global.actions.save",
			expectFrontend: false,
			expectAdmin:    true,
		},
		{
			name:           "admin $tc with parameters",
			code:           `{{ $tc('sw-product.list.count', items.length) }}`,
			snippetKey:     "sw-product.list.count",
			expectFrontend: false,
			expectAdmin:    true,
		},
		{
			name:           "trans filter with parameters",
			code:           `{{ 'checkout.cart.items'|trans({'%count%': count}) }}`,
			snippetKey:     "checkout.cart.items",
			expectFrontend: true,
			expectAdmin:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := twigparser.Parse(tt.code)
			var stringNode *twigsyntax.Node
			for _, candidate := range twigquery.Nodes(result.Tree.Root, twigsyntax.TwigLiteralString) {
				if twigquery.StringValue(candidate) == tt.snippetKey {
					stringNode = candidate
					break
				}
			}
			if stringNode == nil {
				t.Fatalf("Could not find string node with text '%s'", tt.snippetKey)
			}

			frontendMatch := twigquery.StringInFilter(stringNode, "trans")
			adminMatch := twigquery.StringInFunction(stringNode, "$tc", "$t")

			assert.Equal(t, tt.expectFrontend, frontendMatch, "Frontend pattern match for: %s", tt.code)
			assert.Equal(t, tt.expectAdmin, adminMatch, "Admin pattern match for: %s", tt.code)

			// Also verify we can extract the snippet key correctly
			extractedKey := twigquery.StringValue(stringNode)
			assert.Equal(t, tt.snippetKey, extractedKey, "Extracted snippet key for: %s", tt.code)
		})
	}
}

func TestAdminSnippetDefinitionInsideVueBoundTwigAttribute(t *testing.T) {
	root := t.TempDir()
	snippetIndex, err := snippet.NewSnippetIndexer(filepath.Join(root, "cache"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, snippetIndex.Close()) })
	snippetPath := filepath.Join(
		root,
		"src/Resources/app/administration/src/module/demo/snippet/en-GB.json",
	)
	require.NoError(t, snippetIndex.Index(indexer.NewParsedFile(
		snippetPath,
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
	params := &protocol.DefinitionParams{}
	params.TextDocument.URI = document.URI
	params.Position.Line = int(line)
	params.Position.Character = int(character)
	locations := NewSnippetDefinitionProvider(snippetIndex).GetDefinition(
		context.Background(),
		&lsp.DefinitionRequest{
			DefinitionParams: params,
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
	require.Len(t, locations, 1)
	assert.Equal(t, uriutil.FileURI(snippetPath), locations[0].URI)
}

func TestAdminSnippetDefinitionInJavaScriptReferences(t *testing.T) {
	root := t.TempDir()
	snippetIndex, err := snippet.NewSnippetIndexer(filepath.Join(root, "cache"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, snippetIndex.Close()) })
	snippetPath := filepath.Join(
		root,
		"src/Resources/app/administration/src/module/demo/snippet/en-GB.json",
	)
	require.NoError(t, snippetIndex.Index(indexer.NewParsedFile(
		snippetPath,
		[]byte(`{"admin":{"settings":{"title":"Settings"}}}`),
	)))
	for name, source := range map[string]string{
		"global service": `Shopware.Snippet.tc('admin.settings.title')`,
		"module description": `Module.register('demo', {
            description: 'admin.settings.title',
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
			params := &protocol.DefinitionParams{}
			params.TextDocument.URI = document.URI
			params.Position.Line = int(line)
			params.Position.Character = int(character)
			locations := NewSnippetDefinitionProvider(snippetIndex).GetDefinition(
				context.Background(),
				&lsp.DefinitionRequest{
					DefinitionParams: params,
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
			require.Len(t, locations, 1)
			assert.Equal(t, uriutil.FileURI(snippetPath), locations[0].URI)
		})
	}
}

func TestSnippetDefinitionEdgeCases(t *testing.T) {
	tests := []struct {
		name           string
		code           string
		expectFrontend bool
		expectAdmin    bool
	}{
		{
			name:           "nested in block",
			code:           `{% block content %}{{ $tc('my.key') }}{% endblock %}`,
			expectFrontend: false,
			expectAdmin:    true,
		},
		{
			name:           "in if condition",
			code:           `{% if $tc('condition.key') %}yes{% endif %}`,
			expectFrontend: false,
			expectAdmin:    true,
		},
		{
			name:           "in set statement",
			code:           `{% set label = 'my.label'|trans %}`,
			expectFrontend: true,
			expectAdmin:    false,
		},
		{
			name:           "multiple on same line",
			code:           `{{ $tc('first.key') }} - {{ $tc('second.key') }}`,
			expectFrontend: false,
			expectAdmin:    true,
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

			assert.Equal(t, tt.expectFrontend, frontendMatch, "Frontend pattern match for: %s", tt.code)
			assert.Equal(t, tt.expectAdmin, adminMatch, "Admin pattern match for: %s", tt.code)
		})
	}
}
