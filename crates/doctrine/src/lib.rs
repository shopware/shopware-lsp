//! Doctrine DBAL custom type registrations and DBAL table/column
//! intelligence.
//!
//! - Request-time PHP analysis is passed explicitly as `Option<&PhpContext>`.
//! - Sorts are stable, so ties keep their discovery order.
//! - Helpers that take the first letter of a table name (`dbal_camelize`,
//!   `dbal_initials`, single-letter alias) use the first character, so
//!   non-ASCII names are never cut mid-character.
//! - `snake_case_doctrine_type` uses Unicode case and alphanumeric
//!   properties.

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
