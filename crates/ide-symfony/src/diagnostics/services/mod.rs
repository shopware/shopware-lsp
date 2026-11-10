//! Symfony service, routing, controller, event and console diagnostics
//! analyzers.

mod container_constant;
mod controller;
mod duplicate;
mod event;
mod invokable_command;
mod route;
mod route_redefined;
mod service;
mod service_argument;
mod tagged_service;

#[cfg(test)]
mod tests;

pub use container_constant::ContainerConstantAnalyzer;
pub use controller::ControllerAnalyzer;
pub use duplicate::DuplicateAnalyzer;
pub use event::EventAnalyzer;
pub use invokable_command::{
    INVOKABLE_COMMAND_RETURN_TYPE_MESSAGE, INVOKABLE_COMMAND_RETURN_VALUE_MESSAGE,
    InvokableCommandAnalyzer,
};
pub use route::RouteAnalyzer;
pub use route_redefined::{
    ROUTE_REDEFINED_CODE, RouteDefinitions, RouteLocation, RouteRedefinedAnalysis,
};
pub use service::ServiceAnalyzer;
pub use service_argument::ServiceArgumentAnalyzer;
pub use tagged_service::TaggedServiceAnalyzer;

use serde_json::Value;
use swlsp_lsp::{DiagnosticId, Problem};
use swlsp_lsp_protocol as protocol;
use swlsp_syntax::TextRange;

/// The extension of the last path element (from its final `.`), lowercased with
/// ASCII rules (enough for extension comparisons).
pub(crate) fn lower_ext(path: &str) -> String {
    for (index, byte) in path.bytes().enumerate().rev() {
        if byte == b'/' {
            break;
        }
        if byte == b'.' {
            return path[index..].to_ascii_lowercase();
        }
    }
    String::new()
}

/// The similar candidates as a JSON array (`[]` when none scores).
pub(crate) fn similar_value<S: AsRef<str>>(input: &str, candidates: &[S]) -> Value {
    Value::from(swlsp_suggestion::similar(input, candidates))
}

pub(crate) fn problem(
    id: &str,
    range: TextRange,
    message: String,
    severity: protocol::DiagnosticSeverity,
) -> Problem {
    Problem {
        id: DiagnosticId::from(id),
        range,
        message,
        severity: Some(severity),
        source: "symfony".to_owned(),
        ..Problem::default()
    }
}

pub(crate) fn deprecated_tags() -> Vec<protocol::DiagnosticTag> {
    vec![protocol::DiagnosticTag::DEPRECATED]
}
