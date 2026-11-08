use std::sync::Arc;

use swlsp_lsp::{Context, FixResolution, TextDocument};
use swlsp_lsp_protocol::DiagnosticTag;
use swlsp_php::PhpIndex;
use swlsp_rewrite::HandleError;

use crate::shopware::*;
use crate::test_support::{document, fix_context, inspect, quick_fix_with_id, wire_range};

#[test]
fn unused_php_import_inspection_and_lazy_fix() {
    let dir = tempfile::tempdir().unwrap();
    let index = PhpIndex::open(dir.path(), None).unwrap();
    let inspection = new_php_imports(Some(index));
    let source = "<?php /* 😀 */ use Vendor\\{Used, Unused as Alias}; new Used;";
    let document = document("file:///workspace/live.php", source);
    let problems = inspect(inspection.as_ref(), &document);
    assert_eq!(problems.len(), 1);
    let problem = &problems[0];
    assert_eq!(problem.id.as_str(), "php.unusedImport");
    assert!(problem.tags.contains(&DiagnosticTag::UNNECESSARY));
    assert_eq!(&source[problem.range], "Unused");
    assert_eq!(wire_range(&document, problem.range).start.character, 33);
    assert_eq!(problem.fixes.len(), 1);
    let fix = quick_fix_with_id(inspection.as_ref(), problem.fixes[0].id.as_str());
    let mut context = fix_context(&document, problem, &problem.fixes[0], None);
    let presentation = fix
        .present(&Context::background(), &context)
        .unwrap()
        .expect("presented");
    assert_eq!(presentation.resolution, FixResolution::Lazy);
    assert_eq!(presentation.title, "Remove unused import 'Alias'");
    let plan = fix
        .as_rewrite()
        .unwrap()
        .build(&Context::background(), &context)
        .unwrap();
    assert_eq!(plan.documents.len(), 1);
    assert_eq!(
        plan.documents[0].apply().unwrap(),
        "<?php /* 😀 */ use Vendor\\{Used}; new Used;"
    );
    context.document = Arc::new(TextDocument::new(
        document.uri.clone(),
        &format!("{source} new Alias;"),
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
        let problems = inspect(
            new_php_imports(Some(index.clone())).as_ref(),
            &document("file:///workspace/live.php", source),
        );
        assert!(problems.is_empty(), "{source}");
    }
}
