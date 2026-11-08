//! Twig completion providers (port of Go `twig_*_completion.go`,
//! `twig_component_variable_completion.go` and
//! `container_constant_completion.go`).

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

use crate::common::go_to_lower;

/// A Markdown documentation value.
pub(crate) fn markdown(value: impl Into<String>) -> Option<protocol::MarkupContent> {
    Some(protocol::MarkupContent {
        kind: protocol::MarkupKind::Markdown,
        value: value.into(),
    })
}

/// Appends to an existing documentation value (Go mutates
/// `item.Documentation.Value` in place).
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

/// Go `strings.ToLower(filepath.Ext(uri))`.
pub(crate) fn lower_ext(uri: &str) -> String {
    go_to_lower(swlsp_uriutil::filepath::ext(uri))
}

/// Go `unicode.IsLetter`.
pub(crate) fn go_is_letter(value: char) -> bool {
    value.is_alphabetic()
}

/// Go `unicode.IsDigit` (decimal digits; Latin-1 superscripts and fractions
/// are not digits).
pub(crate) fn go_is_digit(value: char) -> bool {
    value.is_ascii_digit() || (u32::from(value) > 0xFF && value.is_numeric())
}

/// Go `unicode.IsSpace`.
pub(crate) fn go_is_space(value: char) -> bool {
    value.is_whitespace()
}

/// The lowercase-label comparison every Go `sort.Slice` in this group uses.
pub(crate) fn sort_by_lower_label(items: &mut [protocol::CompletionItem]) {
    crate::common::sort_completion_items(items);
}
