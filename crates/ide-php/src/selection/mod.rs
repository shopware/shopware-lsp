//! Selection range providers.
//!
//! Both providers expand through the lossless CST ancestry of the open
//! document: the exact non-trivia token first, then every syntax node and
//! finally the whole document.

mod php;

pub use php::PhpSelectionRangeProvider;
// What the selection providers of all domains share.
#[allow(unused_imports)]
pub(crate) use swlsp_ide::selection::*;

#[cfg(test)]
mod tests;
