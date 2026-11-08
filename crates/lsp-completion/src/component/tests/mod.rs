//! Tests for the component group (ports of the matching Go `_test.go`).

mod controller;
mod live_component_event;
mod php_attribute;
mod route;
mod twig_component;
mod twig_component_php;

use std::path::Path;
use std::sync::Arc;

use swlsp_lsp::{CompletionRequest, TextDocument};
use swlsp_lsp_protocol as protocol;
use swlsp_syntax::{SyntaxNode, SyntaxNodeExt, TextSize};

pub(super) fn p(path: &Path) -> String {
    path.to_string_lossy().into_owned()
}

/// Go `requireCompletion`.
pub(super) fn require_completion(
    items: &[protocol::CompletionItem],
    label: &str,
) -> protocol::CompletionItem {
    items
        .iter()
        .find(|item| item.label == label)
        .cloned()
        .unwrap_or_else(|| panic!("{label:?} not in {:?}", labels(items)))
}

/// Go `completionLabels`.
pub(super) fn labels(items: &[protocol::CompletionItem]) -> Vec<String> {
    items.iter().map(|item| item.label.clone()).collect()
}

/// The item's `textEdit` decoded as a plain `TextEdit`.
pub(super) fn edit(item: &protocol::CompletionItem) -> protocol::TextEdit {
    item.text_edit.clone().expect("text edit")
}

pub(super) fn additional_edit(item: &protocol::CompletionItem, index: usize) -> protocol::TextEdit {
    item.additional_text_edits[index].clone()
}

/// Go `completionRangeText` (trimmed source text of `range`).
pub(super) fn range_text(document: &TextDocument, range: protocol::Range) -> String {
    let start = document
        .line_index()
        .offset_utf16(range.start.line, range.start.character) as usize;
    let end = document
        .line_index()
        .offset_utf16(range.end.line, range.end.character) as usize;
    document.source()[start..end].trim().to_string()
}

pub(super) fn node_at(document: &TextDocument, offset: u32) -> Option<SyntaxNode> {
    document.root()?.node_at(TextSize::from(offset))
}

/// A request at `offset` for `node` (default: the node at `offset`, or at
/// `offset - 1` like Go `bundleResourceCompletionRequest`).
pub(super) fn request_for(
    document: &Arc<TextDocument>,
    node: Option<SyntaxNode>,
    offset: u32,
) -> CompletionRequest {
    let node = node.or_else(|| node_at(document, offset)).or_else(|| {
        (offset > 0)
            .then(|| node_at(document, offset - 1))
            .flatten()
    });
    crate::test_support::completion_request(document.clone(), node, offset)
}

/// Go `completionCaret`.
pub(super) fn caret(source: &str) -> (String, u32) {
    let offset = source.find("<caret>").expect("caret");
    (source.replacen("<caret>", "", 1), offset as u32)
}
