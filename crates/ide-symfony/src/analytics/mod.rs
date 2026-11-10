//! Symfony analytics catalogs:
//! routes, services, Twig extensions, template usages and variables, Twig
//! components, and the local Symfony profiler request index.
//!
//! Every catalog is exposed as a custom LSP command with a JSON response.

pub mod profiler;
mod profiler_serialized;
pub mod route_catalog;
mod service_locator;

pub use profiler::{
    LIST_PROFILER_REQUESTS_COMMAND, ProfilerCatalogProvider, ProfilerMailMessage,
    ProfilerRequestCatalogEntry, ProfilerRequestCatalogRequest, ProfilerRuntimeTwigComponent,
};
pub use route_catalog::{
    LIST_ROUTES_COMMAND, RouteCatalogEntry, RouteCatalogProvider, RouteCatalogRequest,
};
pub use service_locator::{
    LOCATE_SERVICES_COMMAND, ServiceDefinitionSourceEntry, ServiceLocatorEntry,
    ServiceLocatorProvider, ServiceLocatorRequest,
};
// What the analytics providers of all domains share.
#[allow(unused_imports)]
pub(crate) use swlsp_ide::analytics::*;

#[cfg(test)]
mod tests;
