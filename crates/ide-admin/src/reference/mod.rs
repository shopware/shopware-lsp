//! Find-references providers.
//!
//! Providers holding a PHP index derive the request's memoized PHP semantic
//! state with [`php_context`] for PHP documents.

mod admin;
mod api_route;

pub use admin::AdminReferenceProvider;
pub use api_route::AdminApiRouteReferenceProvider;
// What the reference providers of all domains share.
#[allow(unused_imports)]
pub(crate) use swlsp_ide::reference::*;

#[cfg(test)]
mod tests;
