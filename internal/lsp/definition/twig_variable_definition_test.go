package definition

import (
	"context"
	"os"
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

func TestTwigControllerVariableDefinition(t *testing.T) {
	root := t.TempDir()
	controllerPath := filepath.Join(root, "src", "ProductController.php")
	require.NoError(t, os.MkdirAll(filepath.Dir(controllerPath), 0o755))
	controllerSource := `<?php
namespace App;
class Product {}
class ProductController {
    public function show(Product $product) {
        return $this->render('product/show.html.twig', [
            'product' => $product,
        ]);
    }
}`
	require.NoError(t, os.WriteFile(
		controllerPath,
		[]byte(controllerSource),
		0o644,
	))

	phpIndex, err := php.NewPHPIndex(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, phpIndex.Close()) })
	require.NoError(t, phpIndex.Index(indexer.NewParsedFile(
		controllerPath,
		[]byte(controllerSource),
	)))
	twigIndex, err := twig.NewTwigIndexer(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, twigIndex.Close()) })

	templatePath := filepath.Join(root, "templates", "product", "show.html.twig")
	source := `{{ product }}`
	document := lsp.NewTextDocument(uriutil.FileURI(templatePath), source, 1)
	offset := strings.Index(source, "product") + 1
	node := document.SyntaxTree.Root.NodeAtOffset(uint32(offset))
	params := &protocol.DefinitionParams{}
	params.TextDocument.URI = document.URI
	request := &lsp.DefinitionRequest{
		DefinitionParams: params,
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

	locations := NewTwigDefinitionProvider(
		root,
		twigIndex,
		nil,
		phpIndex,
	).GetDefinition(context.Background(), request)
	require.Len(t, locations, 1)
	assert.Equal(t, uriutil.FileURI(controllerPath), locations[0].URI)
	assert.Equal(t, 6, locations[0].Range.Start.Line)
}

func TestTwigGlobalVariableDefinition(t *testing.T) {
	root := t.TempDir()
	configPath := filepath.Join(root, "config/packages/twig.yaml")
	require.NoError(t, os.MkdirAll(filepath.Dir(configPath), 0o755))
	config := []byte(`twig:
  globals:
    clock: '@app.clock'
`)
	require.NoError(t, os.WriteFile(configPath, config, 0o644))
	twigIndex, err := twig.NewTwigIndexer(filepath.Join(root, ".cache"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, twigIndex.Close()) })
	require.NoError(t, twigIndex.Index(indexer.NewParsedFile(
		configPath,
		config,
	)))

	templatePath := filepath.Join(root, "templates/page.html.twig")
	source := `{{ clock }}`
	document := lsp.NewTextDocument(
		uriutil.FileURI(templatePath),
		source,
		1,
	)
	offset := strings.Index(source, "clock") + 1
	node := document.SyntaxTree.Root.NodeAtOffset(uint32(offset))
	params := &protocol.DefinitionParams{}
	params.TextDocument.URI = document.URI
	params.Position.Character = offset
	locations := NewTwigDefinitionProvider(
		root,
		twigIndex,
		nil,
		nil,
	).GetDefinition(context.Background(), &lsp.DefinitionRequest{
		DefinitionParams: params,
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
	require.Len(t, locations, 1)
	require.Equal(t, uriutil.FileURI(configPath), locations[0].URI)
	require.Equal(t, 2, locations[0].Range.Start.Line)
	require.Equal(t, 4, locations[0].Range.Start.Character)
	require.Equal(t, 9, locations[0].Range.End.Character)
}
