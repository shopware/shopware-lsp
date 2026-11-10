//! Rename and refactor providers: Twig template file renames, typed Twig usages
//! of renamed PHP symbols and Administration registry/contract renames.

mod admin_rename;
mod admin_rename_helpers;

pub use admin_rename::AdminRenameProvider;
// What the refactor providers of all domains share.
#[allow(unused_imports)]
pub(crate) use swlsp_ide::refactor::*;

#[cfg(test)]
mod tests;
