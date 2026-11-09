//! Go `event.go`: the `symfony.event` inspection.

use std::sync::Arc;

use swlsp_event::Index;
use swlsp_language::LanguageId;
use swlsp_lsp::{Inspection, InspectionDefinition, InspectionScope, bind_fix};
use swlsp_lsp_diagnostics::EventAnalyzer;
use swlsp_lsp_protocol::DiagnosticSeverity;
use swlsp_php::PhpIndex;
use swlsp_symfony::ServiceIndex;

use super::{
    PhpMethodFix, PhpMethodParameter, PhpMethodPayload, definition, map_string, map_strings,
};
use crate::{BoundInspection, SuggestionFix, suggestion_bound_fixes};

pub const EVENT_METHOD_FIX_ID: &str = "create-event-listener-method";

/// Go `NewEvent`.
pub fn new_event(
    index: Option<Arc<Index>>,
    php_index: Option<PhpIndex>,
    service_index: Option<Arc<ServiceIndex>>,
) -> Arc<dyn Inspection> {
    let method_fix = PhpMethodFix {
        id: EVENT_METHOD_FIX_ID,
        title_prefix: "event listener",
        php_index: php_index.clone(),
    };
    Arc::new(BoundInspection {
        definition: InspectionDefinition {
            id: "symfony.event".to_owned(),
            description: "Dispatched events nobody listens to, and listener methods that do not exist.",
            domain: "symfony.events",
            scope: InspectionScope::Framework,
            languages: vec![LanguageId::Php, LanguageId::Xml, LanguageId::Yaml],
            problems: vec![
                definition(
                    "symfony.event.listener_method.missing",
                    DiagnosticSeverity::WARNING,
                ),
                definition("symfony.event.missing", DiagnosticSeverity::WARNING),
            ],
        },
        analyzers: vec![Arc::new(EventAnalyzer::new(
            index,
            php_index,
            service_index,
        ))],
        fixes: vec![Arc::new(SuggestionFix), Arc::new(method_fix)],
        bind: Some(Arc::new(|code, payload| {
            let mut bound = suggestion_bound_fixes(payload);
            if code.as_str() != "symfony.event.listener_method.missing" {
                return bound;
            }
            let mut method = PhpMethodPayload {
                class_name: map_string(payload, "className"),
                method_name: map_string(payload, "methodName"),
                ..PhpMethodPayload::default()
            };
            let types = map_strings(payload, "eventTypes");
            if !types.is_empty() {
                method.typed_parameters = vec![PhpMethodParameter {
                    name: "event".to_owned(),
                    types,
                }];
            }
            if method.class_name.is_empty() || method.method_name.is_empty() {
                return bound;
            }
            bound.push(bind_fix(EVENT_METHOD_FIX_ID, method));
            bound
        })),
    })
}
