//! Inlay hint providers.

mod route_controller;
mod route_path;
mod service_argument;

pub use route_controller::RouteControllerProvider;
pub use route_path::RoutePathProvider;
pub use service_argument::ServiceArgumentProvider;
// What the inlay providers of all domains share.
#[allow(unused_imports)]
pub(crate) use swlsp_ide::inlay::*;

#[cfg(test)]
mod tests;
