use swlsp_indexer::{Indexer, ParsedFile};
use swlsp_php_semantic::{Symbol, SymbolKind};
use swlsp_php_types::Type;

use super::*;
use crate::route_constant_names::ClassConstantLookup;

pub(crate) struct StubConstantLookup(pub Vec<(&'static str, Vec<Symbol>)>);

impl ClassConstantLookup for StubConstantLookup {
    fn constants(&self, class_name: &str) -> Vec<Symbol> {
        self.0
            .iter()
            .find(|(name, _)| *name == class_name)
            .map(|(_, symbols)| symbols.clone())
            .unwrap_or_default()
    }
}

pub(crate) fn literal_constant(name: &str, value: &str) -> Symbol {
    Symbol {
        kind: SymbolKind::ClassConstant,
        name: name.into(),
        ty: Type::literal_string(value),
        ..Symbol::default()
    }
}

fn index(files: &[(&str, &str)]) -> (tempfile::TempDir, RouteIndexer) {
    let dir = tempfile::tempdir().unwrap();
    let routes = RouteIndexer::new(dir.path(), None).unwrap();
    for (path, source) in files {
        routes
            .index(&ParsedFile::new(*path, source.as_bytes().to_vec()))
            .unwrap();
    }
    (dir, routes)
}

fn require_route_named(routes: &[Route], name: &str) {
    assert!(
        routes.iter().any(|route| route.name == name),
        "route {name:?} not found in {routes:#?}"
    );
}

#[test]
fn finds_absolute_and_partial_request_urls() {
    let (_dir, routes) = index(&[(
        "/project/config/routes.yaml",
        "product.show:\n    path: /edit/{id}\ncar.show:\n    path: /car/{edit}/foobar\nstatic.show:\n    path: /class-like-route\n",
    )]);
    for (search, name) in [
        (
            "https://shop.example/edit/foo.bar?preview=1#details",
            "product.show",
        ),
        ("https://shop.example/class-like-route", "static.show"),
        ("ar/12/foo", "car.show"),
        ("/edit/{id}", "product.show"),
    ] {
        require_route_named(&routes.find_routes_by_path(search).unwrap(), name);
    }
    for route in routes.find_routes_by_path("/edit/foo/bar").unwrap() {
        assert_ne!(route.name, "product.show");
    }
}

#[test]
fn ranks_exact_path_matches_before_partial_matches() {
    let (_dir, routes) = index(&[(
        "/project/config/routes.yaml",
        "partial:\n    path: /store-api/sitemap/{filePath}\nexact:\n    path: /sitemap/{filePath}\n",
    )]);
    let found = routes.find_routes_by_path("/sitemap/shop.xml.gz").unwrap();
    assert_eq!(found.len(), 2);
    assert_eq!(found[0].name, "exact");
    assert_eq!(found[1].name, "partial");
}

#[test]
fn sort_routes_prefers_source_definitions_over_generated_catalogs() {
    let mut routes = vec![
        Route {
            name: "generated".into(),
            path: "/sitemap/{filePath}".into(),
            file_path: "/project/var/cache/dev/url_generating_routes.php".into(),
            ..Route::default()
        },
        Route {
            name: "source".into(),
            path: "/sitemap/{filePath}".into(),
            file_path: "/project/src/SitemapController.php".into(),
            ..Route::default()
        },
    ];
    sort_routes(&mut routes);
    assert_eq!(routes[0].name, "source");
    assert_eq!(routes[1].name, "generated");
}

#[test]
fn indexes_routing_configurator_routes() {
    let (_dir, routes) = index(&[(
        "/project/config/routes.php",
        r"<?php
use Symfony\Component\Routing\Loader\Configurator\RoutingConfigurator;

return static function (RoutingConfigurator $routes): void {
    $routes->namePrefix('api_')->prefix('/api')
        ->add('product.show', '/products/{id}')
        ->controller([\App\Controller\ProductController::class, 'show'])
        ->methods(['GET', 'HEAD']);
};",
    )]);
    let found = routes.get_route("api_product.show").unwrap();
    assert_eq!(found.len(), 1);
    assert_eq!(found[0].path, "/api/products/{id}");
    assert_eq!(
        found[0].controller,
        r"App\Controller\ProductController::show"
    );
    assert_eq!(found[0].methods, vec!["GET", "HEAD"]);
    assert_eq!(found[0].parameters(), vec!["id"]);
}

#[test]
fn keeps_unresolved_routes_out_of_get_routes() {
    let (_dir, routes) = index(&[(
        "/project/src/Controller/ProductController.php",
        r"<?php
namespace App\Controller;
use App\Seo\ProductPageSeoUrlRoute;
class ProductController
{
    #[Route(path: '/detail/{productId}', name: ProductPageSeoUrlRoute::ROUTE_NAME)]
    public function detail(): void {}

    #[Route(path: '/address', name: 'frontend.account.address.page')]
    public function address(): void {}
}",
    )]);
    let all = routes.get_routes().unwrap();
    assert_eq!(all.len(), 1);
    assert_eq!(all[0].name, "frontend.account.address.page");

    let lookup = StubConstantLookup(vec![(
        r"App\Seo\ProductPageSeoUrlRoute",
        vec![literal_constant("ROUTE_NAME", "frontend.detail.page")],
    )]);
    let resolved = routes.resolved_routes(Some(&lookup)).unwrap();
    require_route_named(&resolved, "frontend.detail.page");
    require_route_named(&resolved, "frontend.account.address.page");

    let unresolved = routes.resolved_routes(None).unwrap();
    assert_eq!(unresolved.len(), 1);
    assert_eq!(unresolved[0].name, "frontend.account.address.page");
}

#[test]
fn normalize_route_search_path_cases() {
    assert_eq!(normalize_route_search_path(" https://shop.example "), "/");
    assert_eq!(normalize_route_search_path("//cdn.example/a/b?x"), "/a/b");
    assert_eq!(normalize_route_search_path("/edit/{id}?x#y"), "/edit/{id}");
    assert_eq!(normalize_route_search_path("foo:bar"), "foo:bar");
    assert_eq!(normalize_route_search_path("/a b"), "/a%20b");
}
