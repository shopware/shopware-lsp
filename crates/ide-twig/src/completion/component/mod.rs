//! Twig/Live component, PHP attribute, controller and route completion.

mod live_component_event;
mod php_attribute;
mod twig_component;
mod twig_component_php;

pub use live_component_event::LiveComponentEventCompletionProvider;
pub use php_attribute::PhpAttributeCompletionProvider;
pub use twig_component::TwigComponentCompletionProvider;
pub use twig_component_php::TwigComponentPhpCompletionProvider;

#[cfg(test)]
mod tests;
