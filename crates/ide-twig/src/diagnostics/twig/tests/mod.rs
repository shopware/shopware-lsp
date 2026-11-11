//! Tests for the Twig analyzer group.

mod component;
mod deprecation;
mod enums_macros;
mod markup;
mod member;
mod render_block;
mod snippet;
mod template;
mod translation;
mod versioning;

use std::path::Path;
use std::sync::Arc;

use swlsp_indexer::{Indexer, ParsedFile};
use swlsp_lsp::{Context, Problem, TextDocument};
use swlsp_php::PhpIndex;
use swlsp_syntax::TextRange;
use swlsp_twig::TwigIndexer;

use crate::diagnostics::ProblemAnalyzer;
use crate::diagnostics::test_support::check_problems;

pub(super) fn doc(uri: &str, source: &str) -> Arc<TextDocument> {
    Arc::new(TextDocument::new(uri, source, 1))
}

pub(super) fn doc_v(uri: &str, source: &str, version: i32) -> Arc<TextDocument> {
    Arc::new(TextDocument::new(uri, source, version))
}

pub(super) fn analyze(
    analyzer: &dyn ProblemAnalyzer,
    document: &Arc<TextDocument>,
) -> Vec<Problem> {
    analyzer
        .analyze(&Context::background(), document)
        .expect("analyze")
}

/// [`check_problems`] analyzing the document at `version` instead of 1, for
/// tests that reanalyze one URI with changed content.
pub(super) fn check_problems_v(
    analyzer: &dyn ProblemAnalyzer,
    uri: &str,
    fixture: &str,
    version: i32,
) -> (Arc<TextDocument>, Vec<Problem>) {
    swlsp_lsp::fixture::check(uri, fixture, |document| {
        analyze(analyzer, &doc_v(uri, document.source(), version))
    })
}

/// The plain source of `fixture`, without its problem markup.
pub(super) fn plain(fixture: &str) -> String {
    swlsp_lsp::fixture::parse(fixture).0
}

pub(super) fn index_file(indexer: &dyn Indexer, path: &str, source: &str) {
    indexer
        .index(&ParsedFile::new(path, source.as_bytes().to_vec()))
        .expect("index");
}

pub(super) fn twig_index(dir: &Path) -> Arc<TwigIndexer> {
    Arc::new(TwigIndexer::new(dir, None).expect("twig index"))
}

pub(super) fn php_index(dir: &Path) -> PhpIndex {
    PhpIndex::open(dir, None).expect("php index")
}

pub(super) fn tempdir() -> tempfile::TempDir {
    tempfile::tempdir().expect("tempdir")
}

pub(super) fn range_text(document: &TextDocument, range: TextRange) -> &str {
    &document.source()[range]
}

pub(super) fn suggestions(problem: &Problem) -> Vec<String> {
    payload_strings(problem, "suggestions")
}

pub(super) fn payload_strings(problem: &Problem, key: &str) -> Vec<String> {
    problem
        .payload
        .as_ref()
        .and_then(|payload| payload.get(key))
        .and_then(|value| value.as_array())
        .map(|values| {
            values
                .iter()
                .filter_map(|value| value.as_str().map(str::to_owned))
                .collect()
        })
        .unwrap_or_default()
}

pub(super) fn payload_str(problem: &Problem, key: &str) -> String {
    problem
        .payload
        .as_ref()
        .and_then(|payload| payload.get(key))
        .and_then(|value| value.as_str())
        .unwrap_or_default()
        .to_owned()
}
