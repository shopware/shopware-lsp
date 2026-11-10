use std::sync::Arc;

use swlsp_parser_twig::syntax::TWIG_LITERAL_STRING;
use swlsp_parser_yaml::query as yamlquery;
use swlsp_parser_yaml::syntax::YAML_SCALAR;
use swlsp_symfony::ServiceIndex;
use swlsp_uriutil::file_uri;

use super::*;
use crate::definition::php_common::test_support::*;

fn services(root: &std::path::Path) -> Arc<ServiceIndex> {
    let services = Arc::new(ServiceIndex::new(&p(root), root.join("services"), None).unwrap());
    index(
        services.as_ref(),
        "/project/services.yaml",
        r"services:
  app.product_controller:
    class: App\Controller\ProductController
",
    );
    services
}

fn product_controller(root: &std::path::Path) -> (PhpIndex, String) {
    let php = php_index(&root.join("php"));
    let class_path = root.join("src/ProductController.php");
    let content = r"<?php
namespace App\Controller;
class ProductController {
    public function show(): void {}
}";
    write(&class_path, content);
    index(&php, &p(&class_path), content);
    (php, p(&class_path))
}

#[test]
fn controller_definition_resolves_service_method() {
    let root = tempfile::tempdir().unwrap();
    let (php, class_path) = product_controller(root.path());
    let services = services(root.path());
    let source = "product.show:
  path: /products
  controller: app.product_controller:show
";
    let document = document("file:///project/routes.yaml", source);
    let node = yamlquery::nodes(&document.root().unwrap(), &[YAML_SCALAR])
        .into_iter()
        .find(|scalar| yamlquery::scalar_value(scalar) == "app.product_controller:show")
        .unwrap();
    let provider = ControllerDefinitionProvider::new(Some(services), Some(php));
    let locations = definitions(&provider, &request_with_node(&document, Some(node), 0));
    assert_eq!(locations.len(), 1);
    assert_eq!(locations[0].uri, file_uri(&class_path));
    assert_eq!(locations[0].range.start.line, 3);
}

#[test]
fn controller_definition_resolves_escaped_twig_controller() {
    let root = tempfile::tempdir().unwrap();
    let php = php_index(&root.path().join("php"));
    let class_path = root.path().join("src/NavController.php");
    let content = r"<?php
namespace App\Controller;
class NavController {
    public function menuAction(): void {}
}";
    write(&class_path, content);
    index(&php, &p(&class_path), content);

    let source = r"{{ render(controller('\\App\\Controller\\NavController::menuAction')) }}";
    let document = document("file:///project/navigation.html.twig", source);
    let literals: Vec<_> = document
        .root()
        .unwrap()
        .descendants()
        .filter(|node| node.kind() == TWIG_LITERAL_STRING)
        .collect();
    assert_eq!(literals.len(), 1);
    let locations = definitions(
        &ControllerDefinitionProvider::new(None, Some(php)),
        &request_with_node(&document, Some(literals[0].clone()), 0),
    );
    assert_eq!(locations.len(), 1);
    assert_eq!(locations[0].uri, file_uri(&p(&class_path)));
    assert_eq!(locations[0].range.start.line, 3);
}

#[test]
fn controller_definition_resolves_twig_see_service_method() {
    let root = tempfile::tempdir().unwrap();
    let (php, class_path) = product_controller(root.path());
    let services = services(root.path());
    let source = "{# @see app.product_controller:show #}";
    let document = document("file:///project/page.html.twig", source);
    let offset = index_of(source, "product_controller") + 2;
    let locations = definitions(
        &ControllerDefinitionProvider::new(Some(services), Some(php)),
        &request_at(&document, offset),
    );
    assert_eq!(locations.len(), 1);
    assert_eq!(locations[0].uri, file_uri(&class_path));
    assert_eq!(locations[0].range.start.line, 3);
}
