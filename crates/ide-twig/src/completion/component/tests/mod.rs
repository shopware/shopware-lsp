//! Tests for the component completion providers.

mod live_component_event;
mod php_attribute;
mod twig_component;
mod twig_component_php;

use std::path::Path;

use swlsp_lsp::TextDocument;
use swlsp_lsp_protocol as protocol;
use swlsp_syntax::{SyntaxNode, SyntaxNodeExt, TextSize};

pub(super) fn p(path: &Path) -> String {
    path.to_string_lossy().into_owned()
}

/// The item labelled `label`; panics when it is missing.
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

/// The labels of `items`.
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

pub(super) fn node_at(document: &TextDocument, offset: u32) -> Option<SyntaxNode> {
    document.root()?.node_at(TextSize::from(offset))
}
