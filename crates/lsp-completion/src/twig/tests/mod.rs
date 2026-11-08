//! Ports of the Go Twig completion tests.

mod component_variable;
mod constant;
mod container_constant;
mod deprecation;
mod enum_completion;
mod guard;
mod include_parameter;
mod macros;
mod member_deprecation;
mod operator;
mod render_block;
mod see;
mod tag;
mod template_assistant;
mod test;
mod types;
mod variable;

use std::sync::Arc;

use swlsp_indexer::{Indexer, ParsedFile};
use swlsp_lsp::{CompletionProvider, CompletionRequest, Context, SyntaxContext, TextDocument};
use swlsp_lsp_protocol as protocol;
use swlsp_php::PhpIndex;
use swlsp_syntax::{SyntaxNodeExt, TextSize};
use swlsp_twig::TwigIndexer;
use tempfile::TempDir;

pub(super) fn tmp() -> TempDir {
    tempfile::tempdir().unwrap()
}

pub(super) fn php_index(dir: &TempDir) -> PhpIndex {
    PhpIndex::open(dir.path(), None).unwrap()
}

pub(super) fn twig_index(dir: &TempDir) -> Arc<TwigIndexer> {
    Arc::new(TwigIndexer::new(dir.path(), None).unwrap())
}

pub(super) fn index(indexer: &dyn Indexer, path: &str, source: &str) {
    indexer
        .index(&ParsedFile::new(path, source.as_bytes().to_vec()))
        .unwrap();
}

pub(super) fn p(path: &std::path::Path) -> String {
    path.to_string_lossy().into_owned()
}

/// A request whose node is looked up at `node_offset` (Go builds the
/// syntax context by hand in every test).
pub(super) fn request_with_node(
    document: Arc<TextDocument>,
    offset: u32,
    node_offset: u32,
) -> CompletionRequest {
    let root = document.root().unwrap();
    let node = root.node_at(TextSize::from(node_offset));
    let (line, character) = document.line_index().position_utf16(offset);
    let mut params = protocol::CompletionParams::default();
    params.text_document.uri = document.uri.clone();
    params.position = protocol::Position { line, character };
    CompletionRequest {
        params,
        syntax: SyntaxContext {
            language: document.language,
            document: Some(document),
            root: Some(root),
            token: None,
            node,
        },
    }
}

/// Go `twigCompletionAt`: node at `offset - 1`.
pub(super) fn twig_completion_at(uri: &str, source: &str, offset: usize) -> CompletionRequest {
    let document = Arc::new(TextDocument::new(uri, source, 1));
    let offset = offset as u32;
    request_with_node(document, offset, offset.saturating_sub(1))
}

/// Removes `<caret>` and requests completion there.
pub(super) fn caret_items(
    provider: &dyn CompletionProvider,
    uri: &str,
    source: &str,
) -> Vec<protocol::CompletionItem> {
    let offset = source.find("<caret>").expect("caret");
    let source = source.replacen("<caret>", "", 1);
    provider.get_completions(
        &Context::background(),
        &twig_completion_at(uri, &source, offset),
    )
}

pub(super) fn complete(
    provider: &dyn CompletionProvider,
    request: &CompletionRequest,
) -> Vec<protocol::CompletionItem> {
    provider.get_completions(&Context::background(), request)
}

pub(super) fn labels(items: &[protocol::CompletionItem]) -> Vec<String> {
    items.iter().map(|item| item.label.clone()).collect()
}

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

pub(super) fn documentation(item: &protocol::CompletionItem) -> String {
    item.documentation
        .as_ref()
        .map(|documentation| documentation.value.clone())
        .unwrap_or_default()
}

pub(super) fn edit_new_text(item: &protocol::CompletionItem) -> String {
    item.text_edit.as_ref().expect("text edit").new_text.clone()
}

pub(super) fn edit_range_text(document: &TextDocument, item: &protocol::CompletionItem) -> String {
    let range = item.text_edit.as_ref().expect("text edit").range;
    let start = document
        .line_index()
        .offset_utf16(range.start.line, range.start.character) as usize;
    let end = document
        .line_index()
        .offset_utf16(range.end.line, range.end.character) as usize;
    document.source()[start..end].trim().to_string()
}
