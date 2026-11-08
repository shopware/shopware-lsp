//! PHP refactorings built against immutable source snapshots (port of Go
//! `internal/php/refactoring`).
//!
//! Every refactoring returns validated byte edits for the exact source it
//! was given; nothing scans the workspace. `Ok(None)` means the refactoring
//! is unavailable for the selection.

mod extract_method;
mod extract_occurrences;
mod extract_variable;
mod extraction_flow;
mod extraction_reindent;
mod extraction_returns;
mod extraction_scope;

#[cfg(test)]
mod tests;

pub use extract_method::{MethodNameAvailable, extract_method};
pub use extract_variable::{VariableOptions, extract_variable};

use swlsp_rewrite::{Edit, RewriteError};
use swlsp_syntax::{SyntaxNode, SyntaxToken};

/// The edits of one extraction and the name it introduced.
#[derive(Clone, Debug, Default, PartialEq, Eq)]
pub struct Extraction {
    pub occurrences: usize,
    pub name: String,
    pub edits: Vec<Edit>,
}

#[derive(Clone, Debug, PartialEq, Eq, thiserror::Error)]
pub enum RefactorError {
    /// The caller's cancellation signal fired (Go `context.Canceled`).
    #[error("operation cancelled")]
    Cancelled,
    #[error("return statement has no stable boundaries")]
    UnstableReturn,
    #[error(transparent)]
    Rewrite(#[from] RewriteError),
}

pub type Result<T, E = RefactorError> = std::result::Result<T, E>;

/// Polled in non-trivial loops; returns `true` once the request is cancelled.
pub type Cancelled<'a> = &'a dyn Fn() -> bool;

fn check(cancelled: Cancelled<'_>) -> Result<()> {
    if cancelled() {
        Err(RefactorError::Cancelled)
    } else {
        Ok(())
    }
}

fn child_tokens(node: &SyntaxNode) -> impl Iterator<Item = SyntaxToken> + use<> {
    node.children_with_tokens()
        .filter_map(|element| element.into_token())
}

fn descendant_tokens(node: &SyntaxNode) -> impl Iterator<Item = SyntaxToken> + use<> {
    node.descendants_with_tokens()
        .filter_map(|element| element.into_token())
}

fn newline_of(source: &str) -> &'static str {
    if source.contains("\r\n") {
        "\r\n"
    } else {
        "\n"
    }
}

/// The white-space indentation before `offset` on its line; empty when the
/// line has other content before `offset`.
fn source_indent(source: &str, offset: u32) -> &str {
    let offset = offset as usize;
    let start = source[..offset]
        .rfind('\n')
        .map_or(0, |newline| newline + 1);
    let indent = &source[start..offset];
    if indent.trim().is_empty() { indent } else { "" }
}
