//! Tests of the migration inspections (Go `shopware_migration*_test.go`).

mod api;
mod arguments;
mod calls;
mod declarations;
mod rules;

use std::sync::Arc;

use swlsp_indexer::{Indexer, ParsedFile};
use swlsp_lsp::{Context, Inspection, TextDocument};
use swlsp_php::PhpIndex;
use swlsp_shopware::{ResolvedVersion, VersionSource};
use tempfile::TempDir;

use crate::test_support::{apply_single, fix_context, inspect, quick_fix_with_id};

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

/// Go `migrationInspectionPHPIndex`.
pub(super) fn migration_inspection_php_index() -> TestIndex {
    let dir = tempfile::tempdir().expect("tempdir");
    let index = PhpIndex::open(dir.path(), None).expect("php index");
    TestIndex { _dir: dir, index }
}

/// Go `migrationInspectionVersion`.
pub(super) fn migration_inspection_version(major: i64, minor: i64) -> ResolvedVersion {
    let mut resolved = ResolvedVersion {
        source: VersionSource::Explicit,
        known: true,
        ..ResolvedVersion::default()
    };
    resolved.version.major = major;
    resolved.version.minor = minor;
    resolved
}

pub(super) fn migration(index: &TestIndex, minor: i64) -> Arc<dyn Inspection> {
    super::new_shopware_migration(
        Some(index.index.clone()),
        migration_inspection_version(6, minor),
    )
}

pub(super) fn document(uri: &str, source: &str) -> Arc<TextDocument> {
    crate::test_support::document(uri, source)
}

fn assert_parses(document: &TextDocument, updated: &str) {
    let reparsed = TextDocument::new(document.uri.clone(), updated, document.version + 1);
    assert!(
        reparsed.parse_errors().is_empty(),
        "updated source does not parse:\n{updated}"
    );
}

/// Go `applyOnlyMigrationFix`.
pub(super) fn apply_only_migration_fix(
    inspection: &Arc<dyn Inspection>,
    document: &Arc<TextDocument>,
    fix_id: &str,
) -> String {
    let problems = inspect(inspection.as_ref(), document);
    assert_eq!(problems.len(), 1, "problems: {problems:?}");
    let problem = &problems[0];
    assert_eq!(problem.fixes.len(), 1);
    let bound = &problem.fixes[0];
    assert_eq!(bound.id.0, fix_id);
    let fix = quick_fix_with_id(inspection.as_ref(), &bound.id.0);
    let updated = apply_single(&fix, &fix_context(document, problem, bound, None));
    assert_parses(document, &updated);
    updated
}

/// Go `applyMigrationFixByID`.
pub(super) fn apply_migration_fix_by_id(
    inspection: &Arc<dyn Inspection>,
    document: &Arc<TextDocument>,
    fix_id: &str,
) -> String {
    let problems = inspect(inspection.as_ref(), document);
    let matches: Vec<_> = problems
        .iter()
        .flat_map(|problem| {
            problem
                .fixes
                .iter()
                .filter(|bound| bound.id.0 == fix_id)
                .map(move |bound| (problem, bound))
        })
        .collect();
    assert_eq!(matches.len(), 1);
    let (problem, bound) = matches[0];
    let fix = quick_fix_with_id(inspection.as_ref(), &bound.id.0);
    let updated = apply_single(&fix, &fix_context(document, problem, bound, None));
    assert_parses(document, &updated);
    updated
}

pub(super) fn ctx() -> Context {
    Context::background()
}
