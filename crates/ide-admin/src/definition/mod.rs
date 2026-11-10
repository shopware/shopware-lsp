//! Go-to-definition providers.

mod admin;

pub use admin::*;
// What the definition providers of all domains share.
#[allow(unused_imports)]
pub(crate) use swlsp_ide::definition::*;

#[cfg(test)]
mod tests;
