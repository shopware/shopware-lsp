//! The `shopware.storefront_javascript` inspection.

use std::sync::Arc;

use serde_json::{Map, Value};
use swlsp_language::LanguageId;
use swlsp_lsp::{
    BoundFix, DiagnosticId, Inspection, InspectionDefinition, InspectionScope, ProblemDefinition,
};
use swlsp_lsp_protocol as protocol;
use swlsp_shopware::ResolvedVersion;

use crate::diagnostics::{STOREFRONT_JAVASCRIPT_CODES, StorefrontJavaScriptAnalyzer};
use crate::inspections::inspection::{BindProblems, BoundInspection};
use crate::inspections::{RewriteNodeFix, rewrite_node_bound_fixes};

pub fn new_storefront_javascript(version: ResolvedVersion) -> Arc<dyn Inspection> {
    let bind: BindProblems = Arc::new(
        |_: &DiagnosticId, payload: &Map<String, Value>| -> Vec<BoundFix> {
            rewrite_node_bound_fixes(payload)
        },
    );
    Arc::new(BoundInspection {
        definition: InspectionDefinition {
            id: "shopware.storefront_javascript".to_owned(),
            description: "Storefront JavaScript importing the plugin system instead of using its globals, and from Shopware 6.7 the deprecated DomAccess and HttpClient helpers and the query-string package.",
            domain: "shopware",
            scope: InspectionScope::Framework,
            languages: vec![LanguageId::JavaScript],
            problems: STOREFRONT_JAVASCRIPT_CODES
                .iter()
                .map(|code| ProblemDefinition {
                    id: DiagnosticId::from(*code),
                    source: "shopware-lsp".to_owned(),
                    default_severity: protocol::DiagnosticSeverity::WARNING,
                    disabled_by_default: false,
                })
                .collect(),
        },
        analyzers: vec![Arc::new(StorefrontJavaScriptAnalyzer::new(version))],
        fixes: vec![Arc::new(RewriteNodeFix)],
        bind: Some(bind),
    })
}
