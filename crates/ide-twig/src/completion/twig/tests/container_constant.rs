use swlsp_lsp_protocol::CompletionItemKind;
use tempfile::TempDir;

use super::*;
use crate::completion::twig::ContainerConstantCompletionProvider;

fn container_fixture() -> (ContainerConstantCompletionProvider, TempDir) {
    let dir = tmp();
    let php = php_index(&dir);
    index(
        &php,
        "/project/src/Mode.php",
        r"<?php
namespace {
    const APP_LIMIT = 10;
}
namespace App {
    class Mode {
        public const ACTIVE = 'active';
        private const SECRET = 'secret';
    }
    enum State {
        public const ALIAS = self::ACTIVE;
        case ACTIVE;
    }
}
",
    );
    (ContainerConstantCompletionProvider::new(Some(php)), dir)
}

fn container_request(source: &str) -> (Arc<TextDocument>, CompletionRequest) {
    let document = Arc::new(TextDocument::new(
        "file:///project/config/services.yaml",
        source,
        1,
    ));
    let offset = source.len() as u32;
    (
        document.clone(),
        request_with_node(document, offset, offset - 1),
    )
}

#[test]
fn container_constant_completion() {
    let (provider, _dir) = container_fixture();
    let (document, request) = container_request(r"value: !php/const App\Mode::A");
    let items = complete(&provider, &request);
    let active = require_completion(&items, "ACTIVE");
    assert_eq!(active.kind, Some(CompletionItemKind::CONSTANT));
    assert_eq!(edit_range_text(&document, &active), "A");
    assert_eq!(edit_new_text(&active), "ACTIVE");
    assert!(!labels(&items).contains(&"SECRET".to_string()));

    let (_, request) = container_request("value: !php/const APP");
    require_completion(&complete(&provider, &request), "APP_LIMIT");
    let (_, request) = container_request(r"value: !php/const App\M");
    require_completion(&complete(&provider, &request), "App\\Mode::ACTIVE");
}

#[test]
fn container_constant_completion_supports_empty_and_legacy_values() {
    let (provider, _dir) = container_fixture();
    for source in ["value: !php/const ", "value: !php/const:"] {
        let (_, request) = container_request(source);
        require_completion(&complete(&provider, &request), "APP_LIMIT");
    }
}

#[test]
fn container_enum_completion() {
    let (provider, _dir) = container_fixture();
    let (document, request) = container_request(r"value: !php/enum App\State::A");
    let items = complete(&provider, &request);
    let active = require_completion(&items, "ACTIVE");
    assert_eq!(active.kind, Some(CompletionItemKind::ENUM_MEMBER));
    assert_eq!(edit_range_text(&document, &active), "A");
    assert!(!labels(&items).contains(&"ALIAS".to_string()));

    let (_, request) = container_request(r"value: !php/enum App\S");
    let items = complete(&provider, &request);
    require_completion(&items, "App\\State");
    require_completion(&items, "App\\State::ACTIVE");
    assert!(!labels(&items).contains(&"App\\Mode::ACTIVE".to_string()));
    assert!(!labels(&items).contains(&"APP_LIMIT".to_string()));
}
