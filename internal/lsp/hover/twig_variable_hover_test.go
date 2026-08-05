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
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTwigControllerVariableHover(t *testing.T) {
	phpIndex, err := php.NewPHPIndex(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, phpIndex.Close()) })
	require.NoError(t, phpIndex.Index(indexer.NewParsedFile(
		"/project/src/ProductController.php",
		[]byte(`<?php
namespace App;
class Product {}
class ProductController {
    public function show(Product $product) {
        return $this->render('product/show.html.twig', [
            'product' => $product,
        ]);
    }
}`),
	)))

	source := `{{ product }}`
	document := lsp.NewTextDocument(
		"file:///project/templates/product/show.html.twig",
		source,
		1,
	)
	offset := strings.Index(source, "product") + 1
	node := document.SyntaxTree.Root.NodeAtOffset(uint32(offset))
	params := &protocol.HoverParams{}
	params.TextDocument.URI = document.URI
	request := &lsp.HoverRequest{
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
	}

	result, err := NewTwigHoverProvider(
		"/project",
		nil,
		phpIndex,
		nil,
	).GetHover(context.Background(), request)
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Contains(t, result.Contents.Value, "Twig variable")
	assert.Contains(t, result.Contents.Value, "App\\Product")
	assert.Contains(t, result.Contents.Value, "src/ProductController.php")
}

func TestTwigGlobalVariableHover(t *testing.T) {
	root := t.TempDir()
	twigIndex, err := twig.NewTwigIndexer(filepath.Join(root, ".cache"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, twigIndex.Close()) })
	configPath := filepath.Join(root, "config/packages/twig.yaml")
	require.NoError(t, twigIndex.Index(indexer.NewParsedFile(
		configPath,
		[]byte(`twig:
  globals:
    site_name: 'Store'
`),
	)))

	source := `{{ site_name }}`
	document := lsp.NewTextDocument(
		"file:///project/templates/page.html.twig",
		source,
		1,
	)
	offset := strings.Index(source, "site_name") + 1
	params := &protocol.HoverParams{}
	params.TextDocument.URI = document.URI
	params.Position.Character = offset
	result, err := NewTwigHoverProvider(
		root,
		nil,
		nil,
		twigIndex,
	).GetHover(context.Background(), &lsp.HoverRequest{
		HoverParams: params,
		SyntaxContext: lsp.SyntaxContext{
			Document:        document,
			Language:        document.SyntaxLanguage,
			DocumentContent: document.Text,
			DocumentTree:    document.SyntaxTree,
			LineIndex:       document.LineIndex,
			Root:            document.SyntaxTree.Root,
			Node: document.SyntaxTree.Root.NodeAtOffset(
				uint32(offset),
			),
		},
	})
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Contains(t, result.Contents.Value, "Twig global")
	assert.Contains(t, result.Contents.Value, "PHP type: `string`")
	assert.Contains(t, result.Contents.Value, "Twig configuration")
	assert.Contains(t, result.Contents.Value, "config/packages/twig.yaml")
}
