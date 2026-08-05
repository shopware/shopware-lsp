package hover

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/shopware/shopware-lsp/internal/indexer"
	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	"github.com/shopware/shopware-lsp/internal/twig"
)

func TestTwigIncludeParameterHoverDescribesTargetInput(t *testing.T) {
	root := t.TempDir()
	index, err := twig.NewTwigIndexer(filepath.Join(root, ".cache"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, index.Close()) })
	require.NoError(t, index.Index(indexer.NewParsedFile(
		filepath.Join(root, "templates/card.html.twig"),
		[]byte(`{{ title }}`),
	)))
	provider := NewTwigIncludeParameterHoverProvider(root, index, nil)
	source := `{% include 'card.html.twig' with {'title': value} %}`
	document := lsp.NewTextDocument(
		"file:///project/templates/page.html.twig",
		source,
		1,
	)
	offset := uint32(strings.Index(source, "title") + 1)
	line, character := document.LineIndex.PositionUTF16(offset)
	params := &protocol.HoverParams{}
	params.TextDocument.URI = document.URI
	params.Position.Line = int(line)
	params.Position.Character = int(character)
	result, err := provider.GetHover(
		context.Background(),
		&lsp.HoverRequest{
			HoverParams: params,
			SyntaxContext: lsp.SyntaxContext{
				Document:        document,
				Language:        document.SyntaxLanguage,
				DocumentContent: document.Text,
				DocumentTree:    document.SyntaxTree,
				LineIndex:       document.LineIndex,
				Root:            document.SyntaxTree.Root,
				Node: document.SyntaxTree.Root.NodeAtOffset(
					offset,
				),
			},
		},
	)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Contains(t, result.Contents.Value, "`title`")
	require.Contains(t, result.Contents.Value, "`card.html.twig`")
	require.Contains(t, result.Contents.Value, "templates/card.html.twig")
	require.NotNil(t, result.Range)
	require.Equal(t, 35, result.Range.Start.Character)
	require.Equal(t, 40, result.Range.End.Character)
}
