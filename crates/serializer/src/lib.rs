//! Symfony Serializer `deserialize()` target classes (port of Go
//! `internal/serializer`).

mod index;
mod parser;

pub use index::{Index, Record, TargetKind, Usage};
pub use parser::{usage_at, usages_in_document};

#[cfg(test)]
mod tests;
