use std::sync::Arc;

use swlsp_lsp::{Context, FixResolution, TextDocument};
use swlsp_lsp_protocol::DiagnosticTag;
use swlsp_php::PhpIndex;
use swlsp_rewrite::HandleError;

use super::*;
use crate::inspections::test_support::{
    check_diagnostics, check_fix, fix_context, quick_fix_with_id, wire_range,
};

#[test]
fn unused_php_import_inspection_and_lazy_fix() {
    let dir = tempfile::tempdir().unwrap();
    let index = PhpIndex::open(dir.path(), None).unwrap();
    let inspection = new_php_imports(Some(index));
    let uri = "file:///workspace/live.php";
    let fixture =
        "<?php /* 😀 */ use Vendor\\{Used, {|php.unusedImport:Unused|} as Alias}; new Used;";
    let (document, problems) = check_diagnostics(inspection.as_ref(), uri, fixture);
    let problem = &problems[0];
    assert!(problem.tags.contains(&DiagnosticTag::UNNECESSARY));
    assert_eq!(wire_range(&document, problem.range).start.character, 33);
    let fix_id = problem.fixes[0].id.as_str();
    let presentation = check_fix(
        inspection.as_ref(),
        uri,
        fixture,
        fix_id,
        "<?php /* 😀 */ use Vendor\\{Used}; new Used;",
    );
    assert_eq!(presentation.resolution, FixResolution::Lazy);
    assert_eq!(presentation.title, "Remove unused import 'Alias'");

    // The lazy fix rejects a document changed since the problem was reported.
    let fix = quick_fix_with_id(inspection.as_ref(), fix_id);
    let mut context = fix_context(&document, problem, &problem.fixes[0], None);
    context.document = Arc::new(TextDocument::new(
        document.uri.clone(),
        &format!("{} new Alias;", document.source()),
        2,
    ));
    let error = fix
        .as_rewrite()
        .unwrap()
        .build(&Context::background(), &context)
        .unwrap_err();
    assert!(
        error
            .downcast_ref::<HandleError>()
            .is_some_and(HandleError::is_stale),
        "{error}"
    );
}

#[test]
fn unused_imports_suppression_and_incomplete_input() {
    let dir = tempfile::tempdir().unwrap();
    let index = PhpIndex::open(dir.path(), None).unwrap();
    for source in [
        "<?php\n/** @noinspection php.unusedImport */\nuse Vendor\\Unused;\n",
        "<?php\n/** @noinspection PhpUnusedAliasInspection */\nuse Vendor\\Unused;\n",
        "<?php use Vendor\\Unused; function incomplete(",
        "<?php use Vendor\\Unused; use Vendor\\",
    ] {
        check_diagnostics(
            new_php_imports(Some(index.clone())).as_ref(),
            "file:///workspace/live.php",
            source,
        );
    }
}
