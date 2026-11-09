//! Tests for named service argument completion.

use std::sync::Arc;

use swlsp_lsp_protocol as protocol;
use swlsp_syntax::TextSize;

use super::*;
use crate::symfony::ServiceCompletionProvider;

fn named_argument_php_index(dir: &Path) -> PhpIndex {
    php_index(
        dir,
        &[(
            "/project/src/Consumer.php",
            r"<?php
namespace Psr\Log;
interface LoggerInterface {}

namespace App;
class ParentConsumer {
    public function __construct(
        \Psr\Log\LoggerInterface $logger,
        string $name,
        int $count = 0,
    ) {}
}
class ChildConsumer extends ParentConsumer {}
",
        )],
    )
}

fn named_argument_request(source: &str) -> (Arc<TextDocument>, CompletionRequest) {
    let (source, offset) = match source.find("<caret>") {
        Some(marker) => (source.replacen("<caret>", "", 1), marker),
        None => (source.to_string(), source.len()),
    };
    let document = Arc::new(TextDocument::new(
        "file:///project/config/services.yaml",
        &source,
        1,
    ));
    let node_offset = offset.saturating_sub(1) as u32;
    let node = document
        .root()
        .unwrap()
        .node_at(TextSize::from(node_offset));
    let request = request_for(&document, node, offset as u32);
    (document, request)
}

#[test]
fn yaml_service_named_argument_completion() {
    let dir = tempfile::tempdir().unwrap();
    let provider = ServiceCompletionProvider::new(None, Some(named_argument_php_index(dir.path())));
    let (document, request) = named_argument_request(
        "services:\n  app.consumer:\n    class: App\\ChildConsumer\n    arguments:\n      $log",
    );
    let items = completions(&provider, &request);
    let logger = require_completion(&items, "$logger");
    assert_eq!(logger.kind, kind(protocol::CompletionItemKind::VARIABLE));
    assert_eq!(logger.detail, r"Psr\Log\LoggerInterface");
    let edit = edit(&logger);
    assert_eq!(range_text(&document, edit.range), "$log");
    assert_eq!(edit.new_text, "$logger: ");
    assert!(
        logger
            .documentation
            .unwrap()
            .value
            .contains(r"App\ChildConsumer")
    );
}

#[test]
fn yaml_service_named_argument_completion_replaces_complete_key_and_excludes_used() {
    let dir = tempfile::tempdir().unwrap();
    let provider = ServiceCompletionProvider::new(None, Some(named_argument_php_index(dir.path())));
    let (document, request) = named_argument_request(
        "services:\n  app.consumer:\n    class: App\\ChildConsumer\n    arguments:\n      $logger: '@logger'\n      $na<caret>: value",
    );
    let items = completions(&provider, &request);
    let name = require_completion(&items, "$name");
    let edit = edit(&name);
    assert_eq!(range_text(&document, edit.range), "$na");
    assert_eq!(edit.new_text, "$name");
    assert!(!labels(&items).contains(&"$logger".to_string()));
}

#[test]
fn yaml_service_named_argument_completion_skips_factory_services() {
    let dir = tempfile::tempdir().unwrap();
    let provider = ServiceCompletionProvider::new(None, Some(named_argument_php_index(dir.path())));
    let (_, request) = named_argument_request(
        "services:\n  app.consumer:\n    class: App\\ChildConsumer\n    factory: ['@factory', create]\n    arguments:\n      $log",
    );
    assert!(completions(&provider, &request).is_empty());
}
