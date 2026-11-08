//! Doctrine DBAL custom type registrations and DBAL table/column
//! intelligence (port of Go `internal/doctrine`).
//!
//! # Differences from Go
//!
//! - Go passes the request PHP context through `context.Context`; Rust takes
//!   an explicit `Option<&PhpContext>`.
//! - Sorts are stable; Go's `sort.Slice` leaves ties unspecified.
//! - Helpers that slice the first byte of a table name (`dbal_camelize`,
//!   `dbal_initials`, single-letter alias) use the first character, so
//!   non-ASCII names stay valid UTF-8 instead of being cut mid-character.
//! - `snake_case_doctrine_type` uses Rust's Unicode case and alphanumeric
//!   properties, which differ from Go's `unicode` tables only for rare
//!   non-ASCII characters.

mod candidate;
mod dbal;
mod helpers;
mod index;
mod type_config;
mod types;

#[cfg(test)]
mod tests;

pub use dbal::{DbalCompletion, DbalCompletionKind, DbalReference, DbalReferenceRole};
pub use index::Index;
pub use type_config::{
    TypeRegistration, TypeRegistrationReference, TypeRegistrationReferenceRole,
    type_registration_reference_at, type_registrations_in_document,
};
pub use types::{TypeDeclaration, type_declarations};
