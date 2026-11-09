//! Binds a parsed PHP file into an immutable semantic [`Document`].
//!
//! The binder walks the lossless CST once and produces declarations
//! (classes, members, functions, parameters, locals, closures, PHPDoc
//! synthesized members), lexical scopes, unresolved name references, and
//! declared type facts. Cross-file IDs are resolved afterwards by [`link`]
//! against one workspace [`Snapshot`](swlsp_php_semantic::Snapshot).
//!
//! # Imports and ordering
//!
//! - Every scope of a namespace sees the namespace's whole import table, so
//!   a scope created before a later `use` statement still observes it. The
//!   binder tracks which shared import map each scope uses and materializes
//!   each scope's [`ImportTable`] once at the end of binding.
//! - PHPDoc `@phpstan-type` aliases are kept sorted, so synthesized
//!   type-alias symbols are emitted in name order.
//!
//! [`ImportTable`]: swlsp_php_semantic::ImportTable

mod body;
mod builder;
mod capacity;
mod declaration_references;
mod enums;
mod fast;
mod linker;
mod modifiers;
mod phpdoc;
mod planned_parameters;
mod trait_alias;

#[cfg(test)]
mod tests;

use swlsp_php_semantic::Document;
use swlsp_syntax::{SyntaxNode, Tree};

pub use builder::effective_type;
pub use linker::{link, link_owned};

/// Converts PHP syntax trees into semantic documents. Stateless.
#[derive(Clone, Copy, Debug, Default)]
pub struct Binder;

impl Binder {
    pub fn new() -> Self {
        Binder
    }

    /// Binds the root of `tree`.
    pub fn bind(&self, path: &str, version: i64, tree: &Tree) -> Document {
        builder::bind(path, version, Some(&tree.root()), tree.source())
    }

    /// Binds `root`, whose byte ranges index `source` (the complete text of
    /// the tree `root` belongs to). `None` yields an empty document.
    pub fn bind_node(
        &self,
        path: &str,
        version: i64,
        root: Option<&SyntaxNode>,
        source: &str,
    ) -> Document {
        builder::bind(path, version, root, source)
    }
}
