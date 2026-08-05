package symfony

import (
	"os"
	"testing"

	phpparser "github.com/shopware/shopware-lsp/internal/parser/php"
	phpsyntax "github.com/shopware/shopware-lsp/internal/parser/php/syntax"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestExtractRoutesFromFile(t *testing.T) {
	// Extract routes from the test file
	filePath := "testdata/controller.php"
	node, content := parsePHPFile(filePath)

	routes := parsePHPRoutes(filePath, node, content)

	// Verify we found only the method route
	assert.Len(t, routes, 1)

	// Verify method route data
	expectedRouteMethod := Route{
		Name:       "frontend.account.address.create",
		Path:       "/account/address/create", // Combined path
		FilePath:   filePath,
		Line:       14, // Line number of the Route attribute in the test file
		Controller: "App\\Controller\\Frontend\\Account\\AddressController::createAddress",
	}

	assert.Equal(t, expectedRouteMethod, routes[0])
}

func TestExtractRoutesWithBasePathFromFile(t *testing.T) {
	// Extract routes from the test file with base path
	filePath := "testdata/controller_base.php"
	node, content := parsePHPFile(filePath)

	routes := parsePHPRoutes(filePath, node, content)

	// Verify we found only the method route
	assert.Len(t, routes, 1)

	// Verify method route data with combined path
	expectedRouteMethod := Route{
		Name:       "foo",
		Path:       "/api/foo", // Base path + route path
		FilePath:   filePath,
		Line:       11, // Line number of the Route attribute in the test file
		Controller: "Shopware\\Core\\Api\\ApiController::foo",
	}

	assert.Equal(t, expectedRouteMethod, routes[0])
}

func TestExtractRoutesWithClassPathAndMethods(t *testing.T) {
	source := []byte(`<?php
namespace App\Controller;
#[Route(path: '/products')]
final class ProductController {
    #[Route(path: '/{id}', name: 'product_show', methods: [
        Request::METHOD_GET,
        \Symfony\Component\HttpFoundation\Request::METHOD_POST,
        'HEAD',
        Request::METHOD_GET,
        Other::NOT_A_METHOD,
    ])]
    public function show(): void {}
}`)
	routes := parsePHPRoutes(
		"/project/ProductController.php",
		phpparser.ParseBytes(source).Tree.Root,
		source,
	)
	if assert.Len(t, routes, 1) {
		assert.Equal(t, "product_show", routes[0].Name)
		assert.Equal(t, "/products/{id}", routes[0].Path)
		assert.Equal(t, []string{"GET", "POST", "HEAD"}, routes[0].Methods)
		assert.Equal(t, []string{"id"}, routes[0].Parameters())
	}
}

func TestExtractRoutesStorefrontController(t *testing.T) {
	// Extract routes from the test file with base path
	filePath := "testdata/wishlist.php"
	node, content := parsePHPFile(filePath)

	// Run the actual test
	routes := parsePHPRoutes(filePath, node, content)

	// Verify we found routes (should find at least one)
	assert.NotEmpty(t, routes)

	// Find the route we're interested in (frontend.wishlist.page)
	var wishlistPageRoute *Route
	for _, route := range routes {
		if route.Name == "frontend.wishlist.page" {
			wishlistPageRoute = &route
			break
		}
	}

	// Verify we found the route
	assert.NotNil(t, wishlistPageRoute)

	// Verify route data
	expectedRouteMethod := Route{
		Name:       "frontend.wishlist.page",
		Path:       "/wishlist",
		FilePath:   filePath,
		Line:       55, // Line number of the Route attribute in the wishlist.php file
		Controller: "Shopware\\Storefront\\Controller\\WishlistController::index",
		Methods:    []string{"GET"},
	}

	assert.Equal(t, expectedRouteMethod, *wishlistPageRoute)
}

func TestExtractRoutingConfiguratorRoutes(t *testing.T) {
	source := []byte(`<?php
namespace App\Routing;

use App\Controller\MyController;
use Symfony\Component\Routing\Loader\Configurator\RoutingConfigurator;

return static function (RoutingConfigurator $routes): void {
    $route = $routes->add('_profiler_home', '/');
    $route->controller('web_profiler.controller.profiler::homeAction');

    $routes->add('app_class_route', '/class')
        ->controller(MyController::class);

    $routes->add('app_array_route', '/array')
        ->controller([\App\Controller\MyController::class, 'detail']);

    $group = $routes->namePrefix('admin_')->prefix('/admin/');
    $group->add('app_default_route', '/defaults/{id}')
        ->defaults(['_controller' => [MyController::class, 'detail']])
        ->methods([Request::METHOD_GET, 'HEAD', Request::METHOD_GET]);

    $group->namePrefix('api_')->prefix('/api')->add('dashboard', 'dashboard');
};`)

	routes := parsePHPRoutes(
		"/project/config/routes.php",
		phpparser.ParseBytes(source).Tree.Root,
		source,
	)
	require.Len(t, routes, 5)
	assert.Equal(t, Route{
		Name:       "_profiler_home",
		Path:       "/",
		Controller: "web_profiler.controller.profiler::homeAction",
		FilePath:   "/project/config/routes.php",
		Line:       8,
	}, routes[0])
	assert.Equal(t, "App\\Controller\\MyController", routes[1].Controller)
	assert.Equal(t, "App\\Controller\\MyController::detail", routes[2].Controller)
	assert.Equal(t, Route{
		Name:       "admin_app_default_route",
		Path:       "/admin/defaults/{id}",
		Controller: "App\\Controller\\MyController::detail",
		Methods:    []string{"GET", "HEAD"},
		FilePath:   "/project/config/routes.php",
		Line:       18,
	}, routes[3])
	assert.Equal(t, "admin_api_dashboard", routes[4].Name)
	assert.Equal(t, "/admin/api/dashboard", routes[4].Path)
}

func TestRoutingConfiguratorRoutesRequireResolvedTypedRoot(t *testing.T) {
	source := []byte(`<?php
use Symfony\Component\Routing\Loader\Configurator\RoutingConfigurator as Routes;

$collection->add('outside', '/outside');

return static function (Routes $routes): void {
    $alias = $routes;
    $alias->add('inside', '/inside');
    $alias->add($dynamicName, '/dynamic');
};

return static function ($untyped): void {
    $untyped->add('untyped', '/untyped');
};`)

	routes := parsePHPRoutes(
		"/project/config/routes.php",
		phpparser.ParseBytes(source).Tree.Root,
		source,
	)
	require.Len(t, routes, 1)
	assert.Equal(t, "inside", routes[0].Name)
	assert.Equal(t, "/inside", routes[0].Path)
	assert.Equal(t, 8, routes[0].Line)
}

func parsePHPFile(filePath string) (*phpsyntax.Node, []byte) {
	content, err := os.ReadFile(filePath)
	if err != nil {
		panic(err)
	}

	return phpparser.ParseBytes(content).Tree.Root, content
}
