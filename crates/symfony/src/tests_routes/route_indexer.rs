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

#[test]
fn scanner_prepared_indexing_matches_direct_indexing() {
    let root = tempfile::tempdir().unwrap();
    let cache = tempfile::tempdir().unwrap();
    let files = [
        (
            "config/routes.yaml",
            "product.show:\n    path: /product/{id}\n    controller: App\\Controller\\ProductController::show\ncontrollers:\n    resource: ../src/Controller/\n    type: attribute\n",
        ),
        (
            "config/routes.xml",
            "<routes>\n    <route id=\"blog.list\" path=\"/blog\" controller=\"App\\Controller\\BlogController::list\"/>\n    <import resource=\"../src/Admin/\" type=\"attribute\"/>\n</routes>\n",
        ),
        (
            "src/Controller/HomeController.php",
            "<?php\nnamespace App\\Controller;\nuse Symfony\\Component\\Routing\\Attribute\\Route;\nfinal class HomeController\n{\n    #[Route('/home', name: 'home')]\n    public function index() {}\n}\n",
        ),
    ];
    let mut paths = Vec::new();
    for (relative, source) in files {
        let path = root.path().join(relative);
        std::fs::create_dir_all(path.parent().unwrap()).unwrap();
        std::fs::write(&path, source).unwrap();
        paths.push((path.to_str().unwrap().to_owned(), source));
    }

    let store = swlsp_indexer::Store::open(cache.path().join("indexes.db")).unwrap();
    let scanned =
        std::sync::Arc::new(RouteIndexer::new(cache.path().join("scanned"), Some(&store)).unwrap());
    let mut scanner = swlsp_indexer::FileScanner::new(
        root.path().to_str().unwrap(),
        cache.path().join("scanner.db"),
        Some(store.clone()),
    )
    .unwrap();
    scanner.add_indexer(scanned.clone());
    scanner
        .index_all(&swlsp_indexer::CancelToken::new())
        .unwrap();
    scanner.close().unwrap();

    let direct = RouteIndexer::new(cache.path().join("direct"), None).unwrap();
    for (path, source) in &paths {
        direct
            .index(&ParsedFile::new(path.as_str(), source.as_bytes().to_vec()))
            .unwrap();
    }

    let mut scanned_routes = scanned.get_routes().unwrap();
    let mut direct_routes = direct.get_routes().unwrap();
    sort_routes(&mut scanned_routes);
    sort_routes(&mut direct_routes);
    assert_eq!(scanned_routes.len(), 3, "{scanned_routes:#?}");
    assert_eq!(scanned_routes, direct_routes);

    let resource_key = |import: &RouteResourceImport| {
        (
            import.file_path.clone(),
            u32::from(import.range.start()),
            import.path.clone(),
        )
    };
    let mut scanned_imports = scanned.get_route_resource_imports().unwrap();
    let mut direct_imports = direct.get_route_resource_imports().unwrap();
    scanned_imports.sort_by_key(resource_key);
    direct_imports.sort_by_key(resource_key);
    assert_eq!(scanned_imports.len(), 2, "{scanned_imports:#?}");
    assert_eq!(scanned_imports, direct_imports);

    direct.close().unwrap();
    scanned.close().unwrap();
    store.close().unwrap();
}
