//! Shopware DAL entity definitions.
//!
//! [`Index`] persists entity definitions independently of the PHP semantic
//! index; [`context`] recognizes static DAL references in JavaScript, Twig
//! and PHP sources.

pub mod context;
mod criteria_scope;
mod extension;
mod index;

pub use context::*;
pub use criteria_scope::{JsCriteriaRoot, js_criteria_definition, js_criteria_root};
pub use extension::DEFINITION_KIND_EXTENSION;
pub use index::*;

#[cfg(test)]
mod tests;
