mod entity_schema;
mod shopware;
mod symfony;

use std::path::Path;

use serde::Serialize;
use serde_json::Value;
use swlsp_indexer::{Indexer, ParsedFile};
use swlsp_lsp::Context;
use swlsp_php::PhpIndex;

pub(super) fn ctx() -> Context {
    Context::background()
}

pub(super) fn path_string(path: impl AsRef<Path>) -> String {
    path.as_ref().to_string_lossy().into_owned()
}

pub(super) fn raw<T: Serialize>(value: &T) -> Value {
    serde_json::to_value(value).unwrap()
}

pub(super) fn write(path: impl AsRef<Path>, content: &str) {
    let path = path.as_ref();
    std::fs::create_dir_all(path.parent().unwrap()).unwrap();
    std::fs::write(path, content).unwrap();
}

pub(super) fn php_index(cache: &Path) -> PhpIndex {
    PhpIndex::open(cache, None).unwrap()
}

pub(super) fn index_source(indexer: &dyn Indexer, path: impl AsRef<Path>, source: &str) {
    indexer
        .index(&ParsedFile::new(
            path_string(path),
            source.as_bytes().to_vec(),
        ))
        .unwrap();
}

/// Concatenates every inserted text of a workspace edit.
pub(super) fn edit_text(edit: &swlsp_lsp_protocol::WorkspaceEdit) -> String {
    let mut text = String::new();
    for change in &edit.document_changes {
        for edit in &change.edits {
            text.push_str(&edit.new_text);
        }
    }
    text
}
