//! Code lens providers (port of Go `internal/lsp/codelens`).
//!
//! Providers work with byte ranges of the open document snapshot and convert
//! to UTF-16 protocol positions only when building a lens. Navigation lenses
//! use the `shopware.openReferences` client command with one argument: the
//! sorted, deduplicated list of `file://...#line` targets.

mod admin_component;
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
mod twig;
mod twig_component_related;
mod validation;
mod vite;

pub use admin_component::AdminComponentCodeLensProvider;
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
pub use twig::TwigCodeLensProvider;
pub use twig_component_related::TwigComponentRelatedCodeLensProvider;
pub use validation::ValidationCodeLensProvider;
pub use vite::ViteCodeLensProvider;

use std::cmp::Ordering;
use std::collections::HashSet;

use serde_json::Value;
use swlsp_lsp::{BoxError, Context};
use swlsp_lsp_protocol::{self as protocol, Position, Range};
use swlsp_syntax::{LineIndex, TextRange};
use swlsp_uriutil::filepath;

#[cfg(test)]
mod tests;

/// The client command every navigation lens executes.
pub const OPEN_REFERENCES_COMMAND: &str = "shopware.openReferences";

pub(crate) fn cancelled(ctx: &Context) -> Result<(), BoxError> {
    if ctx.is_cancelled() {
        return Err(Box::new(swlsp_lsp::Cancelled));
    }
    Ok(())
}

pub(crate) fn related_lens(
    range: Range,
    title: impl Into<String>,
    targets: Vec<String>,
) -> protocol::CodeLens {
    protocol::CodeLens {
        range,
        command: Some(protocol::Command {
            title: title.into(),
            command: OPEN_REFERENCES_COMMAND.to_owned(),
            arguments: vec![Value::Array(
                targets.into_iter().map(Value::String).collect(),
            )],
        }),
        data: None,
    }
}

pub(crate) fn related_protocol_range(range: TextRange, lines: &LineIndex) -> Range {
    let (start_line, start_character) = lines.position_utf16(range.start().into());
    let (end_line, end_character) = lines.position_utf16(range.end().into());
    Range {
        start: Position {
            line: start_line,
            character: start_character,
        },
        end: Position {
            line: end_line,
            character: end_character,
        },
    }
}

pub(crate) fn line_range(line: u32) -> Range {
    let position = Position { line, character: 0 };
    Range {
        start: position,
        end: position,
    }
}

pub(crate) fn related_target(path: &str, line: i64) -> String {
    if path.is_empty() {
        return String::new();
    }
    let line = line.max(1);
    swlsp_uriutil::file_uri_with_fragment(path, &line.to_string())
}

/// The one-based line of `offset` in the file on disk (1 when unreadable).
pub(crate) fn related_source_line(path: &str, offset: u32) -> i64 {
    let Ok(source) = std::fs::read(path) else {
        return 1;
    };
    let lines = LineIndex::new(String::from_utf8_lossy(&source).into_owned());
    i64::from(lines.position_utf16(offset).0) + 1
}

pub(crate) fn unique_related_targets(targets: Vec<String>) -> Vec<String> {
    let mut seen = HashSet::with_capacity(targets.len());
    let mut result: Vec<String> = targets
        .into_iter()
        .filter(|target| !target.is_empty() && seen.insert(target.clone()))
        .collect();
    result.sort();
    result
}

pub(crate) fn same_path(left: &str, right: &str) -> bool {
    filepath::clean(left) == filepath::clean(right)
}

pub(crate) fn lower_ext(path: &str) -> String {
    filepath::ext(path).to_ascii_lowercase()
}

pub(crate) fn title(lens: &protocol::CodeLens) -> &str {
    lens.command
        .as_ref()
        .map(|command| command.title.as_str())
        .unwrap_or_default()
}

pub(crate) fn by_line(left: &protocol::CodeLens, right: &protocol::CodeLens) -> Ordering {
    left.range.start.line.cmp(&right.range.start.line)
}

pub(crate) fn by_position(left: &protocol::CodeLens, right: &protocol::CodeLens) -> Ordering {
    by_line(left, right).then(left.range.start.character.cmp(&right.range.start.character))
}

pub(crate) fn by_line_title(left: &protocol::CodeLens, right: &protocol::CodeLens) -> Ordering {
    by_line(left, right).then_with(|| title(left).cmp(title(right)))
}

/// Line, character, then title (Go `sortRelatedCodeLenses`).
pub(crate) fn by_position_title(left: &protocol::CodeLens, right: &protocol::CodeLens) -> Ordering {
    by_position(left, right).then_with(|| title(left).cmp(title(right)))
}

pub(crate) fn sort_related_code_lenses(result: &mut [protocol::CodeLens]) {
    result.sort_by(by_position_title);
}

pub(crate) fn counted_title(
    singular: &str,
    plural_prefix: &str,
    plural_suffix: &str,
    count: usize,
) -> String {
    if count == 1 {
        singular.to_owned()
    } else {
        format!("{plural_prefix}{count}{plural_suffix}")
    }
}
