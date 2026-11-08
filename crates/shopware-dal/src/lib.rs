//! Shopware DAL entity definitions (port of Go `internal/shopware/dal`).
//!
//! [`Index`] persists entity definitions independently of the PHP semantic
//! index; [`context`] recognizes static DAL references in JavaScript, Twig
//! and PHP sources.

pub mod context;
mod index;

pub use context::*;
pub use index::*;

#[cfg(test)]
mod tests;
