//! "Suggest Service" XML action tests.

use std::sync::Arc;

use swlsp_indexer::{Indexer, ParsedFile};
use swlsp_lsp::{ActionProvider, CodeActionRequest, Context, TextDocument};
use swlsp_php::PhpIndex;
use swlsp_symfony::ServiceIndex;

use super::XmlServiceSuggestionCodeActionProvider;
use swlsp_ide::codeaction::testing::{apply_code_action_edit, path_string, request_at};

const URI: &str = "file:///project/services.xml";

struct Fixture {
    _dirs: Vec<tempfile::TempDir>,
    provider: XmlServiceSuggestionCodeActionProvider,
}

fn fixture() -> Fixture {
    let php_dir = tempfile::tempdir().unwrap();
    let php_index = PhpIndex::open(php_dir.path(), None).unwrap();
    php_index
        .index(&ParsedFile::new(
            "/project/src/Services.php".to_owned(),
            br"<?php
namespace App;
interface LoggerInterface {}
class Logger implements LoggerInterface {}
class SecondaryLogger implements LoggerInterface {}
class Consumer
{
    public function __construct(
        LoggerInterface $logger,
        string $channel,
        ?LoggerInterface $fallback = null,
    ) {}
}
"
            .to_vec(),
        ))
        .unwrap();
    let root = tempfile::tempdir().unwrap();
    let cache = tempfile::tempdir().unwrap();
    let root_path = path_string(root.path());
    let service_index = Arc::new(ServiceIndex::new(&root_path, cache.path(), None).unwrap());
    service_index.set_php_index(Some(php_index.clone()));
    service_index
        .index(&ParsedFile::new(
            format!("{root_path}/config/services.yaml"),
            b"services:\n  app.consumer:\n    class: App\\Consumer\n  app.logger:\n    class: App\\Logger\n  app.secondary_logger:\n    class: App\\SecondaryLogger\n".to_vec(),
        ))
        .unwrap();
    Fixture {
        _dirs: vec![php_dir, root, cache],
        provider: XmlServiceSuggestionCodeActionProvider::new(Some(service_index), Some(php_index)),
    }
}

fn request(source: &str, needle: &str) -> CodeActionRequest {
    match source.find(needle) {
        Some(offset) => request_at(URI, source, offset as u32, (offset + needle.len()) as u32),
        None => request_at(URI, source, 0, 0),
    }
}

#[test]
fn offers_compatible_constructor_services() {
    let fixture = fixture();
    let source = "<container>\n  <services>\n    <service id=\"app.consumer\" class=\"App\\Consumer\">\n      <argument/>\n    </service>\n  </services>\n</container>";
    let request = request(source, "<argument");
    let actions = fixture
        .provider
        .get_code_actions(&Context::background(), &request);
    assert_eq!(actions.len(), 2);
    assert_eq!(
        actions[0].title,
        "Symfony: Use service 'app.logger' for $logger"
    );
    assert_eq!(
        actions[1].title,
        "Symfony: Use service 'app.secondary_logger' for $logger"
    );
    let updated = apply_code_action_edit(source, &actions[0], URI);
    assert!(updated.contains("<argument type=\"service\" id=\"app.logger\"/>"));
    assert!(
        TextDocument::new(URI, &updated, 2)
            .parse_errors()
            .is_empty()
    );
}

#[test]
fn supports_named_arguments_and_replaces_value() {
    let fixture = fixture();
    let source = "<container>\n  <services>\n    <service id=\"app.consumer\" class=\"App\\Consumer\">\n      <argument>literal scalar</argument>\n      <argument key=\"$logger\" on-invalid=\"null\">%logger%</argument>\n    </service>\n  </services>\n</container>";
    let request = request(source, "%logger%");
    let actions = fixture
        .provider
        .get_code_actions(&Context::background(), &request);
    assert_eq!(actions.len(), 2);
    let updated = apply_code_action_edit(source, &actions[0], URI);
    assert!(updated.contains(
        "<argument key=\"$logger\" on-invalid=\"null\" type=\"service\" id=\"app.logger\"/>"
    ));
    assert!(!updated.contains("%logger%"));
}

#[test]
fn uses_positional_constructor_index() {
    let fixture = fixture();
    let source = "<container>\n  <services>\n    <service id=\"app.consumer\" class=\"App\\Consumer\">\n      <argument type=\"service\" id=\"app.logger\"/>\n      <argument/>\n    </service>\n  </services>\n</container>";
    let offset = source.rfind("<argument/>").unwrap() as u32;
    let request = request_at(URI, source, offset, offset + 11);
    assert!(
        fixture
            .provider
            .get_code_actions(&Context::background(), &request)
            .is_empty(),
        "the second constructor parameter is scalar"
    );
}

#[test]
fn suppresses_current_and_invalid_targets() {
    let fixture = fixture();
    for (name, source, needle, expected) in [
        (
            "current compatible service",
            "<service id=\"app.consumer\" class=\"App\\Consumer\"><argument type=\"service\" id=\"app.logger\"/></service>",
            "app.logger",
            1,
        ),
        (
            "unknown class",
            "<service id=\"app.consumer\" class=\"App\\Missing\"><argument/></service>",
            "<argument",
            0,
        ),
        (
            "outside argument",
            "<service id=\"app.consumer\" class=\"App\\Consumer\"/>",
            "App\\Consumer",
            0,
        ),
    ] {
        let request = request(source, needle);
        assert_eq!(
            fixture
                .provider
                .get_code_actions(&Context::background(), &request)
                .len(),
            expected,
            "{name}"
        );
    }
}
