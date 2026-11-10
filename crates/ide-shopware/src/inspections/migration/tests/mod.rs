//! Tests of the migration inspections.

mod api;
mod arguments;
mod calls;
mod declarations;
mod rules;

use std::sync::Arc;

use swlsp_indexer::{Indexer, ParsedFile};
use swlsp_lsp::{FixPresentation, Inspection, TextDocument};
use swlsp_php::PhpIndex;
use swlsp_shopware::{ResolvedVersion, VersionSource};
use tempfile::TempDir;

use crate::inspections::test_support::{check_diagnostics, check_fix};

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

pub(super) fn migration_inspection_php_index() -> TestIndex {
    let dir = tempfile::tempdir().expect("tempdir");
    let index = PhpIndex::open(dir.path(), None).expect("php index");
    TestIndex { _dir: dir, index }
}

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
    Arc::new(crate::inspections::test_support::Combined(
        super::new_shopware_migration_inspections(
            Some(index.index.clone()),
            migration_inspection_version(6, minor),
        ),
    ))
}

fn assert_parses(uri: &str, updated: &str) {
    let reparsed = TextDocument::new(uri, updated, 2);
    assert!(
        reparsed.parse_errors().is_empty(),
        "updated source does not parse:\n{updated}"
    );
}

/// `before` marks exactly the reported problems, the single one binds only
/// `fix_id`, and the fix turns `before` into `after`, which parses.
pub(super) fn check_only_migration_fix(
    inspection: &Arc<dyn Inspection>,
    uri: &str,
    before: &str,
    fix_id: &str,
    after: &str,
) -> FixPresentation {
    let (_, problems) = check_diagnostics(inspection.as_ref(), uri, before);
    let [problem] = problems.as_slice() else {
        panic!("expected one problem: {problems:?}");
    };
    assert_eq!(problem.fixes.len(), 1);
    assert_eq!(problem.fixes[0].id.0, fix_id);
    let presentation = check_fix(inspection.as_ref(), uri, before, fix_id, after);
    assert_parses(uri, after);
    presentation
}

/// `before` marks exactly the reported problems, one of which binds `fix_id`,
/// and the fix turns `before` into `after`, which parses.
pub(super) fn check_migration_fix_by_id(
    inspection: &Arc<dyn Inspection>,
    uri: &str,
    before: &str,
    fix_id: &str,
    after: &str,
) {
    check_diagnostics(inspection.as_ref(), uri, before);
    check_fix(inspection.as_ref(), uri, before, fix_id, after);
    assert_parses(uri, after);
}
