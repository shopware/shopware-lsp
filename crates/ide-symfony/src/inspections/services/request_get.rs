//! The `symfony.request` inspection.

use std::sync::Arc;

use serde_json::Value;
use swlsp_language::LanguageId;
use swlsp_lsp::{Inspection, InspectionDefinition, InspectionScope};
use swlsp_lsp_protocol::DiagnosticSeverity;
use swlsp_php::PhpIndex;

use super::definition;
use crate::diagnostics::{REQUEST_GET_CODE, RequestGetAnalyzer};
use crate::inspections::{BoundInspection, RewriteNodeFix, rewrite_node_fix};

/// Where a request value can be read from, with what the fix is called.
const SOURCES: [(&str, &str); 3] = [
    ("attributes", "Read the route attribute"),
    ("query", "Read the query string"),
    ("request", "Read the request body"),
];

pub fn new_request_get(php_index: Option<PhpIndex>) -> Arc<dyn Inspection> {
    Arc::new(BoundInspection {
        definition: InspectionDefinition {
            id: "symfony.request".to_owned(),
            description: "Calls of Request::get(), which Symfony 7.4 deprecates: the value is read from the route attributes, the query string or the body, whichever has it first.",
            domain: "symfony",
            scope: InspectionScope::Framework,
            languages: vec![LanguageId::Php],
            problems: vec![definition(REQUEST_GET_CODE, DiagnosticSeverity::WARNING)],
        },
        analyzers: vec![Arc::new(RequestGetAnalyzer::new(php_index))],
        fixes: vec![Arc::new(RewriteNodeFix)],
        bind: Some(Arc::new(|_, payload| {
            let Some(Value::String(method)) = payload.get("method") else {
                return Vec::new();
            };
            // A key the route has in its path is an attribute: get() finds
            // it there first, so that rewrite changes nothing. Any other
            // key may come from either place, and a person has to say.
            let route_parameter = payload.get("routeParameter") == Some(&Value::Bool(true));
            SOURCES
                .iter()
                .map(|(bag, title)| {
                    rewrite_node_fix(
                        format!("{title}: ->{bag}->get()"),
                        method.as_str(),
                        format!("{bag}->{method}"),
                        route_parameter && *bag == "attributes",
                    )
                })
                .collect()
        })),
    })
}
