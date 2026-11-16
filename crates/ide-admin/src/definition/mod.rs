//! Go-to-definition providers.

mod admin;
mod api_route;

pub use admin::*;
pub use api_route::AdminApiRouteDefinitionProvider;
// What the definition providers of all domains share.
#[allow(unused_imports)]
pub(crate) use swlsp_ide::definition::*;

#[cfg(test)]
mod tests;
