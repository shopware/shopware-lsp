//! Go `controller.go`: the `symfony.routing` inspection.

use std::sync::Arc;

use swlsp_language::LanguageId;
use swlsp_lsp::{Inspection, InspectionDefinition, InspectionScope, bind_fix};
use swlsp_lsp_diagnostics::{ControllerAnalyzer, RouteAnalyzer};
use swlsp_lsp_protocol::DiagnosticSeverity;
use swlsp_php::PhpIndex;
use swlsp_symfony::{RouteIndexer, ServiceIndex};

use super::{PhpMethodFix, PhpMethodPayload, definition, map_string, map_strings};
use crate::{BoundInspection, SuggestionFix, suggestion_bound_fixes};

pub const CONTROLLER_METHOD_FIX_ID: &str = "create-controller-method";

/// Go `NewController`.
pub fn new_controller(
    route_index: Option<Arc<RouteIndexer>>,
    service_index: Option<Arc<ServiceIndex>>,
    php_index: Option<PhpIndex>,
) -> Arc<dyn Inspection> {
    let fix = PhpMethodFix {
        id: CONTROLLER_METHOD_FIX_ID,
        title_prefix: "controller",
        php_index: php_index.clone(),
    };
    Arc::new(BoundInspection {
        definition: InspectionDefinition {
            id: "symfony.routing".to_owned(),
            description: "Route names that do not exist, and route controllers that are missing, lack the method, or are deprecated.",
            domain: "symfony",
            scope: InspectionScope::Framework,
            languages: vec![
                LanguageId::Php,
                LanguageId::Yaml,
                LanguageId::Xml,
                LanguageId::Twig,
            ],
            problems: vec![
                definition(
                    "symfony.controller.target.missing",
                    DiagnosticSeverity::WARNING,
                ),
                definition(
                    "symfony.controller.method.missing",
                    DiagnosticSeverity::WARNING,
                ),
                definition("symfony.controller.deprecated", DiagnosticSeverity::HINT),
                definition("symfony.route.missing", DiagnosticSeverity::ERROR),
            ],
        },
        analyzers: vec![
            Arc::new(ControllerAnalyzer::new(
                service_index.clone(),
                php_index.clone(),
            )),
            Arc::new(RouteAnalyzer::new(route_index, service_index, php_index)),
        ],
        fixes: vec![Arc::new(fix), Arc::new(SuggestionFix)],
        bind: Some(Arc::new(|code, payload| {
            let mut bound = suggestion_bound_fixes(payload);
            if code.as_str() != "symfony.controller.method.missing" {
                return bound;
            }
            let method = PhpMethodPayload {
                class_name: map_string(payload, "className"),
                method_name: map_string(payload, "methodName"),
                parameters: map_strings(payload, "routeParameters"),
                typed_parameters: Vec::new(),
            };
            if method.class_name.is_empty() || method.method_name.is_empty() {
                return bound;
            }
            bound.push(bind_fix(CONTROLLER_METHOD_FIX_ID, method));
            bound
        })),
    })
}
