use swlsp_lsp_protocol::{DiagnosticSeverity, DiagnosticTag};
use swlsp_symfony::RouteIndexer;

use super::*;
use crate::services::RouteAnalyzer;
use crate::test_support::check_problems;

struct Fixture {
    _dir: tempfile::TempDir,
    analyzer: RouteAnalyzer,
}

fn fixture() -> Fixture {
    let dir = tempfile::tempdir().unwrap();
    let routes = RouteIndexer::new(dir.path().join("routes"), None).unwrap();
    routes
        .index(&ParsedFile::new(
            "/project/config/routes.yaml",
            br"product.show:
  path: /products/{id}
  controller: App\Controller\ProductController::show
product.legacy_method:
  path: /products/legacy-method
  controller: App\Controller\ProductController::legacy
product.legacy_class:
  path: /products/legacy-class
  controller: App\Controller\LegacyController::show
product.legacy_service:
  path: /products/legacy-service
  controller: app.legacy_controller:show
"
            .to_vec(),
        ))
        .unwrap();
    let class_path = dir.path().join("Controllers.php");
    let class_source = r"<?php
namespace App\Controller;
class ProductController {
    public function show(): void {}
    /** @deprecated Use show instead. */
    public function legacy(): void {}
}
/** @deprecated Use ProductController instead. */
class LegacyController {
    public function show(): void {}
}
interface RouteAware {
    /** @param string $route #Route */
    public function open(string $route): void;
}
class RouteConsumer implements RouteAware {
    public function open(string $route): void {}
}
";
    std::fs::write(&class_path, class_source).unwrap();
    let php = php_index(&dir.path().join("php"), &[(&p(&class_path), class_source)]);
    let services = service_index(
        &dir.path().join("services"),
        Some(&php),
        &[(
            "/project/config/services.yaml",
            "services:\n  app.legacy_controller:\n    class: App\\Controller\\LegacyController\n",
        )],
    );
    Fixture {
        _dir: dir,
        analyzer: RouteAnalyzer::new(Some(Arc::new(routes)), Some(services), Some(php)),
    }
}

#[test]
fn route_diagnostics_for_php_and_twig() {
    let fixture = fixture();
    let tests = [
        (
            "file:///project/Controller.php",
            "<?php $this->redirectToRoute('product.show');",
        ),
        (
            "file:///project/Controller.php",
            "<?php $this->generateUrl('{|symfony.route.missing:product.missing|}');",
        ),
        ("file:///project/view.twig", "{{ path('product.show') }}"),
        (
            "file:///project/view.twig",
            "{{ url('{|symfony.route.missing:product.missing|}') }}",
        ),
        (
            "file:///project/view.twig",
            "{% if app.request.attributes.get('_route') == 'product.show' %}{% endif %}",
        ),
        (
            "file:///project/view.twig",
            "{% if app.request.attributes.get('_route') in ['{|symfony.route.missing:product.missing|}'] %}{% endif %}",
        ),
        (
            "file:///project/view.twig",
            "{% if app.request.attributes.get('_route') starts with 'product.missing' %}{% endif %}",
        ),
        (
            "file:///project/view.twig",
            "{{ path(\"product.#{kind}\") }}",
        ),
        (
            "file:///project/view.twig",
            r"{{ path('App\\Controller\\ProductController::show') }}",
        ),
    ];
    for (uri, source) in tests {
        let (_, problems) = check_problems(&fixture.analyzer, uri, source);
        for problem in &problems {
            assert_eq!(problem.message, "Route 'product.missing' not found");
            assert_eq!(problem.source, "symfony");
        }
    }
}

#[test]
fn route_diagnostics_include_typo_suggestions_and_precise_range() {
    let fixture = fixture();
    let (_, problems) = check_problems(
        &fixture.analyzer,
        "file:///project/Controller.php",
        "<?php $this->generateUrl('{|symfony.route.missing:product.sho|}');",
    );
    assert!(suggestions(&problems[0]).contains(&"product.show".to_owned()));
}

#[test]
fn route_diagnostics_validate_inherited_phpdoc_assistant_references() {
    let fixture = fixture();
    let (_, problems) = check_problems(
        &fixture.analyzer,
        "file:///project/src/Usage.php",
        r"<?php
namespace App\Controller;
function open(RouteConsumer $consumer): void {
    $consumer->open('{|symfony.route.missing:product.sho|}');
}",
    );
    assert!(suggestions(&problems[0]).contains(&"product.show".to_owned()));
}

#[test]
fn twig_route_comparison_diagnostics_include_typo_suggestion() {
    let fixture = fixture();
    let (_, problems) = check_problems(
        &fixture.analyzer,
        "file:///project/view.twig",
        "{% if app.request.attributes.get('_route') is same as('{|symfony.route.missing:product.sho|}') %}{% endif %}",
    );
    assert!(suggestions(&problems[0]).contains(&"product.show".to_owned()));
}

#[test]
fn route_diagnostics_mark_deprecated_controller_actions() {
    let fixture = fixture();
    let tests = [
        (
            "file:///project/Consumer.php",
            "<?php $this->generateUrl('{|symfony.controller.deprecated:product.legacy_method|}');",
        ),
        (
            "file:///project/view.twig",
            "{{ path('{|symfony.controller.deprecated:product.legacy_class|}') }}",
        ),
        (
            "file:///project/view.twig",
            "{{ path('{|symfony.controller.deprecated:product.legacy_service|}') }}",
        ),
    ];
    for (uri, source) in tests {
        let (_, problems) = check_problems(&fixture.analyzer, uri, source);
        assert_eq!(problems[0].severity, Some(DiagnosticSeverity::HINT));
        assert_eq!(problems[0].tags, vec![DiagnosticTag::DEPRECATED]);
    }
}
