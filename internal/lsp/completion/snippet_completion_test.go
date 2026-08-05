package completion

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

func TestSnippetCompletionPatternMatching(t *testing.T) {
	tests := []struct {
		name           string
		code           string
		expectFrontend bool
		expectAdmin    bool
	}{
		{
			name:           "frontend trans filter",
			code:           `{{ 'snippet.key'|trans }}`,
			expectFrontend: true,
			expectAdmin:    false,
		},
		{
			name:           "admin $tc function",
			code:           `{{ $tc('snippet.key') }}`,
			expectFrontend: false,
			expectAdmin:    true,
		},
		{
			name:           "admin $t function",
			code:           `{{ $t('snippet.key') }}`,
			expectFrontend: false,
			expectAdmin:    true,
		},
		{
			name:           "regular string - no completion",
			code:           `{{ 'just a string' }}`,
			expectFrontend: false,
			expectAdmin:    false,
		},
		{
			name:           "regular function call - no completion",
			code:           `{{ someFunc('arg') }}`,
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

			assert.Equal(t, tt.expectFrontend, frontendMatch, "Frontend pattern match for: %s", tt.code)
			assert.Equal(t, tt.expectAdmin, adminMatch, "Admin pattern match for: %s", tt.code)
		})
	}
}

func TestAdminSnippetCompletionInsideVueBoundTwigAttribute(t *testing.T) {
	root := t.TempDir()
	index, err := snippet.NewSnippetIndexer(filepath.Join(root, "cache"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, index.Close()) })
	require.NoError(t, index.Index(indexer.NewParsedFile(
		filepath.Join(
			root,
			"src/Resources/app/administration/src/module/demo/snippet/en-GB.json",
		),
		[]byte(`{"admin":{"settings":{"title":"Settings"}}}`),
	)))
	source := `<mt-button :label="$t('admin.sett" />`
	document := lsp.NewTextDocument(
		"file:///project/src/Resources/app/administration/view.html.twig",
		source,
		1,
	)
	offset := uint32(strings.Index(source, "admin.sett") + len("admin.sett"))
	line, character := document.LineIndex.PositionUTF16(offset)
	params := &protocol.CompletionParams{}
	params.TextDocument.URI = document.URI
	params.Position.Line = int(line)
	params.Position.Character = int(character)
	items := NewSnippetCompletionProvider(index).GetCompletions(
		context.Background(),
		&lsp.CompletionRequest{
			CompletionParams: params,
			SyntaxContext: lsp.SyntaxContext{
				Document: document, DocumentContent: document.Text,
				DocumentTree: document.SyntaxTree,
				LineIndex:    document.LineIndex,
				Root:         document.SyntaxTree.Root,
				Node: document.SyntaxTree.Root.NodeAtOffset(
					offset - 1,
				),
			},
		},
	)
	requireCompletion(t, items, "admin.settings.title")
}

func TestAdminSnippetCompletionInJavaScriptReferences(t *testing.T) {
	root := t.TempDir()
	index, err := snippet.NewSnippetIndexer(filepath.Join(root, "cache"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, index.Close()) })
	require.NoError(t, index.Index(indexer.NewParsedFile(
		filepath.Join(
			root,
			"src/Resources/app/administration/src/module/demo/snippet/en-GB.json",
		),
		[]byte(`{"admin":{"settings":{"title":"Settings"}}}`),
	)))
	for name, source := range map[string]string{
		"injected translator": `translator.$t('admin.sett')`,
		"module title":        `Module.register('demo', { title: 'admin.sett' });`,
		"navigation label": `Shopware.Module.register('demo', {
            navigation: [{ label: 'admin.sett' }],
        });`,
	} {
		t.Run(name, func(t *testing.T) {
			document := lsp.NewTextDocument(
				"file:///project/src/Resources/app/administration/index.ts",
				source,
				1,
			)
			offset := uint32(strings.Index(source, "admin.sett") + len("admin.sett"))
			line, character := document.LineIndex.PositionUTF16(offset)
			params := &protocol.CompletionParams{}
			params.TextDocument.URI = document.URI
			params.Position.Line = int(line)
			params.Position.Character = int(character)
			items := NewSnippetCompletionProvider(index).GetCompletions(
				context.Background(),
				&lsp.CompletionRequest{
					CompletionParams: params,
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
			requireCompletion(t, items, "admin.settings.title")
		})
	}
}
