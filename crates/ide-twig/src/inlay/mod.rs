//! Inlay hint providers.

mod twig_variable;

pub use twig_variable::{BROWSE_TWIG_VARIABLES_COMMAND, TwigVariableProvider};
// What the inlay providers of all domains share.
#[allow(unused_imports)]
pub(crate) use swlsp_ide::inlay::*;

#[cfg(test)]
mod tests;
