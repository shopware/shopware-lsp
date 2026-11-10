//! Find-references providers.
//!
//! Providers holding a PHP index derive the request's memoized PHP semantic
//! state with [`php_context`] for PHP documents.

mod asset;
mod controller;
mod doctrine;
mod environment;
mod event;
mod messenger;
mod route;
mod serializer;

pub use asset::AssetReferenceProvider;
pub use controller::ControllerReferenceProvider;
pub use doctrine::DoctrineReferenceProvider;
pub use environment::EnvironmentReferenceProvider;
pub use event::EventReferenceProvider;
pub use messenger::MessengerReferenceProvider;
pub use route::RouteReferenceProvider;
pub use serializer::SerializerReferenceProvider;
// What the reference providers of all domains share.
#[allow(unused_imports)]
pub(crate) use swlsp_ide::reference::*;

#[cfg(test)]
mod tests;
