//! Ports of the Go `*_test.go` files of this group.

mod container_constant;
mod controller;
mod duplicate;
mod event;
mod invokable_command;
mod route;
mod service;
mod service_argument;
mod tagged_service;

use std::path::Path;
use std::sync::Arc;

use serde_json::Value;
use swlsp_indexer::{Indexer, ParsedFile};
use swlsp_lsp::{Context, Problem, TextDocument};
use swlsp_php::PhpIndex;
use swlsp_symfony::ServiceIndex;
use swlsp_syntax::TextRange;

use crate::ProblemAnalyzer;

pub(super) fn p(path: &Path) -> String {
    path.to_string_lossy().into_owned()
}

pub(super) fn document(uri: &str, source: &str) -> Arc<TextDocument> {
    Arc::new(TextDocument::new(uri, source, 1))
}

pub(super) fn analyze(
    analyzer: &dyn ProblemAnalyzer,
    document: &Arc<TextDocument>,
) -> Vec<Problem> {
    analyzer
        .analyze(&Context::background(), document)
        .expect("analyze")
}

pub(super) fn php_index(dir: &Path, files: &[(&str, &str)]) -> PhpIndex {
    let php = PhpIndex::open(dir, None).expect("php index");
    for (path, source) in files {
        php.index(&ParsedFile::new(*path, source.as_bytes().to_vec()))
            .expect("index php");
    }
    php
}

pub(super) fn service_index(
    dir: &Path,
    php: Option<&PhpIndex>,
    files: &[(&str, &str)],
) -> Arc<ServiceIndex> {
    let services = ServiceIndex::new(&p(dir), dir.join("symfony-cache"), None).expect("services");
    if let Some(php) = php {
        services.set_php_index(Some(php.clone()));
    }
    for (path, source) in files {
        services
            .index(&ParsedFile::new(*path, source.as_bytes().to_vec()))
            .expect("index services");
    }
    Arc::new(services)
}

/// Go `problemRangeText`.
pub(super) fn range_text(document: &TextDocument, range: TextRange) -> String {
    document.source()[range].to_owned()
}

pub(super) fn payload(problem: &Problem) -> &serde_json::Map<String, Value> {
    problem
        .payload
        .as_ref()
        .and_then(Value::as_object)
        .expect("object payload")
}

/// Go `problemSuggestionStrings`.
pub(super) fn suggestions(problem: &Problem) -> Vec<String> {
    strings(
        problem
            .payload
            .as_ref()
            .and_then(|payload| payload.get("suggestions")),
    )
}

pub(super) fn strings(value: Option<&Value>) -> Vec<String> {
    value
        .and_then(Value::as_array)
        .map(|values| {
            values
                .iter()
                .filter_map(|value| value.as_str().map(str::to_owned))
                .collect()
        })
        .unwrap_or_default()
}

/// Go `assertDiagnosticCodes` (`ElementsMatch`).
pub(super) fn assert_codes(problems: &[Problem], expected: &[&str]) {
    let mut actual: Vec<&str> = problems.iter().map(|problem| problem.id.as_str()).collect();
    let mut expected = expected.to_vec();
    actual.sort_unstable();
    expected.sort_unstable();
    assert_eq!(actual, expected);
}

pub(super) fn with_code<'a>(problems: &'a [Problem], code: &str) -> Vec<&'a Problem> {
    problems
        .iter()
        .filter(|problem| problem.id.as_str() == code)
        .collect()
}
