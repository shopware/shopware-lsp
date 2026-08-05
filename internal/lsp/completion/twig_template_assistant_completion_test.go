package completion

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/shopware/shopware-lsp/internal/indexer"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	"github.com/shopware/shopware-lsp/internal/php"
	"github.com/shopware/shopware-lsp/internal/twig"
	"github.com/stretchr/testify/require"
)

func TestPHPDocTemplateAssistantTagCompletion(t *testing.T) {
	root := t.TempDir()
	twigIndex, err := twig.NewTwigIndexer(filepath.Join(root, "twig"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, twigIndex.Close()) })
	require.NoError(t, twigIndex.Index(indexer.NewParsedFile(
		filepath.Join(root, "templates", "page.html.twig"),
		[]byte("page"),
	)))
	phpIndex, err := php.NewPHPIndex(filepath.Join(root, "php"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, phpIndex.Close()) })
	require.NoError(t, phpIndex.Index(indexer.NewParsedFile(
		filepath.Join(root, "src", "TemplateAssistant.php"),
		[]byte(`<?php
/** @param string $template #Template */
function resolve_template(string $template): void {}
`),
	)))
	source := "<?php resolve_template('page.html');"
	path := filepath.Join(root, "src", "Usage.php")
	offset := strings.Index(source, "page.html") + len("page.html")
	document, request := bundleResourceCompletionRequest(
		t,
		path,
		source,
		offset,
	)
	ctx := phpIndex.AddDocumentContext(
		context.Background(),
		path,
		document.Version,
		request.Node,
		request.Root,
	)
	item := requireCompletion(
		t,
		NewTwigCompletionProvider(
			root,
			twigIndex,
			nil,
			phpIndex,
		).GetCompletions(ctx, request),
		"page.html.twig",
	)
	edit, ok := item.TextEdit.(protocol.TextEdit)
	require.True(t, ok)
	require.Equal(t, "page.html.twig", edit.NewText)
	require.Equal(
		t,
		"page.html",
		completionRangeText(document, edit.Range),
	)
}
