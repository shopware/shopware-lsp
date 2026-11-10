//! Analyzers of the Administration domain: they walk a document's syntax tree and
//! return byte-oriented problems. [`crate::inspections`] wraps them into
//! inspections and binds quick fixes.

mod admin;

pub use admin::*;
// The shared analyzer contract, so the analyzers find it beside themselves.
#[allow(unused_imports)]
pub(crate) use swlsp_ide::diagnostics::*;
