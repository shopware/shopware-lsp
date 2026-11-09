use std::sync::Arc;

use swlsp_indexer::{Indexer, ParsedFile};
use swlsp_lsp::{CompletionProvider, CompletionRequest, Context, TextDocument};
use swlsp_lsp_protocol as protocol;
use swlsp_parser_php::query as phpquery;
use swlsp_parser_php::syntax::PHP_STRING;
use swlsp_parser_twig::query as twigquery;
use swlsp_parser_twig::syntax::TWIG_LITERAL_STRING;
use swlsp_php::PhpIndex;
use swlsp_symfony::RouteIndexer;
use swlsp_syntax::SyntaxNode;
use tempfile::TempDir;

use super::{caret, edit, labels, node_at, range_text, request_for, require_completion};
use crate::RouteCompletionProvider;

struct Fixture {
    _cache: TempDir,
    provider: RouteCompletionProvider,
}

fn route_index(content: &[u8]) -> (TempDir, Arc<RouteIndexer>) {
    let cache = tempfile::tempdir().unwrap();
    let routes = Arc::new(RouteIndexer::new(cache.path(), None).unwrap());
    routes
        .index(&ParsedFile::new(
            "/project/config/routes.yaml",
            content.to_vec(),
        ))
        .unwrap();
    (cache, routes)
}

fn fixture() -> Fixture {
    let (cache, routes) = route_index(b"product.show:\n    path: /products/{id}\n");
    Fixture {
        _cache: cache,
        provider: RouteCompletionProvider::new(routes, None),
    }
}

/// A route completion request with an explicit node with no cursor position.
fn route_request(
    uri: &str,
    source: &str,
    node: impl Fn(&SyntaxNode) -> SyntaxNode,
) -> CompletionRequest {
    let document = Arc::new(TextDocument::new(uri, source, 1));
    let node = node(&document.root().unwrap());
    let mut request = crate::test_support::completion_request(document, Some(node), 0);
    request.params.position = Default::default();
    request
}

fn complete(
    provider: &RouteCompletionProvider,
    request: &CompletionRequest,
) -> Vec<protocol::CompletionItem> {
    provider.get_completions(&Context::background(), request)
}

#[test]
fn route_name_completion_includes_path_and_parameters() {
    let f = fixture();
    let request = route_request("file:///project/view.twig", "{{ path('') }}", |root| {
        twigquery::nodes(root, &[TWIG_LITERAL_STRING])[0].clone()
    });
    let item = require_completion(&complete(&f.provider, &request), "product.show");
    assert_eq!(item.kind, Some(protocol::CompletionItemKind::REFERENCE));
    assert_eq!(item.detail, "/products/{id} (id)");
}

#[test]
fn route_path_completion_in_php_attributes_and_annotations() {
    let f = fixture();
    for source in [
        "<?php\n#[Route('/pro<caret>')]\nfunction show() {}\n",
        "<?php\n#[Route(name: 'draft', path: '/pro<caret>')]\nfunction show() {}\n",
        "<?php\n/** @Route(\"/pro<caret>\") */\nfunction show() {}\n",
        "<?php\n/** @Route(name=\"draft\", path=\"/pro<caret>\") */\nfunction show() {}\n",
    ] {
        let (source, offset) = caret(source);
        let document = Arc::new(TextDocument::new(
            swlsp_uriutil::file_uri("/project/src/Controller.php"),
            &source,
            1,
        ));
        let request = request_for(&document, None, offset);
        let item = require_completion(&complete(&f.provider, &request), "/products/{id}");
        assert_eq!(item.kind, Some(protocol::CompletionItemKind::REFERENCE));
        assert_eq!(item.detail, "product.show");
        let edit = edit(&item);
        assert_eq!(edit.new_text, "/products/{id}");
        assert_eq!(range_text(&document, edit.range), "/pro", "{source}");
    }
}

#[test]
fn generated_route_name_completion_in_php_attributes_and_annotations() {
    let f = fixture();
    for source in [
        "<?php\nnamespace App\\Controller;\nclass FooController {\n    #[Route(path: '/foo', name: 'dr<caret>')]\n    public function foo1(): void {}\n}\n",
        "<?php\nnamespace App\\Controller;\nclass FooController {\n    /** @Route(path=\"/foo\", name=\"dr<caret>\") */\n    public function foo1(): void {}\n}\n",
    ] {
        let (source, offset) = caret(source);
        let document = Arc::new(TextDocument::new(
            swlsp_uriutil::file_uri("/project/src/FooController.php"),
            &source,
            1,
        ));
        let request = request_for(&document, None, offset);
        let item = require_completion(&complete(&f.provider, &request), "app_foo_foo1");
        assert_eq!(item.kind, Some(protocol::CompletionItemKind::REFERENCE));
        let edit = edit(&item);
        assert_eq!(edit.new_text, "app_foo_foo1");
        assert_eq!(range_text(&document, edit.range), "dr");
    }
}

#[test]
fn phpdoc_route_assistant_tag_completes_call_arguments() {
    let (_routes_cache, routes) = route_index(b"product.show:\n  path: /products/{id}\n");
    let php_cache = tempfile::tempdir().unwrap();
    let php = PhpIndex::open(php_cache.path(), None).unwrap();
    php.index(&ParsedFile::new(
        "/project/src/RouteAware.php",
        br#"<?php
interface RouteAware
{
    /** @param string $route #Route */
    public function open(string $route): void;
}

class TestClass implements RouteAware
{
    /** @param string $route #Route */
    public function __construct(string $route) {}

    public function open(string $route): void {}

    /** @param string $value ordinary text */
    public function other(string $value): void {}
}

/** @param string $route #Route */
function open_route(string $route): void {}
"#
        .to_vec(),
    ))
    .unwrap();
    let provider = RouteCompletionProvider::new(routes, Some(php));
    for (source, found) in [
        ("<?php new TestClass('prod<caret>');", true),
        (
            "<?php\n$helper = new TestClass('seed');\n$helper->open('prod<caret>');\n",
            true,
        ),
        ("<?php open_route('prod<caret>');", true),
        (
            "<?php\n$helper = new TestClass('seed');\n$helper->other('prod<caret>');\n",
            false,
        ),
    ] {
        let (source, offset) = caret(source);
        let document = Arc::new(TextDocument::new(
            swlsp_uriutil::file_uri("/project/src/Usage.php"),
            &source,
            1,
        ));
        let request = request_for(&document, None, offset);
        let items = complete(&provider, &request);
        if !found {
            assert!(
                !labels(&items).contains(&"product.show".to_string()),
                "{source}"
            );
            continue;
        }
        let item = require_completion(&items, "product.show");
        let edit = edit(&item);
        assert_eq!(edit.new_text, "product.show");
        assert_eq!(range_text(&document, edit.range), "prod");
    }
}

#[test]
fn route_name_completion_in_twig_route_comparison() {
    let f = fixture();
    let request = route_request(
        "file:///project/view.twig",
        "{% if app.request.attributes.get('_route') in ['product.'] %}{% endif %}",
        |root| twigquery::nodes(root, &[TWIG_LITERAL_STRING])[1].clone(),
    );
    let item = require_completion(&complete(&f.provider, &request), "product.show");
    assert_eq!(item.detail, "/products/{id} (id)");

    let request = route_request(
        "file:///project/view.twig",
        "{% if app.request.attributes.get('_route') starts with 'product.' %}{% endif %}",
        |root| twigquery::nodes(root, &[TWIG_LITERAL_STRING])[1].clone(),
    );
    assert!(complete(&f.provider, &request).is_empty());
}

#[test]
fn route_parameter_completion_in_php_and_twig() {
    let f = fixture();
    let request = route_request(
        "file:///project/Controller.php",
        "<?php $this->redirectToRoute('product.show', ['' => 1]);",
        |root| {
            let strings = phpquery::nodes(root, &[PHP_STRING]);
            assert_eq!(strings.len(), 2);
            strings[1].clone()
        },
    );
    let items = complete(&f.provider, &request);
    require_completion(&items, "id");
    require_completion(&items, "_fragment");

    let request = route_request(
        "file:///project/view.twig",
        "{{ path('product.show', {'': product.id}) }}",
        |root| {
            let strings = twigquery::nodes(root, &[TWIG_LITERAL_STRING]);
            assert_eq!(strings.len(), 2);
            strings[1].clone()
        },
    );
    require_completion(&complete(&f.provider, &request), "id");
}

#[test]
fn route_parameter_completion_supports_twig_identifier_hash_keys() {
    let f = fixture();
    for source in [
        "{{ path('product.show', {<caret>}) }}",
        "{{ path('product.show', {i<caret>d}) }}",
        "{{ path('product.show', {i<caret>d: product.id}) }}",
        "{{ path('product.show', {'other': 1, i<caret>d}) }}",
        "{{ path('product.show', {'other': 1, i<caret>d: product.id}) }}",
    ] {
        let (source, offset) = caret(source);
        let document = Arc::new(TextDocument::new("file:///project/view.twig", &source, 1));
        let node = node_at(&document, offset)
            .or_else(|| node_at(&document, offset - 1))
            .unwrap();
        let mut request = crate::test_support::completion_request(document, Some(node), 0);
        request.params.position = Default::default();
        require_completion(&complete(&f.provider, &request), "id");
    }
}

#[test]
fn route_completion_wraps_twig_html_urls() {
    let f = fixture();
    for (source, marker, new_text) in [
        (
            r#"<a href="/prod">Product</a>"#,
            "/prod",
            "{{ path('product.show', {'id': 'x'}) }}",
        ),
        (
            "<form action=''></form>",
            "",
            r#"{{ path("product.show", {"id": "x"}) }}"#,
        ),
    ] {
        let offset = if marker.is_empty() {
            source.find("''").unwrap() + 1
        } else {
            source.find(marker).unwrap() + marker.len()
        };
        let document = Arc::new(TextDocument::new("file:///project/view.twig", source, 1));
        let node = node_at(&document, offset as u32);
        let mut request = crate::test_support::completion_request(document, node, 0);
        request.params.position = Default::default();
        let item = require_completion(&complete(&f.provider, &request), "product.show");
        let edit = edit(&item);
        assert_eq!(edit.new_text, new_text);
        assert_eq!(edit.range.start.line, 0);
    }
}

#[test]
fn route_completion_replaces_javascript_request_url_with_path() {
    let f = fixture();
    for source in [
        "fetch('/prod')",
        "axios.create({ baseURL: '/prod' })",
        "const options = { url: '/prod' }",
    ] {
        let document = Arc::new(TextDocument::new("file:///project/request.js", source, 1));
        let start = source.find("/prod").unwrap();
        let offset = start + "/prod".len();
        let node = node_at(&document, offset as u32);
        let mut request = crate::test_support::completion_request(document, node, 0);
        request.params.position = Default::default();
        let items = complete(&f.provider, &request);
        let item = require_completion(&items, "product.show");
        let path_item = require_completion(&items, "/products/{id}");
        assert_eq!(path_item.detail, "product.show");
        let edit = edit(&item);
        assert_eq!(edit.new_text, "/products/{id}");
        assert_eq!(
            edit.range,
            protocol::Range {
                start: protocol::Position {
                    line: 0,
                    character: start as u32
                },
                end: protocol::Position {
                    line: source[..offset].matches('\n').count() as u32,
                    character: offset as u32
                },
            }
        );
    }
}

#[test]
fn route_completion_ignores_unrelated_javascript_strings() {
    let f = fixture();
    let source = "logger.info('/prod')";
    let document = Arc::new(TextDocument::new("file:///project/request.js", source, 1));
    let node = node_at(&document, (source.find("/prod").unwrap() + 2) as u32);
    let request = crate::test_support::completion_request(document, node, 0);
    assert!(complete(&f.provider, &request).is_empty());
}
