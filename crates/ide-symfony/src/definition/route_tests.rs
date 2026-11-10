use std::path::Path;
use std::sync::Arc;

use swlsp_symfony::RouteIndexer;
use swlsp_uriutil::file_uri;

use super::*;
use crate::definition::php_common::test_support::*;

fn route_index(dir: &Path, path: &str, source: &str) -> Arc<RouteIndexer> {
    let routes = Arc::new(RouteIndexer::new(dir, None).unwrap());
    index(routes.as_ref(), path, source);
    routes
}

#[test]
fn route_definition_navigates_routing_resources() {
    let root = tempfile::tempdir().unwrap();
    let config_dir = root.path().join("config");
    let route_file = config_dir.join("catalog.yaml");
    let controller_file = root.path().join("src/Controller/CatalogController.php");
    write(&route_file, "catalog: { path: /catalog }\n");
    write(&controller_file, "<?php final class CatalogController {}");

    for (name, path, source, marker, expected) in [
        (
            "YAML route file",
            config_dir.join("routes.yaml"),
            "catalog:\n  resource: catalog.yaml\n",
            "catalog.yaml",
            &route_file,
        ),
        (
            "nested YAML attribute directory",
            config_dir.join("routes.yaml"),
            "controllers:\n  resource:\n    path: ../src/Controller/\n    namespace: App\\Controller\n  type: attribute\n",
            "../src",
            &controller_file,
        ),
        (
            "XML attribute directory",
            config_dir.join("routes.xml"),
            r#"<routes><import resource="../src/Controller/" type="attribute"/></routes>"#,
            "../src",
            &controller_file,
        ),
        (
            "PHP configurator directory",
            config_dir.join("routes.php"),
            r"<?php
use Symfony\Component\Routing\Loader\Configurator\RoutingConfigurator;
return static function (RoutingConfigurator $routes): void {
    $routes->import('../src/Controller/', 'attribute');
};",
            "../src",
            &controller_file,
        ),
    ] {
        let document = document(&file_uri(&p(&path)), source);
        let offset = index_of(source, marker) + 2;
        let locations = definitions(
            &RouteDefinitionProvider::new(None, None),
            &request_at(&document, offset),
        );
        assert_eq!(locations.len(), 1, "{name}");
        assert_eq!(locations[0].uri, file_uri(&p(expected)), "{name}");
    }
}

#[test]
fn route_definition_navigates_legacy_bundle_resource() {
    let root = tempfile::tempdir().unwrap();
    let php = php_index(&root.path().join(".cache"));
    let interface_path = root.path().join("BundleInterface.php");
    let interface_source = r"<?php
namespace Symfony\Component\HttpKernel\Bundle;
interface BundleInterface {}
";
    let bundle_root = root.path().join("vendor/acme/foo-bundle");
    let bundle_path = bundle_root.join("FooBundle.php");
    let bundle_source = r"<?php
namespace Acme\Foo;
final class FooBundle implements \Symfony\Component\HttpKernel\Bundle\BundleInterface {}
";
    for (path, source) in [
        (&interface_path, interface_source),
        (&bundle_path, bundle_source),
    ] {
        write(path, source);
        index(&php, &p(path), source);
    }
    let target_path = bundle_root.join("Resources/config/routes.xml");
    write(&target_path, "<routes/>");
    let source = r#"<routes><import resource="@FooBundle/Resources/config/routes.xml"/></routes>"#;
    let document = document(
        &file_uri(&p(&root.path().join("config/routes.xml"))),
        source,
    );
    let offset = index_of(source, "@FooBundle") + 3;
    let locations = definitions(
        &RouteDefinitionProvider::new(None, Some(php)),
        &request_at(&document, offset),
    );
    assert_eq!(locations.len(), 1);
    assert_eq!(locations[0].uri, file_uri(&p(&target_path)));
}

fn assert_single_route(
    routes: Arc<RouteIndexer>,
    uri: &str,
    source: &str,
    marker: &str,
    delta: usize,
) {
    let document = document(uri, source);
    let offset = index_of(source, marker) + delta;
    let locations = definitions(
        &RouteDefinitionProvider::new(Some(routes), None),
        &request_at(&document, offset),
    );
    assert_eq!(locations.len(), 1, "{source}");
    assert_eq!(locations[0].uri, file_uri("/project/config/routes.yaml"));
    assert_eq!(locations[0].range.start.line, 0);
}

fn catalog_routes() -> (tempfile::TempDir, Arc<RouteIndexer>) {
    let dir = tempfile::tempdir().unwrap();
    let routes = route_index(
        dir.path(),
        "/project/config/routes.yaml",
        "catalog.show:\n    path: /catalog/{id}\n",
    );
    (dir, routes)
}

#[test]
fn route_definition_matches_concrete_twig_html_url() {
    let (_dir, routes) = catalog_routes();
    assert_single_route(
        routes,
        "file:///project/view.twig",
        r#"<a href="/catalog/42?preview=1">Catalog</a>"#,
        "/catalog/42",
        3,
    );
}

#[test]
fn route_definition_matches_absolute_twig_html_url() {
    let (_dir, routes) = catalog_routes();
    assert_single_route(
        routes,
        "file:///project/view.twig",
        r#"<a href="https://shop.example/catalog/foo.bar?preview=1#details">Catalog</a>"#,
        "/catalog/foo.bar",
        3,
    );
}

#[test]
fn route_definition_matches_javascript_request_url() {
    let (_dir, routes) = catalog_routes();
    assert_single_route(
        routes,
        "file:///project/request.js",
        "fetch('https://shop.example/catalog/foo.bar?preview=1#details')",
        "/catalog/foo.bar",
        3,
    );
}

#[test]
fn route_definition_from_twig_route_comparison() {
    let (_dir, routes) = catalog_routes();
    assert_single_route(
        routes,
        "file:///project/view.twig",
        "{% if app.request.attributes.get('_route') == 'catalog.show' %}{% endif %}",
        "catalog.show",
        3,
    );
}

#[test]
fn php_doc_route_assistant_tag_definition() {
    let cache = tempfile::tempdir().unwrap();
    let route_path = p(&cache.path().join("routes.yaml"));
    let routes = route_index(
        &cache.path().join("routes"),
        &route_path,
        "catalog.show:\n  path: /catalog/{id}\n",
    );
    let php = php_index(&cache.path().join("php"));
    index(
        &php,
        &p(&cache.path().join("RouteAware.php")),
        r"<?php
interface RouteAware
{
    /** @param string $route #Route */
    public function open(string $route): void;
}
class RouteConsumer implements RouteAware
{
    public function open(string $route): void {}
}
",
    );
    let source = "<?php
$consumer = new RouteConsumer();
$consumer->open('catalog.show');
";
    let document = document("file:///project/src/Usage.php", source);
    let offset = index_of(source, "catalog.show") + 3;
    let locations = definitions(
        &RouteDefinitionProvider::new(Some(routes), Some(php)),
        &request_at(&document, offset),
    );
    assert_eq!(locations.len(), 1);
    assert_eq!(locations[0].uri, file_uri(&route_path));
}

#[test]
fn route_parameter_definition_supports_twig_identifier_hash_keys() {
    let dir = tempfile::tempdir().unwrap();
    let routes = route_index(
        dir.path(),
        "/project/config/routes.yaml",
        "product.show:\n    path: /products/{slug}\n",
    );
    for (source, marker, found) in [
        (
            "{{ path('product.show', {'slug': product.slug}) }}",
            "slug':",
            true,
        ),
        (
            "{{ path('product.show', {slug: product.slug}) }}",
            "slug:",
            true,
        ),
        ("{{ path('product.show', {slug}) }}", "slug}", true),
        (
            "{{ path('product.show', {missing: product.slug}) }}",
            "missing",
            false,
        ),
        (
            "{{ path('product.show', {slug: product.value}) }}",
            "value",
            false,
        ),
    ] {
        let document = document("file:///project/view.twig", source);
        let offset = index_of(source, marker) + 1;
        let locations = definitions(
            &RouteDefinitionProvider::new(Some(routes.clone()), None),
            &request_at(&document, offset),
        );
        if !found {
            assert!(locations.is_empty(), "{source}");
            continue;
        }
        assert_eq!(locations.len(), 1, "{source}");
        assert_eq!(locations[0].uri, file_uri("/project/config/routes.yaml"));
        assert_eq!(locations[0].range.start.line, 0);
    }
}

#[test]
fn route_parameter_definition_supports_php_array_keys() {
    let dir = tempfile::tempdir().unwrap();
    let routes = route_index(
        dir.path(),
        "/project/config/routes.yaml",
        "product.show:\n    path: /products/{slug}\n",
    );
    let source = "<?php $this->redirectToRoute('product.show', ['slug' => $slug]);";
    let document = document("file:///project/Controller.php", source);
    let provider = RouteDefinitionProvider::new(Some(routes), None);
    let offset = index_of(source, "'slug'") + 2;
    let locations = definitions(&provider, &request_at(&document, offset));
    assert_eq!(locations.len(), 1);
    assert_eq!(locations[0].uri, file_uri("/project/config/routes.yaml"));
    assert_eq!(locations[0].range.start.line, 0);

    let value_offset = last_index_of(source, "$slug") + 2;
    assert!(definitions(&provider, &request_at(&document, value_offset)).is_empty());
}

#[test]
fn route_definition_navigates_to_routing_configurator_route() {
    let dir = tempfile::tempdir().unwrap();
    let route_path = "/project/config/routes.php";
    let routes = route_index(
        dir.path(),
        route_path,
        r"<?php
use Symfony\Component\Routing\Loader\Configurator\RoutingConfigurator;

return static function (RoutingConfigurator $routes): void {
    $routes->namePrefix('api_')->prefix('/api')
        ->add('product.show', '/products/{id}');
};",
    );
    let source = "{{ path('api_product.show', {id: product.id}) }}";
    let document = document("file:///project/view.twig", source);
    let offset = index_of(source, "api_product.show") + 3;
    let locations = definitions(
        &RouteDefinitionProvider::new(Some(routes), None),
        &request_at(&document, offset),
    );
    assert_eq!(locations.len(), 1);
    assert_eq!(locations[0].uri, file_uri(route_path));
    assert_eq!(locations[0].range.start.line, 5);
}

#[test]
fn route_definition_navigates_php_attribute_path_prefix() {
    let dir = tempfile::tempdir().unwrap();
    let routes = route_index(
        dir.path(),
        "/project/config/routes.yaml",
        "catalog.root:
    path: /catalog/products
catalog.show:
    path: /catalog/products/{id}
catalog.other:
    path: /catalog/reviews
catalog.current:
    path: /catalog/products/detail
",
    );
    for source in [
        "<?php #[Route('/catalog/products/detail')] function show() {}",
        "<?php #[Route(name: 'current', path: '/catalog/products/detail')] function show() {}",
    ] {
        let document = document("file:///project/src/Controller.php", source);
        let offset = index_of(source, "/catalog/products/detail") + "/catalog/prod".len();
        let locations = definitions(
            &RouteDefinitionProvider::new(Some(routes.clone()), None),
            &request_at(&document, offset),
        );
        assert_eq!(locations.len(), 2, "{source}");
        for location in &locations {
            assert_eq!(location.uri, file_uri("/project/config/routes.yaml"));
        }
        assert_eq!(locations[0].range.start.line, 0);
        assert_eq!(locations[1].range.start.line, 2);
    }
}

#[test]
fn route_definition_ignores_final_and_non_path_attribute_strings() {
    let dir = tempfile::tempdir().unwrap();
    let routes = route_index(
        dir.path(),
        "/project/config/routes.yaml",
        "catalog.root:\n    path: /catalog/products\n",
    );
    for source in [
        "<?php #[Route('/catalog/products')] function show() {}",
        "<?php #[Route(name: '/catalog/products')] function show() {}",
        "<?php #[Cache('/catalog/products/detail')] function show() {}",
    ] {
        let document = document("file:///project/src/Controller.php", source);
        let offset = last_index_of(source, "products") + 2;
        assert!(
            definitions(
                &RouteDefinitionProvider::new(Some(routes.clone()), None),
                &request_at(&document, offset),
            )
            .is_empty(),
            "{source}"
        );
    }
}
