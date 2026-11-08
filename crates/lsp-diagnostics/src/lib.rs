//! Domain diagnostics (port of Go `internal/lsp/diagnostics`).
//!
//! Every analyzer implements [`ProblemAnalyzer`]: it walks the document's
//! syntax tree through the language query helpers and returns byte-oriented
//! [`Problem`]s. Analyzers know nothing about the reporter, the diagnostic
//! envelope or protocol conversion; `swlsp-lsp-inspections` declares the
//! inspections that own their codes and binds quick fixes.

use std::sync::Arc;

use swlsp_lsp::{BoxError, Context, Problem, TextDocument};

pub mod common;

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

/// Domain analysis producing byte-oriented problems (Go
/// `inspections.ProblemAnalyzer`).
pub trait ProblemAnalyzer: Send + Sync {
    fn analyze(
        &self,
        ctx: &Context,
        document: &Arc<TextDocument>,
    ) -> Result<Vec<Problem>, BoxError>;
}
