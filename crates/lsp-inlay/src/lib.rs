//! Inlay hint providers.

mod admin_parameter;
mod phpunit_provider;
mod route_controller;
mod route_path;
mod service_argument;
mod snippet_preview;
mod twig_variable;

pub use admin_parameter::AdminParameterProvider;
pub use phpunit_provider::PhpUnitProviderProvider;
pub use route_controller::RouteControllerProvider;
pub use route_path::RoutePathProvider;
pub use service_argument::ServiceArgumentProvider;
pub use snippet_preview::SnippetPreviewProvider;
pub use twig_variable::{BROWSE_TWIG_VARIABLES_COMMAND, TwigVariableProvider};

use swlsp_lsp::TextDocument;
use swlsp_lsp_protocol as protocol;

#[cfg(test)]
mod tests;

/// The byte range of the requested hint range, normalized to start <= end.
pub(crate) fn inlay_hint_byte_range(document: &TextDocument, range: protocol::Range) -> (u32, u32) {
    let lines = document.line_index();
    let start = lines.offset_utf16(range.start.line, range.start.character);
    let end = lines.offset_utf16(range.end.line, range.end.character);
    if end < start {
        (end, start)
    } else {
        (start, end)
    }
}

pub(crate) fn position(document: &TextDocument, offset: u32) -> protocol::Position {
    let (line, character) = document.line_index().position_utf16(offset);
    protocol::Position { line, character }
}

pub(crate) fn line_position(line: u32) -> protocol::Position {
    protocol::Position { line, character: 0 }
}
