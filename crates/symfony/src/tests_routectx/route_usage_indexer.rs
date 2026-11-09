use swlsp_indexer::{Indexer, ParsedFile};
use swlsp_php::PhpIndex;

use super::*;
use crate::controller::parse_controller_reference;

fn index(cache: &std::path::Path, path: &str, source: &str) -> RouteUsageIndexer {
    let indexer = RouteUsageIndexer::new(cache, None).unwrap();
    indexer
        .index(&ParsedFile::new(path, source.as_bytes().to_vec()))
        .unwrap();
    indexer
}

#[test]
fn persists_static_twig_html_urls() {
    let cache = tempfile::tempdir().unwrap();
    let indexer = index(
        cache.path(),
        "/project/templates/catalog.twig",
        r#"<a href="/catalog/42?preview=1">Catalog</a>
<form action="/catalog/42"></form>
<a href="{{ path('catalog.show') }}">Dynamic</a>
<a href="https://example.test/catalog/42">External</a>"#,
    );
    let usages = indexer.get_html_route_usages().unwrap();
    assert_eq!(
        usages.iter().map(|usage| usage.line).collect::<Vec<_>>(),
        [1, 2, 4]
    );
    assert!(usages.iter().all(|usage| usage.path == "/catalog/42"));
    indexer.close().unwrap();
    let restored = RouteUsageIndexer::new(cache.path(), None).unwrap();
    assert_eq!(restored.get_html_route_usages().unwrap(), usages);
    restored.close().unwrap();
}

#[test]
fn persists_twig_route_comparisons() {
    let cache = tempfile::tempdir().unwrap();
    let path = "/project/templates/navigation.twig";
    let indexer = index(
        cache.path(),
        path,
        r"{% if app.request.attributes.get('_route') == 'catalog.show' %}{% endif %}
{% if app.request.attributes.get('_route') in ['catalog.list'] %}{% endif %}
{% if app.request.attributes.get('_route') starts with 'catalog.' %}{% endif %}",
    );
    let show = indexer.get_route("catalog.show").unwrap();
    assert_eq!(show.len(), 1);
    assert_eq!(show[0].file, path);
    assert_eq!(show[0].line, 1);
    let list = indexer.get_route("catalog.list").unwrap();
    assert_eq!(list.len(), 1);
    assert_eq!(list[0].line, 2);
    assert!(indexer.get_route("catalog.").unwrap().is_empty());
    indexer.close().unwrap();
    let restored = RouteUsageIndexer::new(cache.path(), None).unwrap();
    assert_eq!(restored.get_route("catalog.show").unwrap(), show);
    assert_eq!(restored.get_route("catalog.list").unwrap(), list);
    restored.close().unwrap();
}

#[test]
fn preserves_repeated_occurrences_and_cache_restore() {
    let cache = tempfile::tempdir().unwrap();
    let indexer = index(
        cache.path(),
        "/project/templates/repeated.twig",
        r#"{{ path('catalog.show') }}
{{ path('catalog.show') }}
<a href="/catalog/42">First</a>
<a href="/catalog/42">Second</a>"#,
    );
    let usages = indexer.get_route("catalog.show").unwrap();
    assert_eq!(
        usages.iter().map(|usage| usage.line).collect::<Vec<_>>(),
        [1, 2]
    );
    let html = indexer.get_html_route_usages().unwrap();
    assert_eq!(
        html.iter().map(|usage| usage.line).collect::<Vec<_>>(),
        [3, 4]
    );
    indexer.close().unwrap();
    let restored = RouteUsageIndexer::new(cache.path(), None).unwrap();
    assert_eq!(restored.get_route("catalog.show").unwrap(), usages);
    assert_eq!(restored.get_html_route_usages().unwrap(), html);
    restored.close().unwrap();
}

#[test]
fn persists_every_twig_controller_occurrence() {
    let cache = tempfile::tempdir().unwrap();
    let path = "/project/templates/navigation.twig";
    let indexer = index(
        cache.path(),
        path,
        r"{{ controller('App\\Controller\\NavController::menu') }}
{{ render(controller('\\App\\Controller\\NavController::menu')) }}
{{ controller('app.navigation:menu') }}",
    );
    let reference = parse_controller_reference(r"App\Controller\NavController::menu").unwrap();
    let usages = indexer.get_controller_usages(&reference).unwrap();
    assert_eq!(usages.len(), 2);
    assert_eq!(usages[0].file, path);
    assert_eq!(usages[0].controller, r"App\Controller\NavController::menu");
    assert_ne!(usages[0].range, usages[1].range);
    indexer.close().unwrap();
    let restored = RouteUsageIndexer::new(cache.path(), None).unwrap();
    assert_eq!(restored.get_controller_usages(&reference).unwrap(), usages);
    assert_eq!(restored.get_all_controller_usages().unwrap().len(), 3);
    restored.close().unwrap();
}

#[test]
fn uses_typed_php_route_generators() {
    let cache = tempfile::tempdir().unwrap();
    let php_dir = tempfile::tempdir().unwrap();
    let php = PhpIndex::open(php_dir.path(), None).unwrap();
    for (path, source) in [
        (
            "/project/vendor/AbstractController.php",
            r"<?php
namespace Symfony\Bundle\FrameworkBundle\Controller;
abstract class AbstractController {
    protected function redirectToRoute(string $route): mixed {}
}
",
        ),
        (
            "/project/vendor/UrlGeneratorInterface.php",
            r"<?php
namespace Symfony\Component\Routing\Generator;
interface UrlGeneratorInterface {
    public function generate(string $name): string;
}
",
        ),
    ] {
        php.index(&ParsedFile::new(path, source.as_bytes().to_vec()))
            .unwrap();
    }
    let indexer = RouteUsageIndexer::new(cache.path(), None).unwrap();
    indexer.set_php_index(Some(php.clone()));
    indexer
        .index(&ParsedFile::new(
            "/project/src/Controller.php",
            br"<?php
namespace App;
use Symfony\Bundle\FrameworkBundle\Controller\AbstractController;
use Symfony\Component\Routing\Generator\UrlGeneratorInterface;
final class Other { public function generate(string $name): string {} }
final class Controller extends AbstractController {
    public function run(UrlGeneratorInterface $router, Other $other): void {
        $this->redirectToRoute('controller.route');
        $router->generate('router.route');
        $other->generate('unrelated.route');
    }
}
"
            .to_vec(),
        ))
        .unwrap();
    assert_eq!(indexer.get_route("controller.route").unwrap().len(), 1);
    assert_eq!(indexer.get_route("router.route").unwrap().len(), 1);
    assert!(indexer.get_route("unrelated.route").unwrap().is_empty());
    indexer.close().unwrap();
    php.close().unwrap();
}

#[test]
fn source_gate_is_case_insensitive() {
    assert!(crate::util::contains_fold_ascii(
        b"$this->ReDiReCtToRoUtE('example');",
        "redirecttoroute"
    ));
    assert!(crate::util::contains_fold_ascii(
        b"$router->GeNeRaTe('example');",
        "generate"
    ));
    assert!(!crate::util::contains_fold_ascii(
        b"$service->translate('example');",
        "generate"
    ));
    // Candidates at either end, a truncated candidate, and an empty needle.
    assert!(crate::util::contains_fold_ascii(b"Resource", "resource"));
    assert!(crate::util::contains_fold_ascii(
        b"type: RESOURCE",
        "resource"
    ));
    assert!(!crate::util::contains_fold_ascii(b"rr resourc", "resource"));
    assert!(crate::util::contains_fold_ascii(b"", ""));
    assert!(ROUTE_USAGE_PHP_CANDIDATE_MATCHER.contains_str("$router->GeNeRaTe('x')"));
}
