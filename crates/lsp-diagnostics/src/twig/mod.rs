//! Twig, Twig component, snippet and translation analyzers (port of the
//! corresponding Go `internal/lsp/diagnostics` files).

mod component;
mod deprecation;
mod enums;
mod live_event;
mod macros;
mod member;
mod render_block;
mod snippet;
mod template;
mod translation;
mod versioning;

#[cfg(test)]
mod tests;

pub use component::TwigComponentAnalyzer;
pub use deprecation::TwigDeprecationAnalyzer;
pub use enums::TwigEnumAnalyzer;
pub use live_event::LiveComponentEventAnalyzer;
pub use macros::TwigMacroAnalyzer;
pub use member::{TwigMemberDeprecationAnalyzer, TwigMemberMissingAnalyzer};
pub use render_block::TwigRenderBlockAnalyzer;
pub use snippet::SnippetAnalyzer;
pub use template::TemplateAnalyzer;
pub use translation::TranslationAnalyzer;
pub use versioning::{
    TWIG_BLOCK_DEPRECATED_CODE, TWIG_BLOCK_REDUNDANT_OVERRIDE_CODE,
    TWIG_VERSIONING_COMMENT_MISSING_CODE, TWIG_VERSIONING_ORIGINAL_MISSING_CODE,
    TWIG_VERSIONING_OUTDATED_CODE, TwigVersioningAnalyzer, TwigVersioningPayload,
};

use serde_json::Value;
use swlsp_php_types::{equal_fold, go_to_lower};

/// Go `filepath.Ext`: the suffix from the last dot of the final element.
pub(crate) fn go_ext(path: &str) -> &str {
    for (index, byte) in path.bytes().enumerate().rev() {
        match byte {
            b'/' => return "",
            b'.' => return &path[index..],
            _ => {}
        }
    }
    ""
}

/// `strings.ToLower(filepath.Ext(path))`.
pub(crate) fn lower_ext(path: &str) -> String {
    go_to_lower(go_ext(path))
}

/// Encodes a Go `[]string` that is `nil` when empty (as returned by
/// `suggestion.Similar`): `null` or a JSON array.
pub(crate) fn nil_strings(values: Vec<String>) -> Value {
    if values.is_empty() {
        Value::Null
    } else {
        Value::Array(values.into_iter().map(Value::String).collect())
    }
}

/// Encodes a non-nil Go `[]string`.
pub(crate) fn strings_value(values: Vec<String>) -> Value {
    Value::Array(values.into_iter().map(Value::String).collect())
}

/// Go `containsFold`.
pub(crate) fn contains_fold<S: AsRef<str>>(values: &[S], value: &str) -> bool {
    values
        .iter()
        .any(|candidate| equal_fold(candidate.as_ref(), value))
}

/// Go `liveArgumentAttributeSegment`: camelCase/snake_case to kebab-case.
pub(crate) fn live_argument_attribute_segment(value: &str) -> String {
    let mut result = String::with_capacity(value.len() + 4);
    for (index, character) in value.char_indices() {
        if character.is_ascii_uppercase() {
            if index != 0 {
                result.push('-');
            }
            result.push(character.to_ascii_lowercase());
            continue;
        }
        if character == '_' {
            result.push('-');
            continue;
        }
        result.push(character);
    }
    result.trim_matches('-').to_owned()
}

/// `uriutil.Path` ignoring the error (Go `path, _ := uriutil.Path(uri)`).
pub(crate) fn uri_path(uri: &str) -> String {
    swlsp_uriutil::path(uri).unwrap_or_default()
}
