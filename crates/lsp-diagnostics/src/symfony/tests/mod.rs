//! Tests of the Symfony analyzer group.

mod asset_tests;
mod console_tests;
mod doctrine_tests;
mod embedded_tests;
mod messenger_tests;
mod scss_variable_tests;
mod serializer_tests;
mod validation_tests;

use serde_json::Value;
use swlsp_indexer::{Indexer, ParsedFile};
use swlsp_lsp::Problem;
use swlsp_php::PhpIndex;
use tempfile::TempDir;

pub(super) fn tempdir() -> TempDir {
    tempfile::tempdir().unwrap()
}

pub(super) fn open_php() -> (TempDir, PhpIndex) {
    let dir = tempdir();
    let index = PhpIndex::open(dir.path(), None).unwrap();
    (dir, index)
}

pub(super) fn index_file(indexer: &dyn Indexer, path: &str, source: &str) {
    indexer
        .index(&ParsedFile::new(path, source.as_bytes().to_vec()))
        .unwrap();
}

/// The `suggestions` payload entries.
pub(super) fn suggestions(problem: &Problem) -> Vec<String> {
    match problem
        .payload
        .as_ref()
        .and_then(|payload| payload.get("suggestions"))
    {
        Some(Value::Array(values)) => values
            .iter()
            .filter_map(|value| value.as_str().map(str::to_owned))
            .collect(),
        _ => Vec::new(),
    }
}
