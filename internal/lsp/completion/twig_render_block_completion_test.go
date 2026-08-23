package completion

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

func TestTwigRenderBlockCompletionIncludesInheritedBlocksAndNamedArguments(
	t *testing.T,
) {
	twigIndex, phpIndex, root := renderBlockCompletionFixture(t)
	source := `<?php
namespace App;
class Controller extends \Symfony\Bundle\FrameworkBundle\Controller\AbstractController {
    public function page(): void {
        $this->renderBlock('child.html.twig', '');
        $this->renderBlockView(block: '', view: 'child.html.twig');
    }
}`
	path := filepath.Join(root, "src", "Controller.php")
	require.NoError(t, phpIndex.Index(indexer.NewParsedFile(
		path,
		[]byte(source),
	)))
	document := lsp.NewTextDocument(uriutil.FileURI(path), source, 1)
	provider := NewTwigRenderBlockCompletionProvider(twigIndex, phpIndex)

	start := 0
	for range 2 {
		relative := strings.Index(source[start:], "''")
		require.NotEqual(t, -1, relative)
		offset := start + relative + 1
		node := document.SyntaxTree.Root.NodeAtOffset(uint32(offset))
		line, character := document.LineIndex.PositionUTF16(uint32(offset))
		params := &protocol.CompletionParams{}
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
		items := provider.GetCompletions(ctx, &lsp.CompletionRequest{
			CompletionParams: params,
			SyntaxContext: lsp.SyntaxContext{
				Document:        document,
				DocumentContent: document.Text,
				Root:            document.SyntaxTree.Root,
				Node:            node,
				LineIndex:       document.LineIndex,
			},
		})
		content := requireCompletion(t, items, "content")
		assert.Contains(t, content.Documentation.Value, "Main page content.")
		requireCompletion(t, items, "sidebar")
		requireCompletion(t, items, "child")
		start = offset + 1
	}
}

func TestTwigRenderBlockCompletionRequiresAbstractController(t *testing.T) {
	twigIndex, phpIndex, root := renderBlockCompletionFixture(t)
	source := `<?php
namespace App;
class Unrelated {
    public function page(): void {
        $this->renderBlock('child.html.twig', '');
    }
}`
	path := filepath.Join(root, "src", "Unrelated.php")
	document := lsp.NewTextDocument(uriutil.FileURI(path), source, 1)
	offset := strings.Index(source, "''") + 1
	node := document.SyntaxTree.Root.NodeAtOffset(uint32(offset))
	line, character := document.LineIndex.PositionUTF16(uint32(offset))
	params := &protocol.CompletionParams{}
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
	require.Empty(t, NewTwigRenderBlockCompletionProvider(
		twigIndex,
		phpIndex,
	).GetCompletions(ctx, &lsp.CompletionRequest{
		CompletionParams: params,
		SyntaxContext: lsp.SyntaxContext{
			Document:        document,
			DocumentContent: document.Text,
			Root:            document.SyntaxTree.Root,
			Node:            node,
			LineIndex:       document.LineIndex,
		},
	}))
}

func renderBlockCompletionFixture(
	t *testing.T,
) (*twig.TwigIndexer, *php.PHPIndex, string) {
	t.Helper()
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
		[]byte(`{## Main page content. ##}
{% block content %}{% endblock %}
{% block sidebar %}{% endblock %}`),
	)))
	require.NoError(t, twigIndex.Index(indexer.NewParsedFile(
		filepath.Join(root, "templates", "child.html.twig"),
		[]byte(`{% extends 'base.html.twig' %}
{% block child %}{% endblock %}`),
	)))
	return twigIndex, phpIndex, root
}
