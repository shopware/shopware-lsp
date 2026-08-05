package inlay

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/shopware/shopware-lsp/internal/indexer"
	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	"github.com/shopware/shopware-lsp/internal/php"
	"github.com/shopware/shopware-lsp/internal/symfony"
)

func TestRoutePathInlayHintsResolvePHPAndTwigRouteNames(t *testing.T) {
	routes, err := symfony.NewRouteIndexer(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, routes.Close()) })
	require.NoError(t, routes.Index(indexer.NewParsedFile(
		"/project/config/routes.yaml",
		[]byte(`product.show:
  path: /products/{id}
  controller: App\Controller\ProductController::show
  methods: [GET, HEAD]
`),
	)))
	provider := NewRoutePathProvider(routes)
	for name, document := range map[string]*lsp.TextDocument{
		"php": lsp.NewTextDocument(
			"file:///project/src/ProductController.php",
			`<?php $this->redirectToRoute('product.show');`,
			1,
		),
		"twig": lsp.NewTextDocument(
			"file:///project/templates/product.html.twig",
			`{{ path('product.show', {id: product.id}) }}`,
			1,
		),
	} {
		t.Run(name, func(t *testing.T) {
			hints, hintErr := provider.GetInlayHints(
				context.Background(),
				inlayHintRequest(document),
			)
			require.NoError(t, hintErr)
			require.Len(t, hints, 1)
			parts, ok := hints[0].Label.([]protocol.InlayHintLabelPart)
			require.True(t, ok)
			require.Len(t, parts, 1)
			assert.Equal(t, "→ /products/{id}", parts[0].Value)
			require.NotNil(t, parts[0].Location)
			assert.Equal(
				t,
				"file:///project/config/routes.yaml",
				parts[0].Location.URI,
			)
			assert.Contains(t, hints[0].Tooltip, `Route "product.show"`)
			assert.Contains(t, hints[0].Tooltip, "GET|HEAD /products/{id}")
			assert.True(t, hints[0].PaddingLeft)
		})
	}
}

func TestRoutePathInlayHintsUseTypedPHPGeneratorReceivers(t *testing.T) {
	routes, err := symfony.NewRouteIndexer(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, routes.Close()) })
	require.NoError(t, routes.Index(indexer.NewParsedFile(
		"/project/config/routes.yaml",
		[]byte("product.show:\n  path: /products/{id}\n"),
	)))
	phpIndex, err := php.NewPHPIndex(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, phpIndex.Close()) })
	for path, source := range map[string]string{
		"/project/vendor/AbstractController.php": `<?php
namespace Symfony\Bundle\FrameworkBundle\Controller;
abstract class AbstractController {
    protected function redirectToRoute(string $route): mixed {}
}
`,
		"/project/vendor/UrlGeneratorInterface.php": `<?php
namespace Symfony\Component\Routing\Generator;
interface UrlGeneratorInterface {
    public function generate(string $name): string;
}
`,
	} {
		require.NoError(t, phpIndex.Index(indexer.NewParsedFile(
			path,
			[]byte(source),
		)))
	}
	source := `<?php
namespace App;
use Symfony\Bundle\FrameworkBundle\Controller\AbstractController;
use Symfony\Component\Routing\Generator\UrlGeneratorInterface;
final class Other { public function generate(string $name): string {} }
final class ProductController extends AbstractController {
    public function show(UrlGeneratorInterface $router, Other $other): void {
        $this->redirectToRoute('product.show');
        $router->generate('product.show');
        $other->generate('product.show');
    }
}
`
	document := lsp.NewTextDocument(
		"file:///project/src/ProductController.php",
		source,
		1,
	)
	hints, err := NewRoutePathProvider(
		routes,
		phpIndex,
	).GetInlayHints(context.Background(), inlayHintRequest(document))
	require.NoError(t, err)
	require.Len(t, hints, 2)
	assert.Equal(
		t,
		[]string{"→ /products/{id}", "→ /products/{id}"},
		inlayHintLabels(hints),
	)
}

func TestRoutePathInlayHintsIgnoreUnknownDynamicAndUnrelatedStrings(
	t *testing.T,
) {
	routes, err := symfony.NewRouteIndexer(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, routes.Close()) })
	require.NoError(t, routes.Index(indexer.NewParsedFile(
		"/project/config/routes.yaml",
		[]byte("known:\n  path: /known\n"),
	)))
	provider := NewRoutePathProvider(routes)
	document := lsp.NewTextDocument(
		"file:///project/templates/page.html.twig",
		`{{ path(routeName) }} {{ path('missing') }} {{ 'known' }}`,
		1,
	)
	hints, err := provider.GetInlayHints(
		context.Background(),
		inlayHintRequest(document),
	)
	require.NoError(t, err)
	assert.Empty(t, hints)
}
