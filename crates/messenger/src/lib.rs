//! Symfony Messenger handlers and dispatches (port of Go
//! `internal/messenger`).
//!
//! # Differences from Go
//!
//! - Go passes the request PHP context through `context.Context`; Rust takes
//!   an explicit `Option<&PhpContext>`.
//! - Occurrences with equal file and start offset keep their discovery order
//!   (stable sort); Go's `sort.Slice` leaves it unspecified.

mod index;
mod model;
mod parser;
mod reference;

#[cfg(test)]
mod tests;

pub use index::Index;
pub use model::{Message, Occurrence, OccurrenceKind, SourceKind};
pub use reference::{
    HandlerMethodReference, Reference, ReferenceRole, handler_method_reference_at,
    php_reference_at, public_handler_methods, reference_at, xml_reference_at, yaml_reference_at,
};
