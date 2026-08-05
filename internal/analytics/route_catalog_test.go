package analytics

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/shopware/shopware-lsp/internal/indexer"
	"github.com/shopware/shopware-lsp/internal/php"
	"github.com/shopware/shopware-lsp/internal/symfony"
	"github.com/shopware/shopware-lsp/internal/twig"
	"github.com/shopware/shopware-lsp/internal/uriutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRouteCatalogResolvesControllersTemplatesAndFilters(t *testing.T) {
	root := t.TempDir()
	cache := t.TempDir()
	phpIndex, err := php.NewPHPIndex(cache)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, phpIndex.Close()) })
	routeIndex, err := symfony.NewRouteIndexer(cache)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, routeIndex.Close()) })
	serviceIndex, err := symfony.NewServiceIndex(root, cache)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, serviceIndex.Close()) })
	serviceIndex.SetPHPIndex(phpIndex)
	twigIndex, err := twig.NewTwigIndexer(cache)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, twigIndex.Close()) })

	controllerPath := filepath.Join(
		root,
		"src",
		"Controller",
		"ProductController.php",
	)
	controllerSource := `<?php
namespace App\Controller;

final class ProductController
{
    #[Template('product/attribute.html.twig')]
    #[Route('/products/{id}', name: 'product.show', methods: [Request::METHOD_GET])]
    public function show(): array
    {
        return $this->render('product/show.html.twig');
    }
}
`
	require.NoError(t, os.MkdirAll(filepath.Dir(controllerPath), 0o755))
	require.NoError(t, os.WriteFile(
		controllerPath,
		[]byte(controllerSource),
		0o644,
	))
	controllerFile := indexer.NewParsedFile(
		controllerPath,
		[]byte(controllerSource),
	)
	require.NoError(t, phpIndex.Index(controllerFile))
	require.NoError(t, routeIndex.Index(controllerFile))
	require.NoError(t, twigIndex.Index(controllerFile))

	servicePath := filepath.Join(root, "config", "services.yaml")
	serviceSource := `services:
  app.product_controller:
    class: App\Controller\ProductController
`
	routePath := filepath.Join(root, "config", "routes.yaml")
	routeSource := `product.service:
  path: /service/products/{id}
  methods: [POST]
  controller: app.product_controller:show
`
	require.NoError(t, os.MkdirAll(filepath.Dir(servicePath), 0o755))
	serviceFile := indexer.NewParsedFile(servicePath, []byte(serviceSource))
	routeFile := indexer.NewParsedFile(routePath, []byte(routeSource))
	require.NoError(t, serviceIndex.Index(serviceFile))
	require.NoError(t, routeIndex.Index(routeFile))

	provider := NewRouteCatalogProvider(
		root,
		routeIndex,
		serviceIndex,
		phpIndex,
		twigIndex,
	)
	entries, err := provider.Catalog(
		context.Background(),
		RouteCatalogRequest{},
	)
	require.NoError(t, err)
	require.Len(t, entries, 2)
	direct := routeCatalogEntry(t, entries, "product.show")
	assert.Equal(t, "/products/{id}", direct.Path)
	assert.Equal(t, []string{"GET"}, direct.Methods)
	assert.Equal(
		t,
		"App\\Controller\\ProductController::show",
		direct.ResolvedController,
	)
	assert.Equal(t, uriutil.FileURI(controllerPath), direct.SourceURI)
	assert.Equal(t, 7, direct.SourceLine)
	assert.Equal(t, uriutil.FileURI(controllerPath), direct.ControllerURI)
	assert.Equal(t, 8, direct.ControllerLine)
	assert.Equal(t, []string{
		"product/attribute.html.twig",
		"product/show.html.twig",
	}, direct.Templates)

	service := routeCatalogEntry(t, entries, "product.service")
	assert.Equal(t, []string{"POST"}, service.Methods)
	assert.Equal(
		t,
		"App\\Controller\\ProductController::show",
		service.ResolvedController,
	)
	assert.Equal(t, []string{
		"product/attribute.html.twig",
		"product/show.html.twig",
	}, service.Templates)

	for _, request := range []RouteCatalogRequest{
		{URLPath: "https://shop.test/products/42?preview=1"},
		{Controller: "ProductController::SHOW"},
		{FileGlob: "src/**/ProductController.php"},
	} {
		filtered, filterErr := provider.Catalog(context.Background(), request)
		require.NoError(t, filterErr)
		require.NotEmpty(t, filtered)
	}
	filtered, err := provider.Catalog(context.Background(), RouteCatalogRequest{
		RouteName: "PRODUCT.SERVICE",
		FileGlob:  "src/Controller/*.php",
	})
	require.NoError(t, err)
	require.Len(t, filtered, 1)
	assert.Equal(t, "product.service", filtered[0].Name)
}

func TestAntPathMatchSupportsZeroOrNestedDoubleStarSegments(t *testing.T) {
	assert.True(t, antPathMatch(
		"src/**/ProductController.php",
		"src/ProductController.php",
	))
	assert.True(t, antPathMatch(
		"src/**/ProductController.php",
		"src/Admin/Catalog/ProductController.php",
	))
	assert.False(t, antPathMatch(
		"src/**/ProductController.php",
		"tests/ProductController.php",
	))
}

func routeCatalogEntry(
	t *testing.T,
	entries []RouteCatalogEntry,
	name string,
) RouteCatalogEntry {
	t.Helper()
	for _, entry := range entries {
		if entry.Name == name {
			return entry
		}
	}
	t.Fatalf("route catalog entry %q not found in %#v", name, entries)
	return RouteCatalogEntry{}
}
