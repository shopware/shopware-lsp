use std::collections::HashMap;

use serde_json::json;
use swlsp_lsp_protocol::{DiagnosticSeverity, DiagnosticTag};

use super::*;
use crate::services::ControllerAnalyzer;

const MISSING_TARGET: &str = "symfony.controller.target.missing";
const MISSING_METHOD: &str = "symfony.controller.method.missing";
const DEPRECATED: &str = "symfony.controller.deprecated";

struct Fixture {
    _dir: tempfile::TempDir,
    analyzer: ControllerAnalyzer,
}

fn fixture() -> Fixture {
    let dir = tempfile::tempdir().unwrap();
    let class_path = dir.path().join("ProductController.php");
    let content = r"<?php
namespace App\Controller;
class ProductController
{
    public function show(int $id): void {}
    /** @deprecated Use show instead. */
    public function legacy(): void {}
}
/** @deprecated Use ProductController instead. */
class LegacyController
{
    public function show(): void {}
}
";
    std::fs::write(&class_path, content).unwrap();
    let php = php_index(&dir.path().join("php"), &[(&p(&class_path), content)]);
    let services = service_index(
        &dir.path().join("services"),
        None,
        &[(
            "/project/services.yaml",
            r"services:
  app.product_controller:
    class: App\Controller\ProductController
  app.legacy_controller:
    class: App\Controller\LegacyController
",
        )],
    );
    Fixture {
        _dir: dir,
        analyzer: ControllerAnalyzer::new(Some(services), Some(php)),
    }
}

#[test]
fn controller_diagnostics_for_yaml_targets_and_methods() {
    let fixture = fixture();
    let document = document(
        "file:///project/routes.yaml",
        r"product.show:
  path: /products/{id}
  controller: App\Controller\ProductController::show
product.edit:
  path: /products/{id}/edit
  controller: App\Controller\ProductController::edit
product.service:
  path: /products/{id}
  controller: app.product_controller:show
product.unknown:
  path: /unknown
  controller: App\Controller\MissingController::show
product.legacy:
  path: /legacy
  controller: Bundle:Controller:action
",
    );
    let result = analyze(&fixture.analyzer, &document);
    assert_eq!(result.len(), 2);
    let by_code: HashMap<&str, &Problem> = result
        .iter()
        .map(|problem| (problem.id.as_str(), problem))
        .collect();
    let method = by_code[MISSING_METHOD];
    assert_eq!(range_text(&document, method.range), "edit");
    assert!(method.message.contains("ProductController::edit"));
    let data = payload(method);
    assert_eq!(data["methodName"], "edit");
    assert_eq!(data["routeParameters"], json!(["id"]));
    assert!(!data.contains_key("classURI"));

    let target = by_code[MISSING_TARGET];
    assert_eq!(
        range_text(&document, target.range),
        "App\\Controller\\MissingController"
    );
    assert!(target.message.contains("Controller target"));
}

#[test]
fn controller_diagnostics_for_xml_controller() {
    let fixture = fixture();
    let document = document(
        "file:///project/routes.xml",
        r#"<routes>
  <route id="product.edit" path="/products/{id}" controller="App\Controller\ProductController::edit"/>
</routes>"#,
    );
    let result = analyze(&fixture.analyzer, &document);
    assert_eq!(result.len(), 1);
    assert_eq!(result[0].id.as_str(), MISSING_METHOD);
    assert_eq!(range_text(&document, result[0].range), "edit");
}

#[test]
fn controller_diagnostics_for_twig_controller() {
    let fixture = fixture();
    let document = document(
        "file:///project/navigation.html.twig",
        r"{{ controller('App\\Controller\\ProductController::edit') }}
{{ render(controller('\\App\\Controller\\MissingController::show')) }}
",
    );
    let result = analyze(&fixture.analyzer, &document);
    assert_eq!(result.len(), 2);
    let by_code: HashMap<&str, &Problem> = result
        .iter()
        .map(|problem| (problem.id.as_str(), problem))
        .collect();
    assert_eq!(range_text(&document, by_code[MISSING_METHOD].range), "edit");
    assert_eq!(
        range_text(&document, by_code[MISSING_TARGET].range),
        r"\\App\\Controller\\MissingController::show"
    );
}

#[test]
fn controller_diagnostics_mark_deprecated_actions() {
    let fixture = fixture();
    let tests = [
        (
            "file:///project/routes.yaml",
            r"product.legacy:
  path: /legacy
  controller: App\Controller\ProductController::legacy
",
            r"App\Controller\ProductController::legacy",
        ),
        (
            "file:///project/routes.xml",
            r#"<routes>
<route id="product.legacy" path="/legacy" controller="App\Controller\LegacyController::show"/>
</routes>"#,
            r"App\Controller\LegacyController::show",
        ),
        (
            "file:///project/view.twig",
            "{{ controller('app.legacy_controller:show') }}",
            "app.legacy_controller:show",
        ),
    ];
    for (uri, source, value) in tests {
        let document = document(uri, source);
        let result = analyze(&fixture.analyzer, &document);
        assert_eq!(result.len(), 1, "{uri}");
        assert_eq!(result[0].id.as_str(), DEPRECATED);
        assert_eq!(result[0].severity, Some(DiagnosticSeverity::HINT));
        assert_eq!(result[0].tags, vec![DiagnosticTag::DEPRECATED]);
        assert_eq!(range_text(&document, result[0].range), value);
    }
}
