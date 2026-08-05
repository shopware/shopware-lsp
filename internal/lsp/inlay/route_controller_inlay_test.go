package inlay

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/shopware/shopware-lsp/internal/indexer"
	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	"github.com/shopware/shopware-lsp/internal/php"
	"github.com/shopware/shopware-lsp/internal/symfony"
)

func TestYAMLRouteControllerInlayHintsResolveClassServiceAndInvoker(
	t *testing.T,
) {
	provider := newRouteControllerProvider(t)
	document := lsp.NewTextDocument(
		"file:///project/config/routes.yaml",
		`product.show:
  path: /products/{id}
  controller: App\Controller\ProductController::show
product.invoke:
  path: /products
  defaults:
    _controller: app.product_controller
`,
		1,
	)

	hints, err := provider.GetInlayHints(
		context.Background(),
		inlayHintRequest(document),
	)
	require.NoError(t, err)
	require.Len(t, hints, 2)
	assert.Equal(t, []string{
		"→ ProductController::show",
		"→ ProductController::__invoke",
	}, inlayHintLabels(hints))
	assert.Equal(t, 2, hints[0].Position.Line)
	assert.Equal(t, 6, hints[1].Position.Line)
	assert.Equal(t, protocol.InlayHintKindType, hints[0].Kind)
	assert.True(t, hints[0].PaddingLeft)
	parts, ok := hints[0].Label.([]protocol.InlayHintLabelPart)
	require.True(t, ok)
	require.Len(t, parts, 1)
	require.NotNil(t, parts[0].Location)
	assert.Equal(
		t,
		"file",
		parts[0].Location.URI[:len("file")],
	)
	assert.Equal(t, 4, parts[0].Location.Range.Start.Line)
	assert.Equal(
		t,
		`Route "product.show"
App\Controller\ProductController::show`,
		hints[0].Tooltip,
	)
	assert.Equal(
		t,
		`Route "product.invoke"
app.product_controller → App\Controller\ProductController::__invoke`,
		hints[1].Tooltip,
	)
}

func TestXMLRouteControllerInlayHintsResolveAttributeAndDefault(
	t *testing.T,
) {
	provider := newRouteControllerProvider(t)
	document := lsp.NewTextDocument(
		"file:///project/config/routes.xml",
		`<routes>
  <route id="product.show" path="/products/{id}" controller="app.product_controller:show"/>
  <route id="product.invoke" path="/products">
    <default key="_controller">App\Controller\ProductController</default>
  </route>
</routes>`,
		1,
	)

	hints, err := provider.GetInlayHints(
		context.Background(),
		inlayHintRequest(document),
	)
	require.NoError(t, err)
	require.Len(t, hints, 2)
	assert.Equal(t, []string{
		"→ ProductController::show",
		"→ ProductController::__invoke",
	}, inlayHintLabels(hints))
	assert.Equal(t, 1, hints[0].Position.Line)
	assert.Equal(t, 3, hints[1].Position.Line)
	assert.Contains(t, hints[0].Tooltip, "app.product_controller:show → ")
	assert.Contains(t, hints[1].Tooltip, `Route "product.invoke"`)
}

func TestPHPRouteControllerInlayHintsResolveImplicitAttributeController(
	t *testing.T,
) {
	provider := newRouteControllerProvider(t)
	source := `<?php
namespace App\Controller;

final class ProductController
{
    #[Route('/products/{id}', name: 'product.show')]
    public function show(): void {}

    #[Route('/products', name: 'product.invoke')]
    public function __invoke(): void {}
}
`
	document := lsp.NewTextDocument(
		"file:///project/src/ProductController.php",
		source,
		1,
	)

	hints, err := provider.GetInlayHints(
		context.Background(),
		inlayHintRequest(document),
	)
	require.NoError(t, err)
	require.Len(t, hints, 2)
	assert.Equal(t, []string{
		"→ ProductController::show",
		"→ ProductController::__invoke",
	}, inlayHintLabels(hints))
	assert.Equal(t, 5, hints[0].Position.Line)
	assert.Equal(t, 8, hints[1].Position.Line)
	assert.Equal(
		t,
		int(document.LineIndex.LineUTF16Length(5)),
		hints[0].Position.Character,
	)
}

func TestRouteControllerInlayHintsRespectRangeAndRequirePublicMethod(
	t *testing.T,
) {
	provider := newRouteControllerProvider(t)
	document := lsp.NewTextDocument(
		"file:///project/config/routes.yaml",
		`product.show:
  path: /products/{id}
  controller: App\Controller\ProductController::show
product.hidden:
  path: /hidden
  controller: App\Controller\ProductController::hidden
product.invoke:
  path: /products
  controller: app.product_controller
`,
		1,
	)
	request := inlayHintRequest(document)
	request.Range.Start.Line = 5
	request.Range.End.Line = 8
	request.Range.End.Character = 80

	hints, err := provider.GetInlayHints(context.Background(), request)
	require.NoError(t, err)
	require.Len(t, hints, 1)
	assert.Equal(
		t,
		"→ ProductController::__invoke",
		inlayHintLabels(hints)[0],
	)
	assert.Equal(t, 8, hints[0].Position.Line)
}

func newRouteControllerProvider(t *testing.T) *RouteControllerProvider {
	t.Helper()
	root := t.TempDir()
	phpIndex, err := php.NewPHPIndex(filepath.Join(root, "php-cache"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, phpIndex.Close()) })
	classPath := filepath.Join(root, "src", "ProductController.php")
	classSource := []byte(`<?php
namespace App\Controller;
class ProductController
{
    public function show(): void {}
    public function __invoke(): void {}
    private function hidden(): void {}
}
`)
	require.NoError(t, os.MkdirAll(filepath.Dir(classPath), 0o755))
	require.NoError(t, os.WriteFile(classPath, classSource, 0o644))
	require.NoError(t, phpIndex.Index(indexer.NewParsedFile(
		classPath,
		classSource,
	)))

	serviceIndex, err := symfony.NewServiceIndex(
		root,
		filepath.Join(root, "symfony-cache"),
	)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, serviceIndex.Close()) })
	require.NoError(t, serviceIndex.Index(indexer.NewParsedFile(
		"/project/config/services.yaml",
		[]byte(`services:
  app.product_controller:
    class: App\Controller\ProductController
`),
	)))
	return NewRouteControllerProvider(serviceIndex, phpIndex)
}
