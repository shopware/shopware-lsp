//! Go-to-definition providers.

mod asset;
mod console;
mod console_helper;
mod container_constant;
mod controller;
mod doctrine;
mod environment;
mod event;
mod http_client;
mod messenger;
mod route;
mod serializer;
mod service;
mod symfony_config;
mod translation;
mod validation;

pub use asset::*;
pub use console::*;
pub use console_helper::*;
pub use container_constant::*;
pub use controller::*;
pub use doctrine::*;
pub use environment::*;
pub use event::*;
pub use http_client::*;
pub use messenger::*;
pub use route::*;
pub use serializer::*;
pub use service::*;
pub use symfony_config::*;
pub use translation::*;
pub use validation::*;
// What the definition providers of all domains share.
#[allow(unused_imports)]
pub(crate) use swlsp_ide::definition::*;

#[cfg(test)]
mod tests;
