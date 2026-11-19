//! The `shopware.admin.api_contract` inspection.

use std::sync::Arc;

use serde::{Deserialize, Serialize};
use serde_json::{Map, Value};
use swlsp_language::LanguageId;
use swlsp_lsp::{
    BoundFix, BoxError, Context, DiagnosticId, FixContext, FixId, FixPresentation, FixResolution,
    Inspection, InspectionDefinition, InspectionScope, ProblemDefinition, QuickFix,
    RewriteQuickFix, bind_fix,
};
use swlsp_lsp_protocol as protocol;
use swlsp_php::PhpIndex;
use swlsp_rewrite::{Builder, DocumentPlan, HandleError, WorkspacePlan};
use swlsp_symfony::RouteIndexer;

use crate::diagnostics::{ADMIN_API_CONTRACT_CODES, AdminApiContractAnalyzer};
use crate::inspections::inspection::{BindProblems, BoundInspection};
use crate::inspections::{RewriteNodeFix, resolve_anchor, rewrite_node_bound_fixes};

pub const ADD_API_TYPE_MEMBER_FIX_ID: &str = "admin-api-add-type-member";

/// A member to add to the declared type of a request: `insert` goes after
/// the byte `insert_at`, which follows `expected`.
#[derive(Debug, Clone, Default, Serialize, Deserialize)]
#[serde(rename_all = "camelCase", default)]
struct AddTypeMemberPayload {
    title: String,
    insert_at: Option<u32>,
    expected: String,
    insert: String,
}

/// Adds the key the controller reads to the type the request is declared
/// with. The type is elsewhere in the file than the reported request.
struct AddTypeMemberFix;

impl QuickFix for AddTypeMemberFix {
    fn id(&self) -> FixId {
        FixId::from(ADD_API_TYPE_MEMBER_FIX_ID)
    }

    fn present(&self, _: &Context, fix: &FixContext) -> Result<Option<FixPresentation>, BoxError> {
        let payload: AddTypeMemberPayload = fix.fix_payload()?;
        if payload.title.is_empty() || payload.insert.is_empty() {
            return Ok(None);
        }
        Ok(Some(FixPresentation {
            title: payload.title,
            kind: Some(protocol::CodeActionKind::QUICKFIX),
            preferred: false,
            resolution: FixResolution::Eager,
        }))
    }

    fn as_rewrite(&self) -> Option<&dyn RewriteQuickFix> {
        Some(self)
    }
}

impl RewriteQuickFix for AddTypeMemberFix {
    fn build(&self, _: &Context, fix: &FixContext) -> Result<WorkspacePlan, BoxError> {
        let payload: AddTypeMemberPayload = fix.fix_payload()?;
        resolve_anchor(fix)?;
        let document = &fix.document;
        let source = document.source();
        let offset = payload.insert_at.ok_or(HandleError::Stale)?;
        let before = source.get(..offset as usize).ok_or(HandleError::Stale)?;
        if payload.expected.is_empty() || !before.ends_with(&payload.expected) {
            return Err(Box::new(HandleError::Stale));
        }
        let mut builder = Builder::new(source);
        builder.insert(offset, payload.insert)?;
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

/// Administration requests and response types, checked against the
/// controller that answers the route.
pub fn new_admin_api_contract(
    routes: Option<Arc<RouteIndexer>>,
    php: Option<PhpIndex>,
) -> Arc<dyn Inspection> {
    let bind: BindProblems = Arc::new(
        |_: &DiagnosticId, payload: &Map<String, Value>| -> Vec<BoundFix> {
            if payload.contains_key("insertAt") {
                let value: AddTypeMemberPayload =
                    serde_json::from_value(Value::Object(payload.clone())).unwrap_or_default();
                return vec![bind_fix(ADD_API_TYPE_MEMBER_FIX_ID, value)];
            }
            rewrite_node_bound_fixes(payload)
        },
    );
    Arc::new(BoundInspection {
        definition: InspectionDefinition {
            id: "shopware.admin.api_contract".to_owned(),
            description: "Administration requests to an Admin API action that send a key the controller does not read or leave out one it reads, and service methods whose declared response type differs from what the controller answers.",
            domain: "administration",
            scope: InspectionScope::Framework,
            languages: vec![LanguageId::JavaScript],
            problems: ADMIN_API_CONTRACT_CODES
                .iter()
                .map(|code| ProblemDefinition {
                    id: DiagnosticId::from(*code),
                    source: "shopware-lsp".to_owned(),
                    default_severity: protocol::DiagnosticSeverity::WARNING,
                    disabled_by_default: false,
                })
                .collect(),
        },
        analyzers: vec![Arc::new(AdminApiContractAnalyzer::new(routes, php))],
        fixes: vec![Arc::new(RewriteNodeFix), Arc::new(AddTypeMemberFix)],
        bind: Some(bind),
    })
}
