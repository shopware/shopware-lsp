//! Adapter tests; group tests live next to their inspections.

use std::sync::Arc;

use crate::diagnostics::ProblemAnalyzer;
use serde_json::json;
use swlsp_language::LanguageId;
use swlsp_lsp::{BoxError, Context, InspectionScope, InspectionSpec, Problem, TextDocument};
use swlsp_syntax::{TextRange, TextSize};

use crate::inspections::test_support::{check_diagnostics, check_fix};
use crate::inspections::{SUGGESTION_FIX_ID, new_analyzer_inspection};

const URI: &str = "file:///a.json";

struct Fixed(fn() -> Vec<Problem>);

impl ProblemAnalyzer for Fixed {
    fn analyze(&self, _: &Context, _: &Arc<TextDocument>) -> Result<Vec<Problem>, BoxError> {
        Ok((self.0)())
    }
}

const SPEC: InspectionSpec = InspectionSpec {
    id: "x",
    description: "Test inspection.",
    domain: "",
    scope: InspectionScope::Framework,
    languages: &[LanguageId::Json],
    source: "src",
    codes: &["x.code"],
};

fn range(start: u32, end: u32) -> TextRange {
    TextRange::new(TextSize::from(start), TextSize::from(end))
}

#[test]
fn analyzer_inspection_binds_distinct_suggestions_and_replaces_range() {
    let inspection = new_analyzer_inspection(
        SPEC,
        vec![Arc::new(Fixed(|| {
            vec![Problem {
                id: "x.code".into(),
                range: range(9, 14),
                payload: Some(json!({"suggestions": ["value", " value ", "", 3, "other"]})),
                ..Problem::default()
            }]
        }))],
    );
    let fixture = "{\"key\": \"{|x.code:valeu|}\"}";
    let (_, problems) = check_diagnostics(inspection.as_ref(), URI, fixture);
    assert_eq!(problems[0].fixes.len(), 2);
    let presentation = check_fix(
        inspection.as_ref(),
        URI,
        fixture,
        SUGGESTION_FIX_ID,
        "{\"key\": \"value\"}",
    );
    assert_eq!(presentation.title, "Replace with 'value'");
}

#[test]
fn analyzer_inspection_rejects_undeclared_codes_and_normalizes_payloads() {
    let inspection = new_analyzer_inspection(
        SPEC,
        vec![Arc::new(Fixed(|| {
            vec![Problem {
                id: "x.code".into(),
                payload: Some(json!("scalar")),
                ..Problem::default()
            }]
        }))],
    );
    let (doc, problems) = check_diagnostics(inspection.as_ref(), URI, "{|x.code:|}{}");
    assert_eq!(problems[0].payload, Some(json!({"payload": "scalar"})));

    let rejecting = new_analyzer_inspection(
        SPEC,
        vec![Arc::new(Fixed(|| {
            vec![Problem {
                id: "x.other".into(),
                ..Problem::default()
            }]
        }))],
    );
    let mut collector = crate::inspections::test_support::ProblemCollector::default();
    let error = rejecting
        .inspect(&Context::background(), &doc, &mut collector)
        .unwrap_err();
    assert!(error.to_string().contains("undeclared problem"));
}
