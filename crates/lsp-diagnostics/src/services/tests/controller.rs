use serde_json::json;
use swlsp_lsp_protocol::{DiagnosticSeverity, DiagnosticTag};

use super::*;
use crate::services::ControllerAnalyzer;
use crate::test_support::check_problems;

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
    let (_, problems) = check_problems(
        &fixture.analyzer,
        "file:///project/routes.yaml",
        r"product.show:
  path: /products/{id}
  controller: App\Controller\ProductController::show
product.edit:
  path: /products/{id}/edit
  controller: App\Controller\ProductController::{|symfony.controller.method.missing:edit|}
product.service:
  path: /products/{id}
  controller: app.product_controller:show
product.unknown:
  path: /unknown
  controller: {|symfony.controller.target.missing:App\Controller\MissingController|}::show
product.legacy:
  path: /legacy
  controller: Bundle:Controller:action
",
    );
    let method = &problems[0];
    assert!(method.message.contains("ProductController::edit"));
    let data = payload(method);
    assert_eq!(data["methodName"], "edit");
    assert_eq!(data["routeParameters"], json!(["id"]));
    assert!(!data.contains_key("classURI"));

    assert!(problems[1].message.contains("Controller target"));
}

#[test]
fn controller_diagnostics_for_xml_controller() {
    let fixture = fixture();
    check_problems(
        &fixture.analyzer,
        "file:///project/routes.xml",
        r#"<routes>
  <route id="product.edit" path="/products/{id}" controller="App\Controller\ProductController::{|symfony.controller.method.missing:edit|}"/>
</routes>"#,
    );
}

#[test]
fn controller_diagnostics_for_twig_controller() {
    let fixture = fixture();
    check_problems(
        &fixture.analyzer,
        "file:///project/navigation.html.twig",
        r"{{ controller('App\\Controller\\ProductController::{|symfony.controller.method.missing:edit|}') }}
{{ render(controller('{|symfony.controller.target.missing:\\App\\Controller\\MissingController::show|}')) }}
",
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
  controller: {|symfony.controller.deprecated:App\Controller\ProductController::legacy|}
",
        ),
        (
            "file:///project/routes.xml",
            r#"<routes>
<route id="product.legacy" path="/legacy" controller="{|symfony.controller.deprecated:App\Controller\LegacyController::show|}"/>
</routes>"#,
        ),
        (
            "file:///project/view.twig",
            "{{ controller('{|symfony.controller.deprecated:app.legacy_controller:show|}') }}",
        ),
    ];
    for (uri, source) in tests {
        let (_, problems) = check_problems(&fixture.analyzer, uri, source);
        assert_eq!(problems[0].severity, Some(DiagnosticSeverity::HINT));
        assert_eq!(problems[0].tags, vec![DiagnosticTag::DEPRECATED]);
    }
}
