package hover

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/shopware/shopware-lsp/internal/indexer"
	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	"github.com/shopware/shopware-lsp/internal/php"
	"github.com/shopware/shopware-lsp/internal/twig"
	"github.com/shopware/shopware-lsp/internal/uriutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTwigRenderBlockHoverShowsTemplateAndDeclarations(t *testing.T) {
	root := t.TempDir()
	phpIndex, err := php.NewPHPIndex(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, phpIndex.Close()) })
	require.NoError(t, phpIndex.Index(indexer.NewParsedFile(
		filepath.Join(root, "vendor", "AbstractController.php"),
		[]byte(`<?php
namespace Symfony\Bundle\FrameworkBundle\Controller;
abstract class AbstractController {}`),
	)))
	twigIndex, err := twig.NewTwigIndexer(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, twigIndex.Close()) })
	require.NoError(t, twigIndex.Index(indexer.NewParsedFile(
		filepath.Join(root, "templates", "base.html.twig"),
		[]byte(`{% block content %}{% endblock %}`),
	)))

	source := `<?php
namespace App;
class Controller extends \Symfony\Bundle\FrameworkBundle\Controller\AbstractController {
    public function page(): void {
        $this->renderBlock('base.html.twig', 'content');
    }
}`
	path := filepath.Join(root, "src", "Controller.php")
	require.NoError(t, phpIndex.Index(indexer.NewParsedFile(
		path,
		[]byte(source),
	)))
	document := lsp.NewTextDocument(uriutil.FileURI(path), source, 1)
	offset := strings.Index(source, "'content'") + 2
	node := document.SyntaxTree.Root.NodeAtOffset(uint32(offset))
	line, character := document.LineIndex.PositionUTF16(uint32(offset))
	params := &protocol.HoverParams{}
	params.TextDocument.URI = document.URI
	params.Position.Line = int(line)
	params.Position.Character = int(character)
	ctx := phpIndex.AddDocumentContext(
		context.Background(),
		path,
		1,
		node,
		document.SyntaxTree.Root,
	)
	result, err := NewTwigRenderBlockHoverProvider(
		root,
		twigIndex,
		phpIndex,
	).GetHover(ctx, &lsp.HoverRequest{
		HoverParams: params,
		SyntaxContext: lsp.SyntaxContext{
			Document:        document,
			DocumentContent: document.Text,
			Root:            document.SyntaxTree.Root,
			Node:            node,
			LineIndex:       document.LineIndex,
		},
	})
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Contains(t, result.Contents.Value, "Twig block")
	assert.Contains(t, result.Contents.Value, "`content`")
	assert.Contains(t, result.Contents.Value, "`base.html.twig`")
	assert.Contains(t, result.Contents.Value, "templates/base.html.twig:1")
	require.NotNil(t, result.Range)
	require.Equal(t, len("content"), result.Range.End.Character-result.Range.Start.Character)
}
