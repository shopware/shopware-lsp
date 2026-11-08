//! Symfony Console commands, their arguments/options, and Console helpers
//! (port of Go `internal/console`).
//!
//! Go reads the request's PHP analysis from `context.Context`; Rust takes an
//! explicit [`swlsp_php::PhpContext`].

mod catalog_provider;
mod helper;
mod index;
mod model;
mod parser;
mod reference;

pub use catalog_provider::{
    CatalogEntry, CatalogInput, CatalogProvider, CatalogRequest, LIST_CATALOG_COMMAND,
};
pub use helper::{
    COMMAND_CLASS, HELPER_INTERFACE, HELPER_SET_CLASS, Helper, HelperCatalog, HelperReference,
    helper_reference_at, helpers, validate_helper_reference,
};
pub use index::Index;
pub use model::{Command, Input, InputKind};
pub use parser::{legacy_command_inputs, parse_php_commands_tree};
pub use reference::{
    Reference, ReferenceRole, inputs_for_reference, reference_at, validate_reference,
};

pub(crate) fn equal_fold(left: &str, right: &str) -> bool {
    swlsp_php_types::equal_fold(left, right)
}

#[cfg(test)]
mod tests;
