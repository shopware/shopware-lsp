use std::collections::HashSet;

use swlsp_indexer::{Indexer, ParsedFile};
use swlsp_parser_xml::query as xmlquery;
use swlsp_parser_xml::syntax as xmlsyntax;
use swlsp_parser_yaml::query as yamlquery;
use swlsp_parser_yaml::syntax as yamlsyntax;
use swlsp_php::PhpIndex;

use super::*;
use crate::twig_controller::parse_twig_controller_reference;

#[test]
fn parse_controller_reference_cases() {
    let cases = [
        (
            r"App\Controller\ProductController::show",
            Some((r"App\Controller\ProductController", "show")),
        ),
        (
            "app.product_controller:show",
            Some(("app.product_controller", "show")),
        ),
        (
            r"App\Controller\ProductController",
            Some((r"App\Controller\ProductController", "__invoke")),
        ),
        ("Bundle:Controller:action", None),
    ];
    for (value, expected) in cases {
        let reference = parse_controller_reference(value);
        assert_eq!(
            reference
                .as_ref()
                .map(|reference| (reference.target.as_str(), reference.method.as_str())),
            expected,
            "{value}"
        );
    }
}

#[test]
fn yaml_and_xml_controller_references() {
    let yaml = swlsp_parser_yaml::parse(
        "product.show:\n  path: /product\n  controller: App\\Controller\\ProductController::show\nproduct.legacy:\n  defaults:\n    _controller: app.product_controller:legacy\n",
    );
    let references: Vec<ControllerReference> =
        yamlquery::nodes(&yaml.root(), &[yamlsyntax::YAML_SCALAR])
            .iter()
            .filter_map(|scalar| yaml_controller_reference(scalar).map(|(reference, _)| reference))
            .collect();
    assert_eq!(references.len(), 2);

    let xml = swlsp_parser_xml::parse(
        r#"<routes>
<route id="product.show" controller="App\Controller\ProductController::show"/>
<route id="product.legacy"><default key="_controller">app.product_controller:legacy</default></route>
</routes>"#,
    );
    let values: HashSet<String> = xmlquery::nodes(
        &xml.root(),
        &[xmlsyntax::XML_ATTRIBUTE, xmlsyntax::XML_TEXT],
    )
    .iter()
    .filter_map(|node| xml_controller_reference(node).map(|(reference, _)| reference.value))
    .collect();
    assert!(values.contains(r"App\Controller\ProductController::show"));
    assert!(values.contains("app.product_controller:legacy"));
}

#[test]
fn resolve_controller_reference_for_class_and_service() {
    let php_dir = tempfile::tempdir().unwrap();
    let php = PhpIndex::open(php_dir.path(), None).unwrap();
    php.index(&ParsedFile::new(
        "/project/ProductController.php",
        br"<?php
namespace App\Controller;
class ProductController {
    public function show(): void {}
    public function __invoke(): void {}
    private function hidden(): void {}
}"
        .to_vec(),
    ))
    .unwrap();
    let root = tempfile::tempdir().unwrap();
    let cache = tempfile::tempdir().unwrap();
    let services = ServiceIndex::new(root.path().to_str().unwrap(), cache.path(), None).unwrap();
    services
        .index(&ParsedFile::new(
            "/project/services.yaml",
            b"services:\n  app.product_controller:\n    class: App\\Controller\\ProductController\n".to_vec(),
        ))
        .unwrap();
    for value in [
        r"App\Controller\ProductController::show",
        "app.product_controller:show",
        r"App\Controller\ProductController",
    ] {
        let reference = parse_controller_reference(value).unwrap();
        let resolution =
            resolve_controller_reference(&reference, Some(&services), Some(&php)).unwrap();
        assert!(resolution.target_exists, "{value}");
        assert!(resolution.class_found, "{value}");
        assert!(resolution.method_found, "{value}");
    }
    let reference =
        parse_controller_reference(r"App\Controller\ProductController::hidden").unwrap();
    let resolution = resolve_controller_reference(&reference, Some(&services), Some(&php)).unwrap();
    assert!(resolution.class_found);
    assert!(resolution.method_declared);
    assert!(!resolution.method_found);
    services.close().unwrap();
    php.close().unwrap();
}

#[test]
fn resolve_legacy_twig_controller_reference() {
    let php_dir = tempfile::tempdir().unwrap();
    let php = PhpIndex::open(php_dir.path(), None).unwrap();
    php.index(&ParsedFile::new(
        "/project/NavController.php",
        br"<?php
namespace Acme\DemoBundle\Controller\Admin;
class NavController {
    public function showAction(): void {}
}"
        .to_vec(),
    ))
    .unwrap();
    let reference = parse_twig_controller_reference("DemoBundle:Admin/Nav:show").unwrap();
    let resolution = resolve_controller_reference(&reference, None, Some(&php)).unwrap();
    assert!(resolution.class_found);
    assert!(resolution.method_found);
    assert_eq!(
        resolution.class.as_ref().unwrap().fully_qualified.as_str(),
        r"Acme\DemoBundle\Controller\Admin\NavController"
    );
    assert_eq!(
        resolution.method.as_ref().unwrap().name.as_str(),
        "showAction"
    );
    php.close().unwrap();
}
