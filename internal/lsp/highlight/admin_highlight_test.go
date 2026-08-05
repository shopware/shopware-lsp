package highlight

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/shopware/shopware-lsp/internal/admin"
	"github.com/shopware/shopware-lsp/internal/indexer"
	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	"github.com/shopware/shopware-lsp/internal/uriutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAdminDocumentHighlightsUseLiveTwigPropOccurrences(t *testing.T) {
	root := t.TempDir()
	idx, err := admin.NewAdminComponentIndexer(filepath.Join(root, "cache"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, idx.Close()) })
	adminRoot := filepath.Join(
		root, "src/Administration/Resources/app/administration/src",
	)
	definitionPath := filepath.Join(adminRoot, "component/sw-card/index.ts")
	templatePath := filepath.Join(
		filepath.Dir(definitionPath), "sw-card.html.twig",
	)
	consumerPath := filepath.Join(adminRoot, "page/consumer.html.twig")
	definitionSource := `import template from './sw-card.html.twig';
Component.register('sw-card', {
    template,
    props: { title: { type: String } },
    methods: { save(id: string): void {} },
});`
	for path, source := range map[string]string{
		definitionPath: definitionSource,
		templatePath:   `{{ save(product.id) }}`,
		consumerPath:   `<sw-card :title="first" />`,
	} {
		require.NoError(t, idx.Index(indexer.NewParsedFile(
			path, []byte(source),
		)))
	}
	provider := NewAdminDocumentHighlightProvider(idx)
	liveConsumer := `<sw-card :title="first" /><sw-card :title="second" />`
	highlights, document := adminDocumentHighlights(
		t, provider, consumerPath, liveConsumer, ":title=\"first\"",
	)
	require.Len(t, highlights, 2)
	assert.Equal(t, []string{"title", "title"}, adminHighlightTexts(
		document, highlights,
	))

	liveTemplate := `{{ save(product.id) }}<button @click="save(other.id)" />`
	highlights, document = adminDocumentHighlights(
		t, provider, templatePath, liveTemplate, "save(product.id)",
	)
	require.Len(t, highlights, 2)
	assert.Equal(t, []string{"save", "save"}, adminHighlightTexts(
		document, highlights,
	))
}

func TestAdminDocumentHighlightsReplaceStaleJavaScriptOccurrences(t *testing.T) {
	root := t.TempDir()
	idx, err := admin.NewAdminComponentIndexer(filepath.Join(root, "cache"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, idx.Close()) })
	path := filepath.Join(
		root,
		"src/Administration/Resources/app/administration/src/component/sw-card/index.ts",
	)
	persisted := `Component.register('sw-card', {});`
	require.NoError(t, idx.Index(indexer.NewParsedFile(
		path, []byte(persisted),
	)))
	live := `Component.register('sw-card', {});

Component.override('sw-card', {});`
	highlights, document := adminDocumentHighlights(
		t, NewAdminDocumentHighlightProvider(idx), path, live,
		"'sw-card'",
	)
	require.Len(t, highlights, 2)
	assert.Equal(t, []string{"sw-card", "sw-card"}, adminHighlightTexts(
		document, highlights,
	))
}

func adminDocumentHighlights(
	t *testing.T,
	provider *AdminDocumentHighlightProvider,
	path,
	source,
	needle string,
) ([]protocol.DocumentHighlight, *lsp.TextDocument) {
	t.Helper()
	document := lsp.NewTextDocument(uriutil.FileURI(path), source, 2)
	offset := uint32(strings.Index(source, needle) + 1)
	line, character := document.LineIndex.PositionUTF16(offset)
	params := &protocol.DocumentHighlightParams{
		TextDocument: protocol.TextDocumentIdentifier{URI: document.URI},
		Position: protocol.Position{
			Line: int(line), Character: int(character),
		},
	}
	node := document.SyntaxTree.Root.NodeAtOffset(offset)
	highlights, err := provider.GetDocumentHighlights(
		context.Background(),
		&lsp.DocumentHighlightRequest{
			DocumentHighlightParams: params,
			SyntaxContext: lsp.SyntaxContext{
				Document: document, Language: document.SyntaxLanguage,
				DocumentContent: document.Text,
				DocumentTree:    document.SyntaxTree,
				LineIndex:       document.LineIndex,
				Root:            document.SyntaxTree.Root,
				Node:            node,
			},
		},
	)
	require.NoError(t, err)
	return highlights, document
}

func adminHighlightTexts(
	document *lsp.TextDocument,
	highlights []protocol.DocumentHighlight,
) []string {
	result := make([]string, 0, len(highlights))
	for _, highlight := range highlights {
		start := document.LineIndex.OffsetUTF16(
			uint32(highlight.Range.Start.Line),
			uint32(highlight.Range.Start.Character),
		)
		end := document.LineIndex.OffsetUTF16(
			uint32(highlight.Range.End.Line),
			uint32(highlight.Range.End.Character),
		)
		result = append(result, string(document.Text[start:end]))
	}
	return result
}
