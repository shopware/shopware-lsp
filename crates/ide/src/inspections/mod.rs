//! Diagnostic inspections.
//!
//! An inspection declares its languages and every diagnostic code it can emit,
//! runs one or more [`ProblemAnalyzer`]s from [`crate::diagnostics`] and binds
//! quick fixes to each problem while it is reported.

pub mod inspection;
pub mod range;
pub mod suggestion;

pub use crate::diagnostics::{DeclaredAnalyzer, ProblemAnalyzer};
pub use inspection::{
    AnalyzerInspection, BindProblems, BoundInspection, UndeclaredProblemError, analyzer_inspection,
    diagnostic_payload, new_analyzer_inspection, resolve_anchor,
};
pub use range::{InvertedRangeError, protocol_text_range};
pub use suggestion::{SUGGESTION_FIX_ID, SuggestionFix, suggestion_bound_fixes};

#[cfg(any(test, feature = "testing"))]
pub mod test_support;
#[cfg(test)]
mod tests;
