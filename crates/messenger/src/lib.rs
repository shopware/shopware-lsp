//! Symfony Messenger handlers and dispatches.
//!
//! Request-time PHP analysis is passed explicitly as `Option<&PhpContext>`.
//! Occurrences with equal file and start offset keep their discovery order
//! (stable sort).

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
