//! Code lenses of the symfony domain.

mod console_command;
mod controller_related;
mod messenger;
mod php_service;
mod related_navigation;
mod route_endpoint;
mod route_resource;
mod serializer;
mod service_related;
mod symfony_config;
mod validation;
mod vite;

pub use console_command::{ConsoleCommandCodeLensProvider, RUN_SYMFONY_COMMAND_ID};
pub use controller_related::ControllerRelatedCodeLensProvider;
pub use messenger::MessengerCodeLensProvider;
pub use php_service::PhpServiceCodeLensProvider;
pub use related_navigation::RelatedNavigationCodeLensProvider;
pub use route_endpoint::RouteEndpointCodeLensProvider;
pub use route_resource::RouteResourceCodeLensProvider;
pub use serializer::SerializerCodeLensProvider;
pub use service_related::ServiceRelatedCodeLensProvider;
pub use symfony_config::SymfonyConfigCodeLensProvider;
pub use validation::ValidationCodeLensProvider;
pub use vite::ViteCodeLensProvider;

#[cfg(test)]
mod tests;
