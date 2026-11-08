//! Completion for framework domains backed by their own indexes: console,
//! Doctrine, events, Messenger, environment variables, assets, validation
//! constraints, feature flags, DAL, system config, theme config, Symfony
//! configuration roots, HTTP status constants and HttpClient options.

mod asset;
mod console;
mod console_helper;
mod dal;
mod doctrine;
mod environment;
mod event;
mod feature;
mod http_client;
mod messenger;
mod response_constant;
mod symfony_config;
mod systemconfig;
mod theme;
mod validation;

#[cfg(test)]
mod tests;

pub use asset::AssetCompletionProvider;
pub use console::ConsoleCompletionProvider;
pub use console_helper::ConsoleHelperCompletionProvider;
pub use dal::DalCompletionProvider;
pub use doctrine::DoctrineCompletionProvider;
pub use environment::EnvironmentCompletionProvider;
pub use event::EventCompletionProvider;
pub use feature::FeatureCompletionProvider;
pub use http_client::HttpClientCompletionProvider;
pub use messenger::MessengerCompletionProvider;
pub use response_constant::ResponseConstantCompletionProvider;
pub use symfony_config::SymfonyConfigCompletionProvider;
pub use systemconfig::SystemConfigCompletionProvider;
pub use theme::ThemeCompletionProvider;
pub use validation::ValidationCompletionProvider;

use swlsp_lsp_protocol as protocol;

/// Go `filepath.Ext` lowercased.
pub(crate) fn lower_ext(path: &str) -> String {
    crate::common::go_to_lower(swlsp_uriutil::filepath::ext(path))
}

pub(crate) fn markdown(value: impl Into<String>) -> protocol::MarkupContent {
    protocol::MarkupContent {
        kind: protocol::MarkupKind::Markdown,
        value: value.into(),
    }
}

pub(crate) fn item(
    label: impl Into<String>,
    kind: protocol::CompletionItemKind,
    detail: impl Into<String>,
) -> protocol::CompletionItem {
    protocol::CompletionItem {
        label: label.into(),
        kind: Some(kind),
        detail: detail.into(),
        ..Default::default()
    }
}

pub(crate) fn strings(values: &[&str]) -> Vec<String> {
    values.iter().map(|value| (*value).to_string()).collect()
}
