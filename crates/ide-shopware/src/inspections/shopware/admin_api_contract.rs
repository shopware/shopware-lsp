//! The `shopware.admin.api_contract` inspection.

use std::sync::Arc;

use serde_json::{Map, Value};
use swlsp_language::LanguageId;
use swlsp_lsp::{
    BoundFix, DiagnosticId, Inspection, InspectionDefinition, InspectionScope, ProblemDefinition,
};
use swlsp_lsp_protocol as protocol;
use swlsp_php::PhpIndex;
use swlsp_symfony::RouteIndexer;

use crate::diagnostics::{ADMIN_API_CONTRACT_CODES, AdminApiContractAnalyzer};
use crate::inspections::inspection::{BindProblems, BoundInspection};
use crate::inspections::{RewriteNodeFix, rewrite_node_bound_fixes};

/// Administration requests and response types, checked against the
/// controller that answers the route.
pub fn new_admin_api_contract(
    routes: Option<Arc<RouteIndexer>>,
    php: Option<PhpIndex>,
) -> Arc<dyn Inspection> {
    let bind: BindProblems = Arc::new(
        |_: &DiagnosticId, payload: &Map<String, Value>| -> Vec<BoundFix> {
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
        fixes: vec![Arc::new(RewriteNodeFix)],
        bind: Some(bind),
    })
}
