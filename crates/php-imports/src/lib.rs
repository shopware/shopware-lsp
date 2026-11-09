//! Namespace import analysis and import rewrites.
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
#[derive(Clone, Copy, Debug, PartialEq, Eq, thiserror::Error)]
#[error("import analysis cancelled")]
pub struct Cancelled;

/// Errors from [`organize`].
#[derive(Clone, Debug, PartialEq, Eq, thiserror::Error)]
pub enum OrganizeError {
    #[error("import analysis cancelled")]
    Cancelled,
    #[error(transparent)]
    Rewrite(#[from] swlsp_rewrite::RewriteError),
}

impl From<Cancelled> for OrganizeError {
    fn from(_: Cancelled) -> Self {
        OrganizeError::Cancelled
    }
}
