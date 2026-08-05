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
	twigparser "github.com/shopware/shopware-lsp/internal/parser/twig"
	twigquery "github.com/shopware/shopware-lsp/internal/parser/twig/query"
	twigsyntax "github.com/shopware/shopware-lsp/internal/parser/twig/syntax"
	yamlparser "github.com/shopware/shopware-lsp/internal/parser/yaml"
	yamlquery "github.com/shopware/shopware-lsp/internal/parser/yaml/query"
	yamlsyntax "github.com/shopware/shopware-lsp/internal/parser/yaml/syntax"
	"github.com/shopware/shopware-lsp/internal/php"
	"github.com/shopware/shopware-lsp/internal/symfony"
	"github.com/shopware/shopware-lsp/internal/uriutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestControllerDefinitionResolvesServiceMethod(t *testing.T) {
	phpIndex, err := php.NewPHPIndex(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, phpIndex.Close()) })
	classPath := filepath.Join(t.TempDir(), "ProductController.php")
	content := []byte(`<?php
namespace App\Controller;
class ProductController {
    public function show(): void {}
}`)
	require.NoError(t, os.WriteFile(classPath, content, 0o644))
	require.NoError(t, phpIndex.Index(indexer.NewParsedFile(classPath, content)))

	serviceIndex, err := symfony.NewServiceIndex(t.TempDir(), t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, serviceIndex.Close()) })
	require.NoError(t, serviceIndex.Index(indexer.NewParsedFile(
		"/project/services.yaml",
		[]byte(`services:
  app.product_controller:
    class: App\Controller\ProductController
`),
	)))

	source := `product.show:
  path: /products
  controller: app.product_controller:show
`
	root := yamlparser.Parse(source).Tree.Root
	var node *yamlsyntax.Node
	for _, scalar := range yamlquery.Nodes(root, yamlsyntax.YamlScalar) {
		if yamlquery.ScalarValue(scalar) == "app.product_controller:show" {
			node = scalar
			break
		}
	}
	require.NotNil(t, node)
	params := &protocol.DefinitionParams{}
	params.TextDocument.URI = "file:///project/routes.yaml"
	provider := NewControllerDefinitionProvider(serviceIndex, phpIndex)
	locations := provider.GetDefinition(context.Background(), &lsp.DefinitionRequest{
		DefinitionParams: params,
		SyntaxContext: lsp.SyntaxContext{
			Root: root,
			Node: node,
		},
	})
	require.Len(t, locations, 1)
	assert.Equal(t, uriutil.FileURI(classPath), locations[0].URI)
	assert.Equal(t, 3, locations[0].Range.Start.Line)
}

func TestControllerDefinitionResolvesEscapedTwigController(t *testing.T) {
	phpIndex, err := php.NewPHPIndex(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, phpIndex.Close()) })
	classPath := filepath.Join(t.TempDir(), "NavController.php")
	content := []byte(`<?php
namespace App\Controller;
class NavController {
    public function menuAction(): void {}
}`)
	require.NoError(t, os.WriteFile(classPath, content, 0o644))
	require.NoError(t, phpIndex.Index(indexer.NewParsedFile(classPath, content)))

	source := `{{ render(controller('\\App\\Controller\\NavController::menuAction')) }}`
	root := twigparser.Parse(source).Tree.Root
	literals := twigquery.Nodes(root, twigsyntax.TwigLiteralString)
	require.Len(t, literals, 1)
	params := &protocol.DefinitionParams{}
	params.TextDocument.URI = "file:///project/navigation.html.twig"
	locations := NewControllerDefinitionProvider(nil, phpIndex).GetDefinition(
		context.Background(),
		&lsp.DefinitionRequest{
			DefinitionParams: params,
			SyntaxContext: lsp.SyntaxContext{
				Root: root,
				Node: literals[0],
			},
		},
	)
	require.Len(t, locations, 1)
	assert.Equal(t, uriutil.FileURI(classPath), locations[0].URI)
	assert.Equal(t, 3, locations[0].Range.Start.Line)
}

func TestControllerDefinitionResolvesTwigSeeServiceMethod(t *testing.T) {
	phpIndex, err := php.NewPHPIndex(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, phpIndex.Close()) })
	classPath := filepath.Join(t.TempDir(), "ProductController.php")
	content := []byte(`<?php
namespace App\Controller;
class ProductController {
    public function show(): void {}
}`)
	require.NoError(t, os.WriteFile(classPath, content, 0o644))
	require.NoError(t, phpIndex.Index(indexer.NewParsedFile(classPath, content)))
	serviceIndex, err := symfony.NewServiceIndex(t.TempDir(), t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, serviceIndex.Close()) })
	require.NoError(t, serviceIndex.Index(indexer.NewParsedFile(
		"/project/services.yaml",
		[]byte(`services:
  app.product_controller:
    class: App\Controller\ProductController
`),
	)))

	source := `{# @see app.product_controller:show #}`
	document := lsp.NewTextDocument(
		"file:///project/page.html.twig",
		source,
		1,
	)
	offset := uint32(strings.Index(source, "product_controller") + 2)
	line, character := document.LineIndex.PositionUTF16(offset)
	params := &protocol.DefinitionParams{}
	params.TextDocument.URI = document.URI
	params.Position.Line = int(line)
	params.Position.Character = int(character)
	locations := NewControllerDefinitionProvider(
		serviceIndex,
		phpIndex,
	).GetDefinition(
		context.Background(),
		&lsp.DefinitionRequest{
			DefinitionParams: params,
			SyntaxContext: lsp.SyntaxContext{
				DocumentContent: document.Text,
				LineIndex:       document.LineIndex,
				Root:            document.SyntaxTree.Root,
				Node: document.SyntaxTree.Root.NodeAtOffset(
					offset,
				),
			},
		},
	)
	require.Len(t, locations, 1)
	assert.Equal(t, uriutil.FileURI(classPath), locations[0].URI)
	assert.Equal(t, 3, locations[0].Range.Start.Line)
}
