//! Analyzers of the Twig domain: they walk a document's syntax tree and
//! return byte-oriented problems. [`crate::inspections`] wraps them into
//! inspections and binds quick fixes.

mod twig;

pub use twig::*;
// The shared analyzer contract, so the analyzers find it beside themselves.
#[allow(unused_imports)]
pub(crate) use swlsp_ide::diagnostics::*;
