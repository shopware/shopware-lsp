//! Go-to-definition providers.

mod dal;
mod feature;
mod snippet;
mod systemconfig;
mod theme;

pub use dal::*;
pub use feature::*;
pub use snippet::*;
pub use systemconfig::*;
pub use theme::*;
// What the definition providers of all domains share.
#[allow(unused_imports)]
pub(crate) use swlsp_ide::definition::*;

#[cfg(test)]
mod tests;
