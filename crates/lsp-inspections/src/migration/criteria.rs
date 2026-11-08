//! The `shopware.criteria` inspection (Go `shopware_criteria.go`).

use std::sync::Arc;

use serde::Deserialize;
use serde_json::{Map, Value};
use swlsp_language::LanguageId;
use swlsp_lsp::{
    BoundFix, BoxError, Context, DiagnosticId, FixContext, FixId, FixPresentation, Inspection,
    InspectionDefinition, ProblemDefinition, QuickFix, RewriteQuickFix, bind_fix,
};
use swlsp_lsp_diagnostics::ShopwareCriteriaAnalyzer;
use swlsp_lsp_protocol as protocol;
use swlsp_rewrite::{Builder, WorkspacePlan};
use swlsp_syntax::{TextRange, TextSize};

use super::{invalid, quick_fix_presentation, single_document_plan};
use crate::inspection::{BindProblems, BoundInspection};
use crate::resolve_anchor;

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

/// Go `NewShopwareCriteria`.
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
        builder.replace_range(range(payload.filter_start, payload.filter_end), "")?;
        let edits = builder.finish()?;
        Ok(single_document_plan(&fix.document, edits))
    }
}
