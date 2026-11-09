//! Domain diagnostics.
//!
//! Every analyzer implements [`ProblemAnalyzer`]: it walks the document's
//! syntax tree through the language query helpers and returns byte-oriented
//! [`Problem`]s. Analyzers know nothing about the reporter, the diagnostic
//! envelope or protocol conversion. An analyzer that forms an inspection on its
//! own declares it through [`DeclaredAnalyzer`]; a rule about the whole project
//! is a [`ProjectAnalysis`] run through [`Project`]. `swlsp-lsp-inspections`
//! wraps analyzers into inspections and binds quick fixes.

use std::sync::Arc;

pub use swlsp_indexer::Revisioned;
use swlsp_lsp::{BoxError, Context, InspectionSpec, Problem, TextDocument};

pub mod common;

mod error;
pub use error::LookupError;

mod admin;
pub use admin::*;

mod migration;
pub use migration::*;

mod project;
pub use project::{Project, ProjectAnalysis, SummaryHandle};

mod shared;
pub use shared::{CodeGroup, SharedAnalysis};

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
mod tests_project;

#[cfg(test)]
mod tests_shared;

/// Domain analysis producing byte-oriented problems.
pub trait ProblemAnalyzer: Send + Sync {
    fn analyze(
        &self,
        ctx: &Context,
        document: &Arc<TextDocument>,
    ) -> Result<Vec<Problem>, BoxError>;

    /// The combined revision of everything besides `document` that
    /// [`Self::analyze`] reads for it: the sum of [`inputs`] of the indexes it
    /// holds, `Some(0)` for document-only analyzers. While it and the
    /// document are unchanged, a refresh reuses the previous problems
    /// ([`swlsp_lsp::Inspection::input_revision`]).
    ///
    /// Declare it beside the fields the analyzer reads, and only when it is
    /// complete: an index, configuration or file read from disk left out
    /// leaves stale diagnostics. `None`, the default, runs the analyzer on
    /// every refresh. `crates/app/src/tests/inspection_inputs_test.rs`
    /// audits the declarations.
    fn input_revision(&self, _document: &swlsp_lsp::TextDocument) -> Option<u64> {
        None
    }

    /// See [`swlsp_lsp::Inspection::workspace_paths`].
    fn workspace_paths(&self, _ctx: &Context) -> Vec<String> {
        Vec::new()
    }
}

/// The revision of an index an analyzer holds, for
/// [`ProblemAnalyzer::input_revision`]; `0` for an absent index.
pub fn inputs(index: &impl Revisioned) -> u64 {
    index.revision()
}

/// An analyzer that forms one inspection on its own and declares it beside
/// its implementation: ID, configuration domain, languages, source and every
/// code it reports.
pub trait DeclaredAnalyzer: ProblemAnalyzer {
    const INSPECTION: InspectionSpec;
}
