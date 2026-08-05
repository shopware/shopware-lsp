package hover

import (
	"context"
	"strings"
	"testing"

	"github.com/shopware/shopware-lsp/internal/indexer"
	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	"github.com/shopware/shopware-lsp/internal/translation"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTranslationHoverShowsLocalesAndLocations(t *testing.T) {
	idx, err := translation.NewIndex(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, idx.Close()) })
	require.NoError(t, idx.Index(indexer.NewParsedFile(
		"/project/translations/messages.en.yaml",
		[]byte("hello.world: Hello world\n"),
	)))
	require.NoError(t, idx.Index(indexer.NewParsedFile(
		"/project/translations/messages.de.yaml",
		[]byte("hello.world: Hallo Welt\n"),
	)))
	provider := NewTranslationHoverProvider("/project", idx, nil)

	source := `{{ 'hello.world'|trans }}`
	document := lsp.NewTextDocument(
		"file:///project/template.twig",
		source,
		1,
	)
	node := document.SyntaxTree.Root.NodeAtOffset(
		uint32(strings.Index(source, "hello.world") + 2),
	)
	params := &protocol.HoverParams{}
	params.TextDocument.URI = document.URI
	result, err := provider.GetHover(context.Background(), &lsp.HoverRequest{
		HoverParams: params,
		SyntaxContext: lsp.SyntaxContext{
			Document:        document,
			Language:        document.SyntaxLanguage,
			DocumentContent: document.Text,
			DocumentTree:    document.SyntaxTree,
			LineIndex:       document.LineIndex,
			Root:            document.SyntaxTree.Root,
			Node:            node,
		},
	})
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Contains(
		t,
		result.Contents.Value,
		"**Symfony translation** `hello.world` · `messages`",
	)
	assert.Contains(t, result.Contents.Value, "**en**: Hello world")
	assert.Contains(t, result.Contents.Value, "**de**: Hallo Welt")
	assert.Contains(
		t,
		result.Contents.Value,
		"`translations/messages.en.yaml:1`",
	)
	assert.NotNil(t, result.Range)
}
