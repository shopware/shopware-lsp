//! Local expression and control-flow type analysis over bound PHP semantic
//! documents (port of `internal/php/inference`).
//!
//! [`Analyzer`] enriches a bound [`Document`] with type facts, inferred
//! function returns, local variable types, and diagnostics. A bounded
//! fixed-point pass makes inferred returns available to calls in the same
//! document. [`Extension`]s refine call results for framework-specific APIs
//! without coupling the evaluator to Shopware or Symfony.
//! [`link_members`] resolves member references once receiver types are
//! known.
//!
//! # Module map
//!
//! | Go file | Module |
//! |---|---|
//! | `analyzer.go` (state, statements, fixed point) | `analyzer` |
//! | `analyzer.go` (environment) | `env` |
//! | `helpers.go`, `flow_helpers.go` (syntax) | `syntax` |
//! | `helpers.go` (types), shared type predicates | `types_util` |
//! | `declarations.go` | `declarations` |
//! | `expression_*.go` | `expr` |
//! | `flow_*.go` | `flow` |
//! | `by_reference.go` | `by_reference` |
//! | `extensions.go`, `builtins.go`, `call_contracts.go`, `attribute_contracts.go`, `faker.go` | `extensions` |
//! | `member_links.go` | `member_links` |
//!
//! # Differences from Go
//!
//! - The environment is an `Rc` copy-on-write frame instead of Go's
//!   override/detach handle; observable semantics (fork isolation, shared
//!   handles while evaluating one expression) are identical.
//! - Extensions are values implementing [`Extension`]; Go's package-level
//!   `Builtins`, `CallContracts`, `AttributeContracts` and `FakerTypes`
//!   variables are the unit structs [`Builtins`], [`CallContracts`],
//!   [`AttributeContracts`] and [`FakerTypes`].
//! - [`CallContext`] carries the analyzed source text so extensions can read
//!   argument text without allocating.
//! - Go's `AnalyzeOwned` runs against an overlay snapshot that aliases the
//!   owned document's symbol slice, so a return type inferred for one
//!   function is visible to later functions of the same pass.
//!   [`Analyzer::analyze_owned`] reproduces this by overriding each changed
//!   return in its snapshot (in place, via [`Snapshot::override_symbol`]);
//!   [`Analyzer::analyze`] (Go `Analyze`, which clones) publishes them only
//!   between passes.
//!
//! [`Snapshot::override_symbol`]: swlsp_php_semantic::Snapshot::override_symbol
//! - [`link_members`] reads local variable types from the analyzed document
//!   (Go reads them through that aliased overlay). Go loses the alias when a
//!   by-reference output appends a local and the slice reallocates; Rust
//!   always uses the current document, which resolves a few more receivers.

mod analyzer;
mod by_reference;
mod declarations;
mod env;
mod expr;
mod extensions;
mod flow;
mod member_links;
mod syntax;
mod types_util;

#[cfg(test)]
mod tests;

pub use analyzer::Analyzer;
pub use extensions::{
    AttributeContracts, Builtins, CallArgument, CallContext, CallContracts, Extension, FakerTypes,
};
pub use member_links::{link_members, link_members_owned};

#[doc(hidden)]
pub use swlsp_php_semantic::Document;
