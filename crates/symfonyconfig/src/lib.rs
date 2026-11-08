//! Symfony bundle configuration roots (`getConfigTreeBuilder()`), config
//! root-key and `imports` resource references in PHP and YAML config files
//! (port of Go `internal/symfonyconfig`).

mod index;
mod navigation;
mod parser;
mod reference;

pub use index::{Index, Record};
pub use navigation::resource_files;
pub use parser::{Root, roots_in_php};
pub use reference::{ResourceReference, RootReference, resource_reference_at, root_reference_at};

#[cfg(test)]
mod tests;
