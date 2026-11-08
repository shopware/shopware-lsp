//! Adapter tests; group tests live next to their inspections.

use std::sync::Arc;

use serde_json::json;
use swlsp_language::LanguageId;
use swlsp_lsp::{BoxError, Context, Problem, TextDocument};
use swlsp_lsp_diagnostics::ProblemAnalyzer;
use swlsp_syntax::{TextRange, TextSize};

use crate::test_support::{apply_single, document, fix_context, inspect, quick_fix_with_id};
use crate::{SUGGESTION_FIX_ID, new_analyzer_inspection};

struct Fixed(fn() -> Vec<Problem>);

impl ProblemAnalyzer for Fixed {
    fn analyze(&self, _: &Context, _: &Arc<TextDocument>) -> Result<Vec<Problem>, BoxError> {
        Ok((self.0)())
    }
}

fn range(start: u32, end: u32) -> TextRange {
    TextRange::new(TextSize::from(start), TextSize::from(end))
}

#[test]
fn analyzer_inspection_binds_distinct_suggestions_and_replaces_range() {
    let doc = document("file:///a.json", "{\"key\": \"valeu\"}");
    let inspection = new_analyzer_inspection(
        "x",
        &[LanguageId::Json],
        "src",
        &["x.code"],
        vec![Arc::new(Fixed(|| {
            vec![Problem {
                id: "x.code".into(),
                range: range(9, 14),
                payload: Some(json!({"suggestions": ["value", " value ", "", 3, "other"]})),
                ..Problem::default()
            }]
        }))],
    );
    let problems = inspect(inspection.as_ref(), &doc);
    assert_eq!(problems.len(), 1);
    assert_eq!(problems[0].fixes.len(), 2);
    let fix = quick_fix_with_id(inspection.as_ref(), SUGGESTION_FIX_ID);
    let context = fix_context(&doc, &problems[0], &problems[0].fixes[0], None);
    let presentation = fix
        .present(&Context::background(), &context)
        .unwrap()
        .unwrap();
    assert_eq!(presentation.title, "Replace with 'value'");
    assert_eq!(apply_single(&fix, &context), "{\"key\": \"value\"}");
}

#[test]
fn analyzer_inspection_rejects_undeclared_codes_and_normalizes_payloads() {
    let doc = document("file:///a.json", "{}");
    let inspection = new_analyzer_inspection(
        "x",
        &[LanguageId::Json],
        "src",
        &["x.code"],
        vec![Arc::new(Fixed(|| {
            vec![Problem {
                id: "x.code".into(),
                payload: Some(json!("scalar")),
                ..Problem::default()
            }]
        }))],
    );
    let problems = inspect(inspection.as_ref(), &doc);
    assert_eq!(problems[0].payload, Some(json!({"payload": "scalar"})));

    let rejecting = new_analyzer_inspection(
        "x",
        &[LanguageId::Json],
        "src",
        &["x.code"],
        vec![Arc::new(Fixed(|| {
            vec![Problem {
                id: "x.other".into(),
                ..Problem::default()
            }]
        }))],
    );
    let mut collector = crate::test_support::ProblemCollector::default();
    let error = rejecting
        .inspect(&Context::background(), &doc, &mut collector)
        .unwrap_err();
    assert!(error.to_string().contains("undeclared problem"));
}
