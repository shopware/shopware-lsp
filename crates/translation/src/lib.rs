//! Symfony translation catalogues and references.
//!
//! Request-time PHP analysis is passed explicitly as `Option<&PhpContext>`
//! to [`validate_php_reference`]; missing argument indexes are `None`.

mod index;
mod insertion;
mod parser;
mod placeholder;
mod reference;

#[cfg(test)]
mod tests;

pub use index::Index;
pub use insertion::{Insertion, insertion_for_source};
pub use placeholder::placeholders;
pub use reference::{
    PhpReferenceKind, Reference, ReferenceRole, php_placeholder_reference_at, php_reference_at,
    php_references, reference_at, references, twig_default_domain_before, twig_reference_at,
    twig_references, validate_php_reference,
};

use serde::{Deserialize, Serialize};
use swlsp_syntax::{LineIndex, SyntaxNode, SyntaxNodeExt};
use swlsp_textutil::lowercase;

const STORAGE_SEPARATOR: &str = "\x00";

/// One translation declaration in a source or compiled catalogue.
#[derive(Clone, Debug, Default, PartialEq, Eq, Serialize, Deserialize)]
#[serde(default)]
pub struct Message {
    pub domain: String,
    pub key: String,
    pub text: String,
    pub locale: String,
    pub file: String,
    pub format: String,
    /// Zero-based line of the key.
    pub line: u32,
    /// Zero-based UTF-16 column of the key.
    pub character: u32,
    pub end_line: u32,
    pub end_character: u32,
}

#[allow(clippy::too_many_arguments)]
pub(crate) fn new_message(
    domain: &str,
    key: String,
    text: String,
    locale: &str,
    file: &str,
    format: &str,
    node: Option<&SyntaxNode>,
    line_index: &LineIndex,
) -> Message {
    let mut message = Message {
        domain: normalize_domain(domain).to_owned(),
        key,
        text,
        locale: locale.to_owned(),
        file: file.to_owned(),
        format: format.to_owned(),
        ..Message::default()
    };
    let Some(node) = node else {
        return message;
    };
    let range = node.range_trimmed_trivia();
    let (line, character) = line_index.position_utf16(range.start().into());
    let (end_line, end_character) = line_index.position_utf16(range.end().into());
    message.line = line;
    message.character = character;
    message.end_line = end_line;
    message.end_character = end_character;
    message
}

pub(crate) fn normalize_domain(value: &str) -> &str {
    let value = value.trim();
    value.strip_suffix("+intl-icu").unwrap_or(value)
}

pub(crate) fn storage_key(domain: &str, key: &str) -> String {
    let mut result = lowercase(normalize_domain(domain));
    result.push_str(STORAGE_SEPARATOR);
    result.push_str(key);
    result
}

pub(crate) fn split_storage_key(value: &str) -> Option<(&str, &str)> {
    value.split_once(STORAGE_SEPARATOR)
}

pub(crate) fn sort_messages(messages: &mut [Message]) {
    messages.sort_by(|left, right| {
        left.file
            .cmp(&right.file)
            .then(left.line.cmp(&right.line))
            .then(left.character.cmp(&right.character))
    });
}

/// The last element of a slash-separated path, ignoring trailing slashes;
/// `.` for an empty path and `/` for the root.
pub(crate) fn base_name(path: &str) -> &str {
    let trimmed = path.trim_end_matches('/');
    if trimmed.is_empty() {
        return if path.is_empty() { "." } else { "/" };
    }
    trimmed.rsplit('/').next().unwrap_or(trimmed)
}

/// The suffix of the last slash-separated element from its final dot
/// (`.yaml`); empty when there is none.
pub(crate) fn extension(path: &str) -> &str {
    let bytes = path.as_bytes();
    for index in (0..bytes.len()).rev() {
        match bytes[index] {
            b'/' => break,
            b'.' => return &path[index..],
            _ => {}
        }
    }
    ""
}

pub(crate) fn lower_extension(path: &str) -> String {
    lowercase(extension(path))
}
