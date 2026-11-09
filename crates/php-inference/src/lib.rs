//! Local expression and control-flow type analysis over bound PHP semantic
//! documents.
//!
//! [`Analyzer`] enriches a bound [`Document`] with type facts, inferred
//! function returns, local variable types, and diagnostics. A bounded
//! fixed-point pass makes inferred returns available to calls in the same
//! document. [`Extension`]s refine call results for framework-specific APIs
//! without coupling the evaluator to Shopware or Symfony.
//! [`link_members`] resolves member references once receiver types are
//! known.
//!
//! # Modules
//!
//! | Module | Responsibility |
//! |---|---|
//! | `analyzer` | analysis state, statements, fixed-point scheduling |
//! | `env` | copy-on-write variable frames |
//! | `syntax` | CST helpers shared by expressions and flow |
//! | `types_util` | shared type predicates and joins |
//! | `declarations` | final parents, overrides, abstract methods |
//! | `expr` | expression evaluation |
//! | `flow` | condition narrowing and flow-sensitive updates |
//! | `by_reference` | by-reference argument outputs |
//! | `extensions` | builtin, call-contract, attribute-contract and Faker inference |
//! | `member_links` | member reference linking after inference |
//!
//! # Design notes
//!
//! - An `Env` handle is an `Rc` copy-on-write frame: forks are
//!   isolated, while clones taken during one expression share the frame.
//! - Extensions are values implementing [`Extension`]; the built-in ones are
//!   the unit structs [`Builtins`], [`CallContracts`], [`AttributeContracts`]
//!   and [`FakerTypes`].
//! - [`CallContext`] carries the analyzed source text so extensions can read
//!   argument text without allocating.
//! - [`Analyzer::analyze_owned`] overrides each changed return type in its
//!   snapshot in place (via [`Snapshot::override_symbol`]), so a return type
//!   inferred for one function is visible to later functions of the same
//!   pass. [`Analyzer::analyze`] leaves its snapshot untouched and publishes
//!   inferred returns only between passes.
//! - [`link_members`] reads local variable types from the analyzed document
//!   itself, so locals appended by by-reference outputs resolve too.
//!
//! [`Snapshot::override_symbol`]: swlsp_php_semantic::Snapshot::override_symbol

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
