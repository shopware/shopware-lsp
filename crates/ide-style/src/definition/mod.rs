//! Go-to-definition providers.

mod style_class;

pub use style_class::*;
// What the definition providers of all domains share.
#[allow(unused_imports)]
pub(crate) use swlsp_ide::definition::*;

#[cfg(test)]
mod tests;
