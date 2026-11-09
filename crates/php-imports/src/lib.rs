//! Namespace import analysis and import rewrites (port of
//! `internal/php/imports`).
//!
//! [`analyze`] marks every `use` clause as used or unused from the lexical
//! reference names of a bound document, constant-name expressions, and
//! PHPDoc references. [`remove`] deletes selected clauses while preserving
//! comments; [`organize`] removes unused imports and orders independent,
//! comment-free runs. [`remove_orphaned_imports`] extends a rewrite with the
//! removal of the imports it leaves unused.

mod analyze;
mod orphans;
mod rewrite;

#[cfg(test)]
mod tests;

pub use analyze::{Analysis, Declaration, Item, Scope, analyze};
pub use orphans::remove_orphaned_imports;
pub use rewrite::{organize, remove};

/// The operation was cancelled by its caller.
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub struct Cancelled;

impl std::fmt::Display for Cancelled {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        f.write_str("import analysis cancelled")
    }
}

impl std::error::Error for Cancelled {}

/// Errors from [`organize`].
#[derive(Clone, Debug, PartialEq, Eq)]
pub enum OrganizeError {
    Cancelled,
    Rewrite(swlsp_rewrite::RewriteError),
}

impl std::fmt::Display for OrganizeError {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        match self {
            OrganizeError::Cancelled => Cancelled.fmt(f),
            OrganizeError::Rewrite(error) => error.fmt(f),
        }
    }
}

impl std::error::Error for OrganizeError {}

impl From<swlsp_rewrite::RewriteError> for OrganizeError {
    fn from(error: swlsp_rewrite::RewriteError) -> Self {
        OrganizeError::Rewrite(error)
    }
}

impl From<Cancelled> for OrganizeError {
    fn from(_: Cancelled) -> Self {
        OrganizeError::Cancelled
    }
}
