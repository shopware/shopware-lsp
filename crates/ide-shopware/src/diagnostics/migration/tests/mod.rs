//! Tests of the migration and Criteria analyzers.

mod api;
mod arguments;
mod calls;
mod criteria;
mod declarations;
mod rules;

use std::sync::Arc;

use swlsp_indexer::{Indexer, ParsedFile};
use swlsp_lsp::{Problem, TextDocument, fixture};
use swlsp_php::PhpIndex;
use swlsp_php_project::Version;
use swlsp_shopware::{ResolvedVersion, VersionSource};
use tempfile::TempDir;

use super::{ShopwareMigrationAnalyzer, ShopwareMigrationPayload};
use crate::diagnostics::test_support::check_problems;

pub(super) struct TestIndex {
    _dir: TempDir,
    pub index: PhpIndex,
}

impl TestIndex {
    pub fn add(&self, path: &str, source: &str) {
        self.index
            .index(&ParsedFile::new(path, source.as_bytes().to_vec()))
            .expect("index fixture");
    }
}

pub(super) fn migration_test_php_index() -> TestIndex {
    let dir = tempfile::tempdir().expect("tempdir");
    let index = PhpIndex::open(dir.path(), None).expect("php index");
    let test = TestIndex { _dir: dir, index };
    for (path, source) in [
        (
            "/project/vendor/EntitySearchResult.php",
            "<?php\nnamespace Shopware\\Core\\Framework\\DataAbstractionLayer\\Search;\nclass EntitySearchResult {}",
        ),
        (
            "/project/vendor/ProductListingResult.php",
            "<?php\nnamespace Shopware\\Core\\Content\\Product\\SalesChannel\\Listing;\nuse Shopware\\Core\\Framework\\DataAbstractionLayer\\Search\\EntitySearchResult;\nclass ProductListingResult extends EntitySearchResult {}",
        ),
        (
            "/project/vendor/EntityCollection.php",
            "<?php\nnamespace Shopware\\Core\\Framework\\DataAbstractionLayer;\nclass EntityCollection {}",
        ),
    ] {
        test.add(path, source);
    }
    test
}

pub(super) fn version(major: i64, minor: i64, patch: i64) -> ResolvedVersion {
    ResolvedVersion {
        version: Version::new(major, minor, patch),
        source: VersionSource::Explicit,
        known: true,
    }
}

/// Asserts that the migration analyzer for `resolved` reports exactly the
/// problems marked in `fixture` (see [`check_problems`]).
pub(super) fn check(
    index: &TestIndex,
    resolved: ResolvedVersion,
    uri: &str,
    fixture: &str,
) -> (Arc<TextDocument>, Vec<Problem>) {
    check_problems(
        &ShopwareMigrationAnalyzer::new(Some(index.index.clone()), resolved),
        uri,
        fixture,
    )
}

/// `fixture` keeping only the markers whose code is in `codes`, for the same
/// source analyzed at a version where fewer migrations apply.
pub(super) fn only(fixture: &str, codes: &[&str]) -> String {
    let (source, marks) = fixture::parse(fixture);
    let kept: Vec<_> = marks
        .into_iter()
        .filter(|mark| codes.contains(&mark.code.as_str()))
        .collect();
    fixture::render(&source, &kept)
}

pub(super) fn payload(problem: &Problem) -> ShopwareMigrationPayload {
    serde_json::from_value(problem.payload.clone().expect("payload")).expect("migration payload")
}
