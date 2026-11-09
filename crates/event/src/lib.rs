//! Symfony EventDispatcher listeners and dispatches.
//!
//! Request-time PHP analysis is passed explicitly as `Option<&PhpContext>`.
//! Occurrences with equal file and start offset keep their discovery order
//! (stable sort).

mod index;
mod model;
mod parser;
mod reference;
mod type_extension;

#[cfg(test)]
mod tests;

pub use index::Index;
pub use model::{Constant, Event, Occurrence, OccurrenceKind, Record, SourceKind};
pub use reference::{
    Reference, ReferenceOrigin, ReferenceRole, php_reference_at, public_methods, reference_at,
    resolve_listener, xml_reference_at, yaml_reference_at,
};
pub use type_extension::PhpTypeExtension;
