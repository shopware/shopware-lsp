//! Organize-imports, extract-variable and extract-method tests.

use std::sync::Arc;

use swlsp_lsp::{ActionProvider, Context, TextDocument};
use swlsp_lsp_protocol::{self as protocol, CodeActionKind};
use swlsp_php::PhpIndex;

use crate::codeaction::{PhpExtractMethodProvider, PhpExtractVariableProvider, PhpImportsProvider};
use swlsp_ide::codeaction::testing::request_for_document;
use swlsp_ide::codeaction::testing::request_for_range;

const URI: &str = "file:///workspace/live.php";

fn index() -> (tempfile::TempDir, PhpIndex) {
    let dir = tempfile::tempdir().unwrap();
    let index = PhpIndex::open(dir.path(), None).unwrap();
    (dir, index)
}

fn doc(source: &str, version: i32) -> Arc<TextDocument> {
    Arc::new(TextDocument::new(URI, source, version))
}

fn col_range(start: usize, end: usize) -> protocol::Range {
    protocol::Range {
        start: protocol::Position {
            line: 0,
            character: start as u32,
        },
        end: protocol::Position {
            line: 0,
            character: end as u32,
        },
    }
}

#[test]
fn organize_imports_is_versioned_and_preserves_suppressed_imports() {
    let (_dir, index) = index();
    let ctx = Context::background();
    let source =
        "<?php\n/** @noinspection php.unusedImport */\nuse Vendor\\Keep;\nuse Vendor\\Unused;\n";
    let provider = PhpImportsProvider::new(Some(index));
    let actions =
        provider.get_code_actions(&ctx, &request_for_range(doc(source, 8), col_range(0, 0)));
    assert_eq!(actions.len(), 1);
    assert_eq!(actions[0].title, "Organize Imports");
    assert_eq!(
        actions[0].kind,
        Some(CodeActionKind::SOURCE_ORGANIZE_IMPORTS)
    );
    let edit = actions[0].edit.as_ref().unwrap();
    assert!(!edit.document_changes.is_empty());
    assert!(edit.changes.is_empty());
    assert_eq!(
        edit.document_changes[0]
            .text_document
            .as_ref()
            .unwrap()
            .version,
        Some(8)
    );
    for source in [
        "<?php use Vendor\\Unused; function broken(",
        "<?php use Vendor\\Used; new Used;",
    ] {
        let actions =
            provider.get_code_actions(&ctx, &request_for_range(doc(source, 9), col_range(0, 0)));
        assert!(actions.is_empty(), "{source}");
    }
}

#[test]
fn extract_variable_uses_snapshot_and_utf16() {
    let source = "<?php\n/* 😀 */ $x = build();";
    let document = doc(source, 7);
    let start = source.find("build()").unwrap() as u32;
    let request = request_for_document(document, start, start + 7);
    let actions =
        PhpExtractVariableProvider::new(None).get_code_actions(&Context::background(), &request);
    assert_eq!(actions.len(), 1);
    assert_eq!(actions[0].title, "Extract variable '$extracted'");
    assert_eq!(actions[0].kind, Some(CodeActionKind::REFACTOR_EXTRACT));
    let change = &actions[0].edit.as_ref().unwrap().document_changes[0];
    assert_eq!(change.text_document.as_ref().unwrap().version, Some(7));
    assert_eq!(change.edits.len(), 2);
    assert_eq!(change.edits[1].range, request.params.range);
    assert_eq!(change.edits[1].new_text, "$extracted");
}

#[test]
fn extract_variable_rejects_incomplete_snapshot() {
    for source in [
        "<?php $x =& build();",
        "<?php return build(",
        "<?php return build(); function incomplete(",
    ] {
        let position = source.find("build").unwrap();
        let actions = PhpExtractVariableProvider::new(None).get_code_actions(
            &Context::background(),
            &request_for_range(doc(source, 1), col_range(position, position)),
        );
        assert!(actions.is_empty(), "{source}");
    }
}

#[test]
fn extract_variable_argument_reference_resolution() {
    let (_dir, index) = index();
    for (declaration, available) in [
        ("function value() { return 1; }", true),
        ("function &value() { static $x = 1; return $x; }", false),
    ] {
        let source = format!("<?php {declaration} consume(value());");
        let start = source.rfind("value()").unwrap();
        let actions = PhpExtractVariableProvider::new(Some(index.clone())).get_code_actions(
            &Context::background(),
            &request_for_range(doc(&source, 1), col_range(start, start + 7)),
        );
        if available {
            assert_eq!(actions.len(), 1);
            assert!(
                actions[0].edit.as_ref().unwrap().document_changes[0].edits[0]
                    .new_text
                    .contains("($extracted = value())")
            );
        } else {
            assert!(actions.is_empty());
        }
    }
}

#[test]
fn extract_method_uses_overlay_and_inherited_names() {
    let (_dir, index) = index();
    let source = "<?php\nclass ParentClass { protected function extractedMethod() {} }\nclass Child extends ParentClass { public function run($a) { /* 😀 */ return $a + 1; } }";
    let start = source.find("return $a").unwrap() as u32;
    let end = start + "return $a + 1;".len() as u32;
    let request = request_for_document(doc(source, 9), start, end);
    let actions = PhpExtractMethodProvider::new(Some(index))
        .get_code_actions(&Context::background(), &request);
    assert_eq!(actions.len(), 1);
    assert_eq!(actions[0].title, "Extract method 'extractedMethod2'");
    let change = &actions[0].edit.as_ref().unwrap().document_changes[0];
    assert_eq!(change.text_document.as_ref().unwrap().version, Some(9));
    assert_eq!(change.edits.len(), 2);
    assert_eq!(change.edits[0].range, request.params.range);
    assert_eq!(
        change.edits[0].new_text,
        "return $this->extractedMethod2($a);"
    );
}

#[test]
fn extract_method_rejects_unknown_hierarchy_and_incomplete_syntax() {
    let (_dir, index) = index();
    for source in [
        "<?php class Child extends Missing { public function run($a) { return $a; } }",
        "<?php class Child { public function run($a) { return $a; }",
    ] {
        let start = source.find("return").unwrap();
        let actions = PhpExtractMethodProvider::new(Some(index.clone())).get_code_actions(
            &Context::background(),
            &request_for_range(doc(source, 1), col_range(start, start)),
        );
        assert!(actions.is_empty(), "{source}");
    }
}

#[test]
fn extract_function_namespace_and_collision() {
    let (_dir, index) = index();
    let source =
        "<?php namespace App; function extractedFunction() {} function run($a) { echo $a; }";
    let start = source.find("echo").unwrap();
    let actions = PhpExtractMethodProvider::new(Some(index)).get_code_actions(
        &Context::background(),
        &request_for_range(doc(source, 3), col_range(start, start)),
    );
    assert_eq!(actions.len(), 1);
    assert_eq!(actions[0].title, "Extract function 'extractedFunction2'");
    let change = &actions[0].edit.as_ref().unwrap().document_changes[0];
    assert_eq!(change.edits[0].new_text, "extractedFunction2($a);");
    assert!(!change.edits[1].new_text.contains("private"));
}
