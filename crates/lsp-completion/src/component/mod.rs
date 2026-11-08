//! Twig/Live component, PHP attribute, controller and route completion
//! (Go `twig_component_completion.go`, `twig_component_php_completion.go`,
//! `live_component_event_completion.go`, `php_attribute_completion.go`,
//! `controller_completion.go`, `route_completion.go`).

mod controller;
mod live_component_event;
mod php_attribute;
mod route;
mod twig_component;
mod twig_component_php;
mod util;

pub use controller::ControllerCompletionProvider;
pub use live_component_event::LiveComponentEventCompletionProvider;
pub use php_attribute::PhpAttributeCompletionProvider;
pub use route::RouteCompletionProvider;
pub use twig_component::TwigComponentCompletionProvider;
pub use twig_component_php::TwigComponentPhpCompletionProvider;

#[cfg(test)]
mod tests;
