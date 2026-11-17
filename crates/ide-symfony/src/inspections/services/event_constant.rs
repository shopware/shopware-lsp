//! The `symfony.event.constant` inspection.

use std::sync::Arc;

use swlsp_language::LanguageId;
use swlsp_lsp::{Inspection, InspectionDefinition, InspectionScope};
use swlsp_lsp_protocol::DiagnosticSeverity;
use swlsp_php::PhpIndex;

use super::definition;
use crate::diagnostics::{EVENT_NAME_LITERAL_CODE, EventConstantAnalyzer};
use crate::inspections::{BoundInspection, ClassConstantFix, class_constant_bound_fixes};

pub fn new_event_constant(php_index: Option<PhpIndex>) -> Arc<dyn Inspection> {
    Arc::new(BoundInspection {
        definition: InspectionDefinition {
            id: "symfony.event.constant".to_owned(),
            description: "Event subscribers and listeners naming an event by a string where a class constant, or the event class, holds the name.",
            domain: "symfony.events",
            scope: InspectionScope::Framework,
            languages: vec![LanguageId::Php],
            problems: vec![definition(
                EVENT_NAME_LITERAL_CODE,
                DiagnosticSeverity::HINT,
            )],
        },
        analyzers: vec![Arc::new(EventConstantAnalyzer::new(php_index))],
        fixes: vec![Arc::new(ClassConstantFix)],
        bind: Some(Arc::new(|_, payload| class_constant_bound_fixes(payload))),
    })
}
