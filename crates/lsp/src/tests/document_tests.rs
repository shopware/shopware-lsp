//! Port of `document_test.go`.

use std::sync::{Arc, Mutex};

use swlsp_language::LanguageId;
use swlsp_syntax::{SyntaxNode, SyntaxNodeExt, SyntaxToken, TextSize};

use crate::*;

fn syntax_at(
    manager: &DocumentManager,
    uri: &str,
    line: u32,
    character: u32,
) -> (SyntaxNode, SyntaxToken, SyntaxNode) {
    let syntax = manager
        .syntax_context(uri, line, character)
        .expect("open document");
    (
        syntax.root.expect("root"),
        syntax.token.expect("token"),
        syntax.node.expect("node"),
    )
}

#[test]
fn syntax_context_source_reuses_document_snapshot() {
    let source = "{{ product.name }}\n".repeat(256);
    let document = Arc::new(TextDocument::new(
        "file:///tmp/example.html.twig",
        &source,
        1,
    ));
    let syntax = SyntaxContext::new(document.clone(), 0, 2);
    assert_eq!(syntax.source(), source);
    assert!(std::ptr::eq(syntax.source(), document.source()));
    assert_eq!(SyntaxContext::default().source(), "");
}

#[test]
fn provider_performance_trace_configuration() {
    // Environment variables are process-wide; exercise the value parser
    // shared by SHOPWARE_LSP_TRACE_PROVIDERS and _DIAGNOSTICS instead of
    // mutating the environment of parallel tests.
    for (value, enabled) in [
        ("", false),
        ("0", false),
        (" false ", false),
        ("OFF", false),
        ("off", false),
        ("1", true),
        ("true", true),
        (" on ", true),
    ] {
        assert_eq!(
            crate::server::trace_value_enabled(value),
            enabled,
            "{value:?}"
        );
    }
}

#[test]
fn observer_replays_updates_and_closes() {
    let manager = DocumentManager::new();
    const URI: &str = "file:///tmp/Resources/app/administration/src/Card.vue";
    manager.open_document(URI, "<template><div /></template>", 1);
    let versions = Arc::new(Mutex::new(Vec::new()));
    let closed = Arc::new(Mutex::new(Vec::new()));
    let (seen, gone) = (versions.clone(), closed.clone());
    manager.register_observer(DocumentObserver {
        did_open_or_change: Some(Arc::new(move |document: &Arc<TextDocument>| {
            seen.lock().unwrap().push(document.version)
        })),
        did_close: Some(Arc::new(move |uri: &str| {
            gone.lock().unwrap().push(uri.to_owned())
        })),
    });
    assert_eq!(
        *versions.lock().unwrap(),
        vec![1],
        "already-open documents must replay"
    );
    manager.update_document(URI, "<template><span /></template>", 2);
    assert_eq!(*versions.lock().unwrap(), vec![1, 2]);
    manager.close_document(URI);
    assert_eq!(*closed.lock().unwrap(), vec![URI.to_owned()]);
    manager.close();
}

#[test]
fn php_uses_native_syntax_tree() {
    let manager = DocumentManager::new();
    const URI: &str = "file:///tmp/Controller.php";
    let source = "<?php class Controller { public function index() { return $this->redirectToRoute('storefront.home'); } }";
    manager.open_document(URI, source, 1);
    let document = manager.get_document(URI).unwrap();
    assert!(document.tree().is_some());
    assert_eq!(document.language, Some(LanguageId::Php));
    let column = source.find("storefront.home").unwrap() as u32 + 2;
    let (_, token, node) = syntax_at(&manager, URI, 0, column);
    assert_eq!(token.kind(), swlsp_parser_php::syntax::TK_STRING);
    assert!(swlsp_parser_php::query::string_in_call(
        &node,
        0,
        &["redirectToRoute"]
    ));
    assert_eq!(
        swlsp_parser_php::query::string_value(&node),
        "storefront.home"
    );
}

#[test]
fn twig_context_at_eof() {
    let manager = DocumentManager::new();
    const URI: &str = "file:///tmp/incomplete.twig";
    let source = "{{ value|";
    manager.open_document(URI, source, 1);
    let (_, token, node) = syntax_at(&manager, URI, 0, source.len() as u32);
    assert_eq!(token.text(), "|");
    assert_eq!(node.kind(), swlsp_parser_twig::syntax::TWIG_FILTER);
    assert!(swlsp_parser_twig::query::is_filter_position(&node));
}

#[test]
fn twig_context_uses_utf16_columns() {
    let manager = DocumentManager::new();
    const URI: &str = "file:///tmp/unicode.twig";
    manager.open_document(URI, "{{ path('😀route') }}", 1);
    // Nine ASCII units precede the emoji, which takes two units.
    let (_, token, node) = syntax_at(&manager, URI, 0, 11);
    assert_eq!(token.text(), "route");
    assert!(swlsp_parser_twig::query::string_in_function(
        &node,
        &["path"]
    ));
}

#[test]
fn json_uses_native_syntax_tree() {
    let manager = DocumentManager::new();
    const URI: &str = "file:///tmp/config.json";
    manager.open_document(URI, r#"{"emoji":"😀","enabled":true}"#, 1);
    let document = manager.get_document(URI).unwrap();
    assert_eq!(document.language, Some(LanguageId::Json));
    // The emoji takes two UTF-16 units; column 25 is inside "true".
    let (_, token, node) = syntax_at(&manager, URI, 0, 25);
    assert_eq!(token.text(), "true");
    assert_eq!(node.kind(), swlsp_parser_json::syntax::JSON_BOOLEAN);
}

#[test]
fn html_uses_twig_syntax_tree() {
    let manager = DocumentManager::new();
    const URI: &str = "file:///tmp/page.html";
    manager.open_document(URI, r#"<div data-controller="hello"></div>"#, 1);
    let document = manager.get_document(URI).unwrap();
    assert!(document.tree().is_some());
    assert_eq!(document.language, Some(LanguageId::Twig));
}

#[test]
fn vue_uses_mixed_embedded_syntax_tree() {
    let manager = DocumentManager::new();
    const URI: &str = "file:///tmp/Card.vue";
    let source = "<template><sw-card>{{ title }}</sw-card></template>\n<script setup>const title = ref('Card');</script>";
    manager.open_document(URI, source, 1);
    let document = manager.get_document(URI).unwrap();
    assert_eq!(document.language, Some(LanguageId::Vue));
    let root = document.root().unwrap();
    assert_eq!(root.kind(), swlsp_parser_vue::syntax::VUE_DOCUMENT);

    let template = root
        .node_at(TextSize::from(source.find("title").unwrap() as u32))
        .unwrap();
    assert_eq!(
        effective_syntax_language(Some(LanguageId::Vue), Some(&template)),
        Some(LanguageId::Twig)
    );
    assert!(
        swlsp_parser_twig::query::closest_node_of_kind(
            &template,
            &[swlsp_parser_twig::syntax::TWIG_LITERAL_NAME]
        )
        .is_some()
    );

    let script = root
        .node_at(TextSize::from(source.rfind("title").unwrap() as u32))
        .unwrap();
    assert_eq!(
        effective_syntax_language(Some(LanguageId::Vue), Some(&script)),
        Some(LanguageId::JavaScript)
    );
    assert_eq!(script.kind(), swlsp_parser_js::syntax::JS_IDENTIFIER);
    assert_eq!(
        effective_syntax_language(Some(LanguageId::Php), Some(&script)),
        Some(LanguageId::Php)
    );
}

#[test]
fn scss_uses_native_syntax_tree() {
    let manager = DocumentManager::new();
    const URI: &str = "file:///tmp/theme.scss";
    manager.open_document(URI, ".button { color: $sw-color-brand-primary; }", 1);
    assert_eq!(
        manager.get_document(URI).unwrap().language,
        Some(LanguageId::Scss)
    );
    let (_, token, node) = syntax_at(&manager, URI, 0, 30);
    assert_eq!(token.text(), "$sw-color-brand-primary");
    assert_eq!(node.kind(), swlsp_parser_scss::syntax::SCSS_VARIABLE);
    assert_eq!(
        swlsp_parser_scss::query::variable_name(&node).as_deref(),
        Some("sw-color-brand-primary")
    );
}

#[test]
fn scss_context_at_incomplete_feature() {
    let manager = DocumentManager::new();
    const URI: &str = "file:///tmp/features.scss";
    let source = r#"display: feature("ACCESSIBILITY"#;
    manager.open_document(URI, source, 1);
    let (_, _, node) = syntax_at(&manager, URI, 0, source.len() as u32);
    assert!(swlsp_parser_scss::query::string_in_function(
        &node,
        &["feature"]
    ));
    assert_eq!(
        swlsp_parser_scss::query::string_value(&node).as_deref(),
        Some("ACCESSIBILITY")
    );
}

#[test]
fn yaml_uses_native_syntax_tree() {
    let manager = DocumentManager::new();
    const URI: &str = "file:///tmp/services.yaml";
    manager.open_document(
        URI,
        "services:\n  App\\Service\\Example:\n    class: App\\Service\\Concrete\n",
        1,
    );
    assert_eq!(
        manager.get_document(URI).unwrap().language,
        Some(LanguageId::Yaml)
    );
    let (_, token, node) = syntax_at(&manager, URI, 2, 17);
    assert_eq!(token.text(), "App\\Service\\Concrete");
    assert_eq!(node.kind(), swlsp_parser_yaml::syntax::YAML_SCALAR);
    assert_eq!(
        swlsp_parser_yaml::query::pair_path(&node),
        vec!["services", "App\\Service\\Example", "class"]
    );
}

#[test]
fn yaml_context_at_empty_value() {
    let manager = DocumentManager::new();
    const URI: &str = "file:///tmp/services.yml";
    manager.open_document(URI, "services:\n  App\\Service\\Example:\n    class: ", 1);
    let (_, _, node) = syntax_at(&manager, URI, 2, "    class: ".len() as u32);
    assert_eq!(
        swlsp_parser_yaml::query::pair_path(&node),
        vec!["services", "App\\Service\\Example", "class"]
    );
}

#[test]
fn xml_uses_native_syntax_tree() {
    let manager = DocumentManager::new();
    const URI: &str = "file:///tmp/services.xml";
    manager.open_document(
        URI,
        r#"<container><service id="App\Service"/></container>"#,
        1,
    );
    assert_eq!(
        manager.get_document(URI).unwrap().language,
        Some(LanguageId::Xml)
    );
    let (_, token, node) = syntax_at(&manager, URI, 0, 30);
    assert_eq!(token.text(), r#""App\Service""#);
    assert_eq!(node.kind(), swlsp_parser_xml::syntax::XML_ATTRIBUTE_VALUE);
    assert_eq!(
        swlsp_parser_xml::query::attribute_value(&node).as_deref(),
        Some(r"App\Service")
    );
}

#[test]
fn xml_context_at_incomplete_attribute() {
    let manager = DocumentManager::new();
    const URI: &str = "file:///tmp/services.xml";
    let source = r#"<argument type="service" id="App\Service"#;
    manager.open_document(URI, source, 1);
    let (_, _, node) = syntax_at(&manager, URI, 0, source.len() as u32);
    assert_eq!(
        swlsp_parser_xml::query::attribute_name(&node).as_deref(),
        Some("id")
    );
    assert_eq!(
        swlsp_parser_xml::query::attribute_value(&node).as_deref(),
        Some(r"App\Service")
    );
}

#[test]
fn memoized_analysis_is_per_revision() {
    let document = TextDocument::new("file:///tmp/a.yaml", "a: b", 1);
    let calls = std::sync::atomic::AtomicUsize::new(0);
    let compute = || {
        calls.fetch_add(1, std::sync::atomic::Ordering::SeqCst);
        42u32
    };
    assert_eq!(*document.memoized_analysis(1, 7, compute), 42);
    assert_eq!(*document.memoized_analysis(1, 7, compute), 42);
    assert_eq!(calls.load(std::sync::atomic::Ordering::SeqCst), 1);
    document.memoized_analysis(1, 8, compute);
    document.memoized_analysis(2, 8, compute);
    assert_eq!(calls.load(std::sync::atomic::Ordering::SeqCst), 3);
}

#[test]
fn unregistered_extension_has_no_tree() {
    let document = TextDocument::new("file:///tmp/readme.md", "# Title", 1);
    assert!(document.tree().is_none());
    assert!(document.language.is_none());
    let syntax = SyntaxContext::new(Arc::new(document), 0, 1);
    assert!(syntax.root.is_none() && syntax.token.is_none() && syntax.node.is_none());
    assert_eq!(syntax.source(), "# Title");
}
