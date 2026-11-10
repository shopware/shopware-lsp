use crate::completion::ControllerCompletionProvider;
use std::collections::BTreeMap;
use std::path::Path;
use std::sync::Arc;
use swlsp_indexer::{Indexer, ParsedFile};
use swlsp_lsp::{CompletionProvider, Context, TextDocument};
use swlsp_lsp_protocol as protocol;
use swlsp_parser_twig::query as twigquery;
use swlsp_parser_twig::syntax::TWIG_LITERAL_STRING;
use swlsp_php::PhpIndex;
use swlsp_symfony::{RouteIndexer, ServiceIndex};

fn p(path: &Path) -> String {
    path.to_string_lossy().into_owned()
}

/// The item's `textEdit` decoded as a plain `TextEdit`.
fn edit(item: &protocol::CompletionItem) -> protocol::TextEdit {
    item.text_edit.clone().expect("text edit")
}

#[test]
fn controller_completion_includes_class_and_route_service_actions() {
    let cache = tempfile::tempdir().unwrap();
    let php = PhpIndex::open(cache.path(), None).unwrap();
    let class_dir = tempfile::tempdir().unwrap();
    let class_source = r#"<?php
namespace App\Controller;
class NavController {
    public function menu(string $section): void {}
    public function __invoke(): void {}
    protected function hidden(): void {}
}
namespace Acme\DemoBundle\Controller\Admin;
class LegacyController {
    public function showAction(): void {}
}"#;
    php.index(&ParsedFile::new(
        p(&class_dir.path().join("NavController.php")),
        class_source.as_bytes().to_vec(),
    ))
    .unwrap();
    let services_root = tempfile::tempdir().unwrap();
    let services_cache = tempfile::tempdir().unwrap();
    let services =
        Arc::new(ServiceIndex::new(&p(services_root.path()), services_cache.path(), None).unwrap());
    services
        .index(&ParsedFile::new(
            "/project/services.yaml",
            b"services:\n  app.navigation:\n    class: App\\Controller\\NavController\n".to_vec(),
        ))
        .unwrap();
    let routes_cache = tempfile::tempdir().unwrap();
    let routes = Arc::new(RouteIndexer::new(routes_cache.path(), None).unwrap());
    routes
        .index(&ParsedFile::new(
            "/project/routes.yaml",
            b"navigation:\n  path: /navigation\n  controller: app.navigation:menu\n".to_vec(),
        ))
        .unwrap();

    let document = Arc::new(TextDocument::new(
        "file:///project/navigation.html.twig",
        "{{ render(controller('')) }}",
        1,
    ));
    let literals = twigquery::nodes(&document.root().unwrap(), &[TWIG_LITERAL_STRING]);
    assert_eq!(literals.len(), 1);
    let mut request =
        crate::completion::test_support::completion_request(document, Some(literals[0].clone()), 0);
    request.params.position = Default::default();
    let provider = ControllerCompletionProvider::new(Some(php), Some(services), Some(routes));
    let items = provider.get_completions(&Context::background(), &request);
    let by_label: BTreeMap<_, _> = items
        .iter()
        .map(|item| (item.label.as_str(), item))
        .collect();
    for label in [
        "App\\Controller\\NavController::menu",
        "App\\Controller\\NavController",
        "app.navigation:menu",
        "app.navigation::menu",
        "app.navigation",
        "DemoBundle:Admin\\Legacy:show",
    ] {
        assert!(
            by_label.contains_key(label),
            "{label} in {:?}",
            by_label.keys()
        );
    }
    assert!(!by_label.contains_key("App\\Controller\\NavController::hidden"));
    assert_eq!(
        edit(by_label["App\\Controller\\NavController::menu"]).new_text,
        "App\\\\Controller\\\\NavController::menu"
    );
}
