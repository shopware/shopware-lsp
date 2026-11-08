use std::collections::HashSet;

use serde::{Deserialize, Serialize};
use serde_json::{Map, Value};
use swlsp_lsp::{
    BoundFix, BoxError, Context, FixContext, FixId, FixPresentation, FixResolution, QuickFix,
    RewriteQuickFix, bind_fix,
};
use swlsp_lsp_protocol as protocol;
use swlsp_rewrite::{Builder, DocumentPlan, WorkspacePlan};

use crate::inspection::resolve_anchor;
use crate::range::protocol_text_range;

pub const SUGGESTION_FIX_ID: &str = "replace-with-suggestion";

#[derive(Debug, Clone, Default, Serialize, Deserialize)]
struct SuggestionPayload {
    #[serde(default)]
    replacement: String,
}

/// Replaces the diagnostic range with one suggested value (Go
/// `suggestionFix`).
#[derive(Debug, Clone, Copy, Default)]
pub struct SuggestionFix;

impl QuickFix for SuggestionFix {
    fn id(&self) -> FixId {
        FixId::from(SUGGESTION_FIX_ID)
    }

    fn present(
        &self,
        _ctx: &Context,
        fix: &FixContext,
    ) -> Result<Option<FixPresentation>, BoxError> {
        let payload: SuggestionPayload = fix.fix_payload()?;
        if payload.replacement.is_empty() {
            return Ok(None);
        }
        Ok(Some(FixPresentation {
            title: format!("Replace with '{}'", payload.replacement),
            kind: Some(protocol::CodeActionKind::QUICKFIX),
            preferred: true,
            resolution: FixResolution::Eager,
        }))
    }

    fn as_rewrite(&self) -> Option<&dyn RewriteQuickFix> {
        Some(self)
    }
}

impl RewriteQuickFix for SuggestionFix {
    fn build(&self, _ctx: &Context, fix: &FixContext) -> Result<WorkspacePlan, BoxError> {
        let payload: SuggestionPayload = fix.fix_payload()?;
        resolve_anchor(fix)?;
        let document = &fix.document;
        let mut builder = Builder::new(document.source());
        builder.replace_range(
            protocol_text_range(document.line_index(), fix.diagnostic.range)?,
            payload.replacement,
        )?;
        let edits = builder.finish()?;
        Ok(WorkspacePlan {
            documents: vec![
                DocumentPlan::new(
                    document.uri.clone(),
                    Some(document.version),
                    document.source_arc().clone(),
                    edits,
                )
                .with_line_index(document.line_index_arc().clone()),
            ],
            ..WorkspacePlan::default()
        })
    }
}

/// Go `suggestionBoundFixes`: one bound fix per distinct non-blank entry of
/// the payload's `suggestions` array.
pub fn suggestion_bound_fixes(payload: &Map<String, Value>) -> Vec<BoundFix> {
    let Some(Value::Array(values)) = payload.get("suggestions") else {
        return Vec::new();
    };
    let mut seen = HashSet::with_capacity(values.len());
    let mut result = Vec::with_capacity(values.len());
    for value in values {
        let Value::String(value) = value else {
            continue;
        };
        let value = value.trim();
        if value.is_empty() || !seen.insert(value.to_owned()) {
            continue;
        }
        result.push(bind_fix(
            SUGGESTION_FIX_ID,
            SuggestionPayload {
                replacement: value.to_owned(),
            },
        ));
    }
    result
}
