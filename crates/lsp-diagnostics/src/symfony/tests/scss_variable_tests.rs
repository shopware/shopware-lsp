use std::sync::Arc;

use swlsp_lsp::{BoxError, Context};

use super::{analyze, index_file, tempdir};
use crate::symfony::{
    SCSS_VARIABLE_UNKNOWN_CODE, ScssVariableAnalyzer, ScssVariableIndexReadiness,
};
use crate::test_support::diagnostics_document;

fn open_style() -> (tempfile::TempDir, Arc<swlsp_style::Index>) {
    let dir = tempdir();
    let index = Arc::new(swlsp_style::Index::new(dir.path(), None).unwrap());
    (dir, index)
}

struct StaticReadiness(bool);

impl ScssVariableIndexReadiness for StaticReadiness {
    fn ready(&self, _ctx: &Context) -> Result<bool, BoxError> {
        Ok(self.0)
    }
}

#[test]
fn scss_variable_analyzer_uses_live_and_indexed_declarations() {
    let (_dir, index) = open_style();
    index_file(
        index.as_ref(),
        "/project/_variables.scss",
        "$project-value: red;\n$items: ();",
    );
    index_file(
        index.as_ref(),
        "/project/theme.json",
        r#"{"config":{"fields":{"theme-color":{"type":"color"}}}}"#,
    );
    let source = ".use {
  color: $project_value;
  background: $theme-color;
  border-color: $later;
}
$later: red;
@mixin paint($color) { color: $color; }
@include paint($color: $project-value);
@each $item in $items { color: $item; }
.module { z-index: math.$pi; }
.missing { color: $missing; }";
    let document = diagnostics_document("file:///project/main.scss", source);
    let problems = analyze(&ScssVariableAnalyzer::new(Some(index), None), &document);
    assert_eq!(problems.len(), 1);
    assert_eq!(problems[0].id.0, SCSS_VARIABLE_UNKNOWN_CODE);
    assert_eq!(
        problems[0].message,
        "SCSS variable '$missing' is not defined"
    );
    assert_eq!(&source[problems[0].range], "$missing");
}

#[test]
fn scss_variable_analyzer_does_not_use_stale_current_file_declaration() {
    let (_dir, index) = open_style();
    index_file(index.as_ref(), "/project/main.scss", "$removed: red;");
    let document = diagnostics_document("file:///project/main.scss", ".use { color: $removed; }");
    let problems = analyze(&ScssVariableAnalyzer::new(Some(index), None), &document);
    assert_eq!(problems.len(), 1);
    assert_eq!(problems[0].id.0, SCSS_VARIABLE_UNKNOWN_CODE);
}

#[test]
fn scss_variable_analyzer_waits_for_complete_workspace_index() {
    let (_dir, index) = open_style();
    let document = diagnostics_document("file:///project/main.scss", ".use { color: $missing; }");
    let not_ready =
        ScssVariableAnalyzer::new(Some(index.clone()), Some(Arc::new(StaticReadiness(false))));
    assert!(analyze(&not_ready, &document).is_empty());
    let ready = ScssVariableAnalyzer::new(Some(index), Some(Arc::new(StaticReadiness(true))));
    assert_eq!(analyze(&ready, &document).len(), 1);
}
