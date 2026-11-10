//! Inlay hint providers.

mod admin_parameter;

pub use admin_parameter::AdminParameterProvider;
// What the inlay providers of all domains share.
#[allow(unused_imports)]
pub(crate) use swlsp_ide::inlay::*;

#[cfg(test)]
mod tests;
