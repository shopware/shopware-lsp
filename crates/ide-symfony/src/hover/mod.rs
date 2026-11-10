//! Hover providers.
//!
//! Every provider builds Markdown from byte ranges and converts byte
//! ranges to UTF-16 positions only when building the protocol range.

mod asset;
mod console;
mod console_helper;
mod controller;
mod doctrine;
mod environment;
mod event;
mod http_client;
mod messenger;
mod route;
mod serializer;
mod translation;
mod validation;

pub use asset::AssetHoverProvider;
pub use console::ConsoleHoverProvider;
pub use console_helper::ConsoleHelperHoverProvider;
pub use controller::ControllerHoverProvider;
pub use doctrine::DoctrineHoverProvider;
pub use environment::EnvironmentHoverProvider;
pub use event::EventHoverProvider;
pub use http_client::HttpClientHoverProvider;
pub use messenger::MessengerHoverProvider;
pub use route::RouteHoverProvider;
pub use serializer::SerializerHoverProvider;
pub use translation::TranslationHoverProvider;
pub use validation::ValidationHoverProvider;
// What the hover providers of all domains share.
#[allow(unused_imports)]
pub(crate) use swlsp_ide::hover::*;
