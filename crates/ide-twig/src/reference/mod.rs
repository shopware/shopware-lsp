//! Find-references providers.
//!
//! Providers holding a PHP index derive the request's memoized PHP semantic
//! state with [`php_context`] for PHP documents.

mod live_component_event;
mod twig_component;
mod twig_constant;
mod twig_macro;
mod twig_php;
mod twig_template;

pub use live_component_event::LiveComponentEventReferenceProvider;
pub use twig_component::TwigComponentReferenceProvider;
pub use twig_constant::TwigConstantReferenceProvider;
pub use twig_macro::TwigMacroReferenceProvider;
pub use twig_php::TwigPhpReferenceProvider;
pub use twig_template::TwigTemplateReferenceProvider;
// What the reference providers of all domains share.
#[allow(unused_imports)]
pub(crate) use swlsp_ide::reference::*;

#[cfg(test)]
mod tests;
