use std::sync::Arc;

use swlsp_lsp::{BoxError, Context};

use super::{index_file, tempdir};
use crate::symfony::{ScssVariableAnalyzer, ScssVariableIndexReadiness};
use crate::test_support::check_problems;

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
    let fixture = ".use {
  color: $project_value;
  background: $theme-color;
  border-color: $later;
}
$later: red;
@mixin paint($color) { color: $color; }
@include paint($color: $project-value);
@each $item in $items { color: $item; }
.module { z-index: math.$pi; }
.missing { color: {|scss.variable.unknown:$missing|}; }";
    let (_, problems) = check_problems(
        &ScssVariableAnalyzer::new(Some(index), None),
        "file:///project/main.scss",
        fixture,
    );
    assert_eq!(
        problems[0].message,
        "SCSS variable '$missing' is not defined"
    );
}

#[test]
fn scss_variable_analyzer_does_not_use_stale_current_file_declaration() {
    let (_dir, index) = open_style();
    index_file(index.as_ref(), "/project/main.scss", "$removed: red;");
    check_problems(
        &ScssVariableAnalyzer::new(Some(index), None),
        "file:///project/main.scss",
        ".use { color: {|scss.variable.unknown:$removed|}; }",
    );
}

#[test]
fn scss_variable_analyzer_waits_for_complete_workspace_index() {
    let (_dir, index) = open_style();
    let uri = "file:///project/main.scss";
    let not_ready =
        ScssVariableAnalyzer::new(Some(index.clone()), Some(Arc::new(StaticReadiness(false))));
    check_problems(&not_ready, uri, ".use { color: $missing; }");
    let ready = ScssVariableAnalyzer::new(Some(index), Some(Arc::new(StaticReadiness(true))));
    check_problems(
        &ready,
        uri,
        ".use { color: {|scss.variable.unknown:$missing|}; }",
    );
}
