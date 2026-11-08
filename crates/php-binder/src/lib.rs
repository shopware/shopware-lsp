//! Binds a parsed PHP file into an immutable semantic [`Document`]
//! (port of `internal/php/binder`).
//!
//! The binder walks the lossless CST once and produces declarations
//! (classes, members, functions, parameters, locals, closures, PHPDoc
//! synthesized members), lexical scopes, unresolved name references, and
//! declared type facts. Cross-file IDs are resolved afterwards by [`link`]
//! against one workspace [`Snapshot`](swlsp_php_semantic::Snapshot).
//!
//! # Differences from Go
//!
//! - Go scopes share their namespace's import maps by reference, so a scope
//!   created before a later `use` statement still observes it. The Rust
//!   binder reproduces the resulting tables exactly by tracking map
//!   identities and materializing each scope's [`ImportTable`] once at the
//!   end of binding.
//! - Go iterates PHPDoc `@phpstan-type` aliases in random map order; the
//!   Rust PHPDoc parser keeps them sorted, so synthesized type-alias symbols
//!   are emitted in name order.
//! - The declaration set is a plain hash set of node identities instead of
//!   Go's hand-rolled open-addressing table.
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
