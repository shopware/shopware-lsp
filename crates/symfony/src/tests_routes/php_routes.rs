use super::*;

fn parse_fixture(file_path: &str) -> Vec<Route> {
    let source =
        std::fs::read_to_string(format!("{}/{file_path}", env!("CARGO_MANIFEST_DIR"))).unwrap();
    parse_php_routes(file_path, &source)
}

fn route(
    name: &str,
    path: &str,
    file_path: &str,
    line: u32,
    controller: &str,
    methods: &[&str],
) -> Route {
    Route {
        name: name.into(),
        path: path.into(),
        file_path: file_path.into(),
        line,
        controller: controller.into(),
        methods: methods.iter().map(|method| (*method).to_owned()).collect(),
        ..Route::default()
    }
}

#[test]
fn extract_routes_from_file() {
    let routes = parse_fixture("testdata/controller.php");
    assert_eq!(
        routes,
        vec![route(
            "frontend.account.address.create",
            "/account/address/create",
            "testdata/controller.php",
            14,
            r"App\Controller\Frontend\Account\AddressController::createAddress",
            &[],
        )]
    );
}

#[test]
fn extract_routes_with_base_path_from_file() {
    let routes = parse_fixture("testdata/controller_base.php");
    assert_eq!(
        routes,
        vec![route(
            "foo",
            "/api/foo",
            "testdata/controller_base.php",
            11,
            r"Shopware\Core\Api\ApiController::foo",
            &[]
        )]
    );
}

#[test]
fn extract_routes_with_class_path_and_methods() {
    let routes = parse_php_routes(
        "/project/ProductController.php",
        r"<?php
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
}",
    );
    assert_eq!(routes.len(), 1);
    assert_eq!(routes[0].name, "product_show");
    assert_eq!(routes[0].path, "/products/{id}");
    assert_eq!(routes[0].methods, vec!["GET", "POST", "HEAD"]);
    assert_eq!(routes[0].parameters(), vec!["id"]);
}

#[test]
fn extract_routes_storefront_controller() {
    let routes = parse_fixture("testdata/wishlist.php");
    let found = routes
        .iter()
        .find(|route| route.name == "frontend.wishlist.page")
        .unwrap();
    assert_eq!(
        *found,
        route(
            "frontend.wishlist.page",
            "/wishlist",
            "testdata/wishlist.php",
            55,
            r"Shopware\Storefront\Controller\WishlistController::index",
            &["GET"],
        )
    );
}

#[test]
fn extract_routing_configurator_routes() {
    let routes = parse_php_routes(
        "/project/config/routes.php",
        r"<?php
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
};",
    );
    assert_eq!(routes.len(), 5);
    assert_eq!(
        routes[0],
        route(
            "_profiler_home",
            "/",
            "/project/config/routes.php",
            8,
            "web_profiler.controller.profiler::homeAction",
            &[]
        )
    );
    assert_eq!(routes[1].controller, r"App\Controller\MyController");
    assert_eq!(routes[2].controller, r"App\Controller\MyController::detail");
    assert_eq!(
        routes[3],
        route(
            "admin_app_default_route",
            "/admin/defaults/{id}",
            "/project/config/routes.php",
            18,
            r"App\Controller\MyController::detail",
            &["GET", "HEAD"],
        )
    );
    assert_eq!(routes[4].name, "admin_api_dashboard");
    assert_eq!(routes[4].path, "/admin/api/dashboard");
}

#[test]
fn routing_configurator_routes_require_resolved_typed_root() {
    let routes = parse_php_routes(
        "/project/config/routes.php",
        r"<?php
use Symfony\Component\Routing\Loader\Configurator\RoutingConfigurator as Routes;

$collection->add('outside', '/outside');

return static function (Routes $routes): void {
    $alias = $routes;
    $alias->add('inside', '/inside');
    $alias->add($dynamicName, '/dynamic');
};

return static function ($untyped): void {
    $untyped->add('untyped', '/untyped');
};",
    );
    assert_eq!(routes.len(), 1);
    assert_eq!(routes[0].name, "inside");
    assert_eq!(routes[0].path, "/inside");
    assert_eq!(routes[0].line, 8);
}

#[test]
fn extract_route_named_by_class_constant() {
    let routes = parse_fixture("testdata/controller_constant_name.php");
    assert_eq!(routes.len(), 1);
    assert_eq!(routes[0].name, "");
    assert_eq!(
        routes[0].name_constant,
        r"App\Seo\ProductPageSeoUrlRoute::ROUTE_NAME"
    );
    assert_eq!(routes[0].path, "/detail/{productId}");
}

#[test]
fn extract_route_ignores_concatenated_constant_names() {
    let routes = parse_fixture("testdata/controller_concatenated_name.php");
    assert_eq!(routes.len(), 1);
    assert_eq!(routes[0].name, "");
    assert_eq!(routes[0].name_constant, "");
    assert_eq!(routes[0].path, "/detail/{productId}");
}

#[test]
fn extract_route_ignores_class_constant_for_controller_names() {
    for route in parse_fixture("testdata/controller.php") {
        assert_eq!(route.name_constant, "", "{}", route.name);
    }
}
