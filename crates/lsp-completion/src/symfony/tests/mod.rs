//! Ports of the Go tests of the Symfony completion group.

mod bundle_resource;
mod service_deprecation;
mod service_method;
mod service_named_argument;
mod service_php;
mod snippet;
mod translation;
mod yaml_route_authoring;
mod yaml_service_authoring;

use std::path::Path;
use std::sync::Arc;

use swlsp_indexer::{Indexer, ParsedFile};
use swlsp_lsp::{CompletionProvider, CompletionRequest, Context, SyntaxContext, TextDocument};
use swlsp_lsp_protocol as protocol;
use swlsp_php::PhpIndex;
use swlsp_syntax::{SyntaxNode, SyntaxNodeExt, TextSize};

pub(super) fn p(path: &Path) -> String {
    path.to_string_lossy().into_owned()
}

/// Go `completionCaret`: removes `<caret>` and returns its offset.
pub(super) fn completion_caret(source: &str) -> (String, usize) {
    let offset = source.find("<caret>").expect("source has <caret>");
    (source.replacen("<caret>", "", 1), offset)
}

/// Go `bundleResourceCompletionRequest`.
pub(super) fn bundle_request(
    path: &str,
    source: &str,
    offset: usize,
) -> (Arc<TextDocument>, CompletionRequest) {
    let document = Arc::new(TextDocument::new(swlsp_uriutil::file_uri(path), source, 1));
    let root = document.root().unwrap();
    let mut node = root.node_at(TextSize::from(offset as u32));
    if node.is_none() && offset > 0 {
        node = root.node_at(TextSize::from(offset as u32 - 1));
    }
    let request = request_for(&document, node, offset as u32);
    (document, request)
}

/// A request for `document` at `offset` with an explicit node.
pub(super) fn request_for(
    document: &Arc<TextDocument>,
    node: Option<SyntaxNode>,
    offset: u32,
) -> CompletionRequest {
    let (line, character) = document.line_index().position_utf16(offset);
    let mut params = protocol::CompletionParams::default();
    params.text_document.uri = document.uri.clone();
    params.position = protocol::Position { line, character };
    CompletionRequest {
        params,
        syntax: SyntaxContext {
            language: document.language,
            document: Some(document.clone()),
            root: document.root(),
            token: None,
            node,
        },
    }
}

pub(super) fn completions(
    provider: &dyn CompletionProvider,
    request: &CompletionRequest,
) -> Vec<protocol::CompletionItem> {
    provider.get_completions(&Context::background(), request)
}

pub(super) fn labels(items: &[protocol::CompletionItem]) -> Vec<String> {
    items.iter().map(|item| item.label.clone()).collect()
}

/// Go `requireCompletion`.
#[track_caller]
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

/// The decoded `TextEdit` of an item.
#[track_caller]
pub(super) fn edit(item: &protocol::CompletionItem) -> protocol::TextEdit {
    item.text_edit.clone().expect("item has a text edit")
}

/// Go `completionRangeText`.
pub(super) fn range_text(document: &TextDocument, range: protocol::Range) -> String {
    let start = document
        .line_index()
        .offset_utf16(range.start.line, range.start.character) as usize;
    let end = document
        .line_index()
        .offset_utf16(range.end.line, range.end.character) as usize;
    document.source()[start..end].trim().to_string()
}

pub(super) fn php_index(dir: &Path, files: &[(&str, &str)]) -> PhpIndex {
    let php = PhpIndex::open(dir, None).unwrap();
    for (path, source) in files {
        php.index(&ParsedFile::new(*path, source.as_bytes().to_vec()))
            .unwrap();
    }
    php
}

pub(super) fn index_file(indexer: &dyn Indexer, path: &str, source: &str) {
    indexer
        .index(&ParsedFile::new(path, source.as_bytes().to_vec()))
        .unwrap();
}

pub(super) fn kind(value: protocol::CompletionItemKind) -> Option<protocol::CompletionItemKind> {
    Some(value)
}
