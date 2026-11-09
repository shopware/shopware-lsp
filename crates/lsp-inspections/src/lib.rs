//! Diagnostic inspections (port of Go `internal/lsp/inspections`).
//!
//! An inspection declares its languages and every diagnostic code it can
//! emit, runs one or more [`ProblemAnalyzer`]s from `swlsp-lsp-diagnostics`
//! and binds quick fixes to each problem while it is reported.

mod inspection;
mod range;
mod suggestion;

pub use inspection::{
    AnalyzerInspection, BindProblems, BoundInspection, analyzer_inspection, diagnostic_payload,
    new_analyzer_inspection, resolve_anchor,
};
pub use range::protocol_text_range;
pub use suggestion::{SUGGESTION_FIX_ID, SuggestionFix, suggestion_bound_fixes};
pub use swlsp_lsp_diagnostics::{DeclaredAnalyzer, ProblemAnalyzer};

mod admin;
pub use admin::*;

mod migration;
pub use migration::*;

mod shopware;
pub use shopware::*;

mod services;
pub use services::*;

mod symfony;
pub use symfony::*;

mod twig;
pub use twig::*;

#[cfg(test)]
mod test_support;
#[cfg(test)]
mod tests;
