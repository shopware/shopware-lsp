//! Tests of the migration group (Go `shopware_migration*_test.go`,
//! `shopware_criteria_test.go`).

mod api;
mod arguments;
mod calls;
mod criteria;
mod declarations;
mod rules;

use std::sync::Arc;

use swlsp_indexer::{Indexer, ParsedFile};
use swlsp_lsp::{Context, Problem, TextDocument};
use swlsp_php::PhpIndex;
use swlsp_php_project::Version;
use swlsp_shopware::{ResolvedVersion, VersionSource};
use swlsp_syntax::TextRange;
use tempfile::TempDir;

use super::{ShopwareMigrationAnalyzer, ShopwareMigrationPayload};
use crate::ProblemAnalyzer;

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

/// Go `migrationTestPHPIndex`.
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

/// Go `resolvedShopwareMigrationVersion`.
pub(super) fn version(major: i64, minor: i64, patch: i64) -> ResolvedVersion {
    ResolvedVersion {
        version: Version::new(major, minor, patch),
        source: VersionSource::Explicit,
        known: true,
    }
}

pub(super) fn document(uri: &str, source: &str) -> Arc<TextDocument> {
    Arc::new(TextDocument::new(uri, source, 1))
}

pub(super) fn analyze(
    index: &TestIndex,
    resolved: ResolvedVersion,
    document: &Arc<TextDocument>,
) -> Vec<Problem> {
    ShopwareMigrationAnalyzer::new(Some(index.index.clone()), resolved)
        .analyze(&Context::background(), document)
        .expect("analyze")
}

/// Go `problemRangeText`.
pub(super) fn range_text(document: &TextDocument, range: TextRange) -> &str {
    &document.source()[range]
}

pub(super) fn payload(problem: &Problem) -> ShopwareMigrationPayload {
    serde_json::from_value(problem.payload.clone().expect("payload")).expect("migration payload")
}
