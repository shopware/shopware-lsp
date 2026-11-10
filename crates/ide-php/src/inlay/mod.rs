//! Inlay hint providers.

mod phpunit_provider;

pub use phpunit_provider::PhpUnitProviderProvider;
// What the inlay providers of all domains share.
#[allow(unused_imports)]
pub(crate) use swlsp_ide::inlay::*;

#[cfg(test)]
mod tests;
