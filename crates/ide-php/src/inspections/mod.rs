//! Inspections of the PHP domain.

pub(crate) mod help;
mod php_imports;

pub use php_imports::{UNUSED_IMPORT_FIX_ID, new_php_imports};
// The shared inspection contract, so the inspections find it beside themselves.
#[allow(unused_imports)]
pub(crate) use swlsp_ide::inspections::*;
