//! Rename and refactor providers: Twig template file renames, typed Twig usages
//! of renamed PHP symbols and Administration registry/contract renames.

mod php_twig_rename;
mod twig_template_rename;

pub use php_twig_rename::PhpTwigRenameProvider;
pub use twig_template_rename::TwigTemplateRenameProvider;
// What the refactor providers of all domains share.
#[allow(unused_imports)]
pub(crate) use swlsp_ide::refactor::*;

#[cfg(test)]
mod tests;
