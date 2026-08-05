package symfony

import (
	"testing"

	"github.com/shopware/shopware-lsp/internal/indexer"
	"github.com/stretchr/testify/require"
)

func TestRouteIndexerFindsAbsoluteAndPartialRequestURLs(t *testing.T) {
	routeIndex, err := NewRouteIndexer(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, routeIndex.Close()) })
	require.NoError(t, routeIndex.Index(indexer.NewParsedFile(
		"/project/config/routes.yaml",
		[]byte(`product.show:
    path: /edit/{id}
car.show:
    path: /car/{edit}/foobar
static.show:
    path: /class-like-route
`),
	)))

	for searchPath, routeName := range map[string]string{
		"https://shop.example/edit/foo.bar?preview=1#details": "product.show",
		"https://shop.example/class-like-route":               "static.show",
		"ar/12/foo":                                           "car.show",
		"/edit/{id}":                                          "product.show",
	} {
		routes, findErr := routeIndex.FindRoutesByPath(searchPath)
		require.NoError(t, findErr)
		requireRouteNamed(t, routes, routeName)
	}

	routes, err := routeIndex.FindRoutesByPath("/edit/foo/bar")
	require.NoError(t, err)
	for _, route := range routes {
		require.NotEqual(t, "product.show", route.Name)
	}
}

func TestRouteIndexerRanksExactPathMatchesBeforePartialMatches(t *testing.T) {
	routeIndex, err := NewRouteIndexer(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, routeIndex.Close()) })
	require.NoError(t, routeIndex.Index(indexer.NewParsedFile(
		"/project/config/routes.yaml",
		[]byte(`partial:
    path: /store-api/sitemap/{filePath}
exact:
    path: /sitemap/{filePath}
`),
	)))

	routes, err := routeIndex.FindRoutesByPath("/sitemap/shop.xml.gz")
	require.NoError(t, err)
	require.Len(t, routes, 2)
	require.Equal(t, "exact", routes[0].Name)
	require.Equal(t, "partial", routes[1].Name)
}

func TestSortRoutesPrefersSourceDefinitionsOverGeneratedCatalogs(
	t *testing.T,
) {
	t.Parallel()

	routes := []Route{
		{
			Name:     "generated",
			Path:     "/sitemap/{filePath}",
			FilePath: "/project/var/cache/dev/url_generating_routes.php",
		},
		{
			Name:     "source",
			Path:     "/sitemap/{filePath}",
			FilePath: "/project/src/SitemapController.php",
		},
	}
	sortRoutes(routes)

	require.Equal(t, "source", routes[0].Name)
	require.Equal(t, "generated", routes[1].Name)
}

func TestRouteIndexerIndexesRoutingConfiguratorRoutes(t *testing.T) {
	routeIndex, err := NewRouteIndexer(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, routeIndex.Close()) })
	require.NoError(t, routeIndex.Index(indexer.NewParsedFile(
		"/project/config/routes.php",
		[]byte(`<?php
use Symfony\Component\Routing\Loader\Configurator\RoutingConfigurator;

return static function (RoutingConfigurator $routes): void {
    $routes->namePrefix('api_')->prefix('/api')
        ->add('product.show', '/products/{id}')
        ->controller([\App\Controller\ProductController::class, 'show'])
        ->methods(['GET', 'HEAD']);
};`),
	)))

	routes, err := routeIndex.GetRoute("api_product.show")
	require.NoError(t, err)
	require.Len(t, routes, 1)
	require.Equal(t, "/api/products/{id}", routes[0].Path)
	require.Equal(
		t,
		"App\\Controller\\ProductController::show",
		routes[0].Controller,
	)
	require.Equal(t, []string{"GET", "HEAD"}, routes[0].Methods)
	require.Equal(t, []string{"id"}, routes[0].Parameters())
}

func requireRouteNamed(t *testing.T, routes []Route, name string) {
	t.Helper()
	for _, route := range routes {
		if route.Name == name {
			return
		}
	}
	t.Fatalf("route %q not found in %#v", name, routes)
}
