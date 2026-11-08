//! Ports of the Shopware group's `*_test.go` files.

use std::collections::BTreeMap;
use std::sync::Arc;

use swlsp_lsp::{Context, Problem, TextDocument};

use crate::ProblemAnalyzer;

mod app_script;
mod dal_entity;
mod decoration;
mod entity_snapshot;
mod php_local;
mod php_semantic;
mod services_xml;
mod store_composer;
mod theme;

pub(super) fn analyze(
    analyzer: &dyn ProblemAnalyzer,
    document: &Arc<TextDocument>,
) -> Vec<Problem> {
    analyzer
        .analyze(&Context::background(), document)
        .expect("analyze")
}

pub(super) fn problem_counts(problems: &[Problem]) -> BTreeMap<String, usize> {
    let mut result = BTreeMap::new();
    for problem in problems {
        *result.entry(problem.id.0.clone()).or_default() += 1;
    }
    result
}

pub(super) fn counts(entries: &[(&str, usize)]) -> BTreeMap<String, usize> {
    entries
        .iter()
        .map(|(code, count)| ((*code).to_owned(), *count))
        .collect()
}

pub(super) fn highlight<'a>(document: &'a TextDocument, problem: &Problem) -> &'a str {
    &document.source()[problem.range]
}

pub(super) fn highlights(document: &TextDocument, problems: &[Problem]) -> Vec<String> {
    problems
        .iter()
        .map(|problem| highlight(document, problem).to_owned())
        .collect()
}

pub(super) fn problem_ids(problems: &[Problem]) -> Vec<String> {
    problems
        .iter()
        .map(|problem| problem.id.0.clone())
        .collect()
}
