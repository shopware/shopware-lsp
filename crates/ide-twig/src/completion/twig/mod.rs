//! Twig completion providers, plus `!php/const` / `!php/enum` completion in
//! Symfony YAML.

mod completion;
mod component_variable;
mod constant;
mod container_constant;
mod enum_completion;
mod guard;
mod include_parameter;
mod macros;
mod render_block;
mod see;
mod statement;
mod tag;
mod test;
mod types;

#[cfg(test)]
mod tests;

pub use completion::TwigCompletionProvider;
pub use component_variable::TwigComponentVariableCompletionProvider;
pub use constant::TwigConstantCompletionProvider;
pub use container_constant::ContainerConstantCompletionProvider;
pub use enum_completion::TwigEnumCompletionProvider;
pub use include_parameter::TwigIncludeParameterCompletionProvider;
pub use macros::TwigMacroCompletionProvider;
pub use render_block::TwigRenderBlockCompletionProvider;

use swlsp_lsp::CompletionRequest;
use swlsp_lsp_protocol as protocol;

/// A Markdown documentation value.
pub(crate) fn markdown(value: impl Into<String>) -> Option<protocol::MarkupContent> {
    Some(protocol::MarkupContent {
        kind: protocol::MarkupKind::Markdown,
        value: value.into(),
    })
}

/// Appends `suffix` to the item's documentation value in place.
pub(crate) fn append_documentation(item: &mut protocol::CompletionItem, suffix: &str) {
    if let Some(documentation) = item.documentation.as_mut() {
        documentation.value.push_str(suffix);
    } else {
        item.documentation = markdown(suffix);
    }
}

/// The byte offset of the request position, `None` without a document.
pub(crate) fn request_offset(request: &CompletionRequest) -> Option<u32> {
    let position = request.params.position;
    Some(
        request
            .line_index()?
            .offset_utf16(position.line, position.character),
    )
}

/// The lowercase extension of `uri`, including the leading dot.
pub(crate) use crate::completion::common::lower_ext;

/// Whether `value` is a digit in an identifier: ASCII digits and numeric
/// characters outside Latin-1 (so superscripts and fractions such as `²` and
/// `½` are not digits).
pub(crate) fn is_identifier_digit(value: char) -> bool {
    value.is_ascii_digit() || (u32::from(value) > 0xFF && value.is_numeric())
}

/// Sorts items by lowercase label, the order every Twig provider uses.
pub(crate) fn sort_by_lower_label(items: &mut [protocol::CompletionItem]) {
    crate::completion::common::sort_completion_items(items);
}
