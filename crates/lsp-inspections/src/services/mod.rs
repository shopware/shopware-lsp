//! Symfony service-argument, routing/controller, event and console inspections.

mod controller;
mod event;
mod invokable_command;
mod php_method;
mod service_argument;
mod service_argument_rewrite;

#[cfg(test)]
mod tests;

pub use controller::{CONTROLLER_METHOD_FIX_ID, new_controller};
pub use event::{EVENT_METHOD_FIX_ID, new_event};
pub use invokable_command::{INVOKABLE_RETURN_TYPE_FIX_ID, new_invokable_command};
pub use php_method::{PhpMethodFix, PhpMethodParameter, PhpMethodPayload};
pub use service_argument::{SERVICE_ARGUMENTS_FIX_ID, SERVICE_METHOD_FIX_ID, new_service_argument};

use serde_json::{Map, Value};
use swlsp_lsp::{DiagnosticId, ProblemDefinition};
use swlsp_lsp_protocol::DiagnosticSeverity;
use swlsp_syntax::{SyntaxElement, SyntaxNode};

pub(crate) fn map_string(values: &Map<String, Value>, key: &str) -> String {
    values
        .get(key)
        .and_then(Value::as_str)
        .unwrap_or_default()
        .to_owned()
}

/// The non-empty strings of a JSON array.
pub(crate) fn map_strings(values: &Map<String, Value>, key: &str) -> Vec<String> {
    let Some(Value::Array(items)) = values.get(key) else {
        return Vec::new();
    };
    items
        .iter()
        .filter_map(Value::as_str)
        .filter(|value| !value.is_empty())
        .map(str::to_owned)
        .collect()
}

pub(crate) fn definition(id: &str, severity: DiagnosticSeverity) -> ProblemDefinition {
    ProblemDefinition {
        id: DiagnosticId::from(id),
        source: "symfony".to_owned(),
        default_severity: severity,
        disabled_by_default: false,
    }
}

/// The element itself or its nearest ancestor node of `kind`.
pub(crate) fn ancestor_node(
    element: &SyntaxElement,
    kind: swlsp_syntax::Kind,
) -> Option<SyntaxNode> {
    let start = match element {
        SyntaxElement::Node(node) => Some(node.clone()),
        SyntaxElement::Token(token) => token.parent(),
    };
    start.and_then(|node| node.ancestors().find(|node| node.kind() == kind))
}
