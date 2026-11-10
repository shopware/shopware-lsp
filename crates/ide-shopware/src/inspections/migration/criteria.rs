//! The `shopware.criteria` inspection.

use std::sync::Arc;

use crate::diagnostics::ShopwareCriteriaAnalyzer;
use serde::Deserialize;
use serde_json::{Map, Value};
use swlsp_language::LanguageId;
use swlsp_lsp::{
    BoundFix, BoxError, Context, DiagnosticId, FixContext, FixId, FixPresentation, Inspection,
    InspectionDefinition, InspectionScope, ProblemDefinition, QuickFix, RewriteQuickFix, bind_fix,
};
use swlsp_lsp_protocol as protocol;
use swlsp_rewrite::{Builder, WorkspacePlan};
use swlsp_syntax::{TextRange, TextSize};

use super::{invalid, quick_fix_presentation, single_document_plan};
use crate::inspections::inspection::{BindProblems, BoundInspection};
use crate::inspections::resolve_anchor;

const CRITERIA_ID_FIX_ID: &str = "move-id-filter-to-criteria";

#[derive(Debug, Default, Deserialize)]
#[serde(rename_all = "camelCase", default)]
struct CriteriaIdFixPayload {
    safe: bool,
    criteria_start: u32,
    criteria_end: u32,
    filter_start: u32,
    filter_end: u32,
    argument: String,
}

pub fn new_shopware_criteria() -> Arc<dyn Inspection> {
    let bind: BindProblems = Arc::new(
        |_: &DiagnosticId, payload: &Map<String, Value>| -> Vec<BoundFix> {
            if payload.get("safe") != Some(&Value::Bool(true)) {
                return Vec::new();
            }
            vec![bind_fix(CRITERIA_ID_FIX_ID, Value::Object(payload.clone()))]
        },
    );
    Arc::new(BoundInspection {
        definition: InspectionDefinition {
            id: "shopware.criteria".to_owned(),
            description: "Criteria built with an ID equality filter that should pass the IDs to the Criteria constructor.",
            domain: "shopware.dal",
            scope: InspectionScope::Framework,
            languages: vec![LanguageId::Php],
            problems: vec![ProblemDefinition {
                id: DiagnosticId::from("shopware.criteria.id-filter"),
                source: "shopware-lsp".to_owned(),
                default_severity: protocol::DiagnosticSeverity::WARNING,
                disabled_by_default: false,
            }],
        },
        analyzers: vec![Arc::new(ShopwareCriteriaAnalyzer::new())],
        fixes: vec![Arc::new(CriteriaIdFix)],
        bind: Some(bind),
    })
}

struct CriteriaIdFix;

impl QuickFix for CriteriaIdFix {
    fn id(&self) -> FixId {
        FixId::from(CRITERIA_ID_FIX_ID)
    }

    fn present(&self, _: &Context, fix: &FixContext) -> Result<Option<FixPresentation>, BoxError> {
        let payload: CriteriaIdFixPayload = fix.fix_payload()?;
        if !payload.safe || payload.argument.is_empty() {
            return Ok(None);
        }
        Ok(Some(quick_fix_presentation(
            "Move ID constraint to Criteria constructor",
        )))
    }

    fn as_rewrite(&self) -> Option<&dyn RewriteQuickFix> {
        Some(self)
    }
}

impl RewriteQuickFix for CriteriaIdFix {
    fn build(&self, _: &Context, fix: &FixContext) -> Result<WorkspacePlan, BoxError> {
        let payload: CriteriaIdFixPayload = fix.fix_payload()?;
        if !payload.safe || payload.argument.is_empty() {
            return Err(invalid("criteria ID rewrite is no longer safe"));
        }
        resolve_anchor(fix)?;
        let length = fix.document.source().len();
        if payload.criteria_end as usize > length
            || payload.filter_end as usize > length
            || payload.criteria_start > payload.criteria_end
            || payload.filter_start > payload.filter_end
        {
            return Err(invalid("criteria ID rewrite ranges changed"));
        }
        let range =
            |start: u32, end: u32| TextRange::new(TextSize::from(start), TextSize::from(end));
        let mut builder = Builder::new(fix.document.source());
        builder.replace_range(
            range(payload.criteria_start, payload.criteria_end),
            format!("new Criteria({})", payload.argument),
        )?;
        let (filter_start, filter_end) = statement_line(
            fix.document.source(),
            payload.filter_start as usize,
            payload.filter_end as usize,
        );
        builder.replace_range(range(filter_start, filter_end), "")?;
        let edits = builder.finish()?;
        Ok(single_document_plan(&fix.document, edits))
    }
}

/// Widens a removed statement to its whole line, including the line break,
/// when nothing else shares the line, so no indentation-only line remains.
fn statement_line(source: &str, start: usize, end: usize) -> (u32, u32) {
    let bytes = source.as_bytes();
    let line_start = source[..start].rfind('\n').map_or(0, |index| index + 1);
    let line_end = source[end..]
        .find('\n')
        .map_or(bytes.len(), |index| end + index);
    let blank = |text: &str| {
        text.bytes()
            .all(|byte| byte == b' ' || byte == b'\t' || byte == b'\r')
    };
    if line_start == 0 || !blank(&source[line_start..start]) || !blank(&source[end..line_end]) {
        return (start as u32, end as u32);
    }
    let after = if line_end < bytes.len() {
        line_end + 1
    } else {
        line_end
    };
    (line_start as u32, after as u32)
}
