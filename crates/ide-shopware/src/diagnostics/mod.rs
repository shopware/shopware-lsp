//! Analyzers of the Shopware domain: they walk a document's syntax tree and
//! return byte-oriented problems. [`crate::inspections`] wraps them into
//! inspections and binds quick fixes.

mod migration;
mod shopware;

pub use migration::*;
pub use shopware::*;
// The shared analyzer contract, so the analyzers find it beside themselves.
#[allow(unused_imports)]
pub(crate) use swlsp_ide::diagnostics::*;
