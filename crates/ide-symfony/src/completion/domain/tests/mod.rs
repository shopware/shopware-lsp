//! Tests for the domain completion providers.

mod asset;
mod console;
mod doctrine;
mod environment;
mod event;
mod http_client;
mod messenger;
mod response_constant;
mod symfony_config;
mod validation;

use std::sync::Arc;

use swlsp_indexer::{Indexer, ParsedFile};
use swlsp_lsp::{CompletionRequest, SyntaxContext, TextDocument};
use swlsp_lsp_protocol as protocol;
use swlsp_php::PhpIndex;
use swlsp_syntax::{SyntaxNode, SyntaxNodeExt, TextSize};

pub(super) fn ctx() -> swlsp_lsp::Context {
    swlsp_lsp::Context::background()
}

pub(super) fn document(uri: &str, source: &str) -> Arc<TextDocument> {
    Arc::new(TextDocument::new(uri, source, 1))
}

pub(super) fn node_at(document: &TextDocument, offset: usize) -> Option<SyntaxNode> {
    document.root()?.node_at(TextSize::from(offset as u32))
}

/// A request for the node at `offset`, with a zero position.
pub(super) fn node_request(document: &Arc<TextDocument>, offset: usize) -> CompletionRequest {
    let mut params = protocol::CompletionParams::default();
    params.text_document.uri = document.uri.clone();
    CompletionRequest {
        params,
        syntax: SyntaxContext {
            language: document.language,
            root: document.root(),
            token: None,
            node: node_at(document, offset),
            document: Some(document.clone()),
        },
    }
}

/// A request for the node at `offset`.
pub(super) fn request(document: &Arc<TextDocument>, offset: usize) -> CompletionRequest {
    crate::completion::test_support::completion_request(
        document.clone(),
        node_at(document, offset),
        offset as u32,
    )
}

pub(super) fn index_file(indexer: &dyn Indexer, path: &str, source: &str) {
    indexer
        .index(&ParsedFile::new(path, source.as_bytes().to_vec()))
        .unwrap();
}

pub(super) fn php_index(cache: &tempfile::TempDir) -> PhpIndex {
    PhpIndex::open(cache.path(), None).unwrap()
}

pub(super) fn require_completion<'a>(
    items: &'a [protocol::CompletionItem],
    label: &str,
) -> &'a protocol::CompletionItem {
    items
        .iter()
        .find(|item| item.label == label)
        .unwrap_or_else(|| panic!("{label:?} not in {items:#?}"))
}

pub(super) fn labels(items: &[protocol::CompletionItem]) -> Vec<String> {
    items.iter().map(|item| item.label.clone()).collect()
}

pub(super) fn text_edit(item: &protocol::CompletionItem) -> protocol::TextEdit {
    item.text_edit.clone().expect("text edit")
}

/// The source text of `range`.
pub(super) fn range_text(document: &TextDocument, range: protocol::Range) -> String {
    let index = document.line_index();
    let start = index.offset_utf16(range.start.line, range.start.character) as usize;
    let end = index.offset_utf16(range.end.line, range.end.character) as usize;
    document.source()[start..end].trim().to_string()
}

pub(super) fn tempdir() -> tempfile::TempDir {
    tempfile::tempdir().unwrap()
}
