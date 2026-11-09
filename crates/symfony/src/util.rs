//! Helpers shared by several Symfony parsers.

use std::path::{Component, Path, PathBuf};

use swlsp_parser_xml::query as xmlquery;
use swlsp_parser_xml::syntax as xmlsyntax;
use swlsp_syntax::{LineIndex, SyntaxNode, SyntaxNodeExt, TextRange, TextSize};

/// One-based line of `offset`.
pub(crate) fn line_at(line_index: &LineIndex, offset: TextSize) -> u32 {
    line_index.position(offset.into()).0 + 1
}

/// One-based line of a node start.
pub(crate) fn node_line(line_index: &LineIndex, node: &SyntaxNode) -> u32 {
    line_at(line_index, node.text_range().start())
}

/// Parses statically knowable Symfony configuration booleans. The second
/// value reports whether the value was understood.
pub(crate) fn configured_service_bool(value: &str) -> (bool, bool) {
    match value.trim().to_lowercase().as_str() {
        "1" | "true" | "yes" | "on" => (true, true),
        "0" | "false" | "no" | "off" | "null" | "~" => (false, true),
        _ => (false, false),
    }
}

pub(crate) fn deprecated_config_value(value: &str) -> bool {
    let value = value.trim();
    !value.is_empty() && !value.eq_ignore_ascii_case("false") && value != "0"
}

pub(crate) fn deprecation_message(value: &str) -> String {
    let value = value.trim();
    if !deprecated_config_value(value)
        || value.eq_ignore_ascii_case("true")
        || value.eq_ignore_ascii_case("null")
        || value == "~"
        || value == "1"
    {
        return String::new();
    }
    value.to_owned()
}

/// Strips one pair of matching quotes from a trimmed range (shared by XML
/// attribute and YAML scalar content ranges).
fn unquoted_range(mut range: TextRange, text: &str) -> TextRange {
    let text = text.trim();
    let bytes = text.as_bytes();
    if bytes.len() >= 2
        && (bytes[0] == b'\'' || bytes[0] == b'"')
        && bytes[bytes.len() - 1] == bytes[0]
        && range.end() > range.start() + TextSize::from(1)
    {
        range = TextRange::new(
            range.start() + TextSize::from(1),
            range.end() - TextSize::from(1),
        );
    }
    range
}

/// The value range of an XML attribute without its quotes.
pub(crate) fn xml_attribute_content_range(attribute: Option<&SyntaxNode>) -> TextRange {
    let Some(attribute) = attribute else {
        return TextRange::default();
    };
    for child in attribute.children() {
        if child.kind() != xmlsyntax::XML_ATTRIBUTE_VALUE {
            continue;
        }
        return unquoted_range(child.range_trimmed_trivia(), &child.text().to_string());
    }
    TextRange::default()
}

/// The first non-blank direct text of an element, trimmed, with its range.
pub(crate) fn xml_element_text(node: &SyntaxNode) -> (String, TextRange) {
    for text in xmlquery::nodes(node, &[xmlsyntax::XML_TEXT]) {
        if xmlquery::element_at(&text).as_ref() != Some(node) {
            continue;
        }
        let raw = text.text().to_string();
        let trimmed = raw.trim_matches(|c: char| c.is_whitespace());
        if trimmed.is_empty() {
            continue;
        }
        let range = text.text_range();
        let left = raw.len() - raw.trim_start_matches([' ', '\t', '\r', '\n']).len();
        let right = raw.len() - raw.trim_end_matches([' ', '\t', '\r', '\n']).len();
        let range = TextRange::new(
            range.start() + TextSize::from(left as u32),
            range.end() - TextSize::from(right as u32),
        );
        return (raw.trim().to_owned(), range);
    }
    (String::new(), TextRange::default())
}

/// The value of attribute `name`, or an empty string.
pub(crate) fn xml_attr(node: &SyntaxNode, name: &str) -> String {
    xmlquery::attribute(node, name)
        .and_then(|attribute| xmlquery::attribute_value(&attribute))
        .unwrap_or_default()
}

/// The scalar range without quotes.
pub(crate) fn yaml_scalar_content_range(node: Option<&SyntaxNode>) -> TextRange {
    let Some(node) = node else {
        return TextRange::default();
    };
    unquoted_range(node.range_trimmed_trivia(), &node.text().to_string())
}

/// The shortest lexically equivalent slash-separated path: `.` and empty
/// elements dropped, `..` resolved where possible; `.` for an empty result.
pub(crate) fn clean_path(path: &str) -> String {
    if path.is_empty() {
        return ".".to_owned();
    }
    let rooted = path.starts_with('/');
    let mut parts: Vec<&str> = Vec::new();
    for part in path.split('/') {
        match part {
            "" | "." => {}
            ".." => {
                if parts.last().is_some_and(|last| *last != "..") {
                    parts.pop();
                } else if !rooted {
                    parts.push("..");
                }
            }
            other => parts.push(other),
        }
    }
    let joined = parts.join("/");
    if rooted {
        format!("/{joined}")
    } else if joined.is_empty() {
        ".".to_owned()
    } else {
        joined
    }
}

/// The cleaned directory part of a slash-separated path; `.` without one.
pub(crate) fn dir_path(path: &str) -> String {
    match path.rfind('/') {
        Some(index) => clean_path(&path[..=index]),
        None => ".".to_owned(),
    }
}

/// The last element of a slash-separated path, ignoring trailing slashes;
/// `.` for an empty path and `/` for the root.
pub(crate) fn base_name(path: &str) -> &str {
    let trimmed = path.trim_end_matches('/');
    if trimmed.is_empty() {
        return if path.is_empty() { "." } else { "/" };
    }
    match trimmed.rfind('/') {
        Some(index) => &trimmed[index + 1..],
        None => trimmed,
    }
}

/// The non-empty `parts` joined with `/` and cleaned; empty when all parts
/// are empty.
pub(crate) fn join_path(parts: &[&str]) -> String {
    let joined: Vec<&str> = parts
        .iter()
        .copied()
        .filter(|part| !part.is_empty())
        .collect();
    if joined.is_empty() {
        return String::new();
    }
    clean_path(&joined.join("/"))
}

/// The extension of the last element including its dot (`.xml`); empty
/// without one. Callers lowercase it when needed.
#[allow(dead_code)]
pub(crate) fn extension(path: &str) -> &str {
    let base_start = path.rfind('/').map_or(0, |index| index + 1);
    match path[base_start..].rfind('.') {
        Some(index) => &path[base_start + index..],
        None => "",
    }
}

/// Resolves a configuration-relative path.
pub(crate) fn service_config_path(config_path: &str, value: &str) -> String {
    let value = value.trim();
    if value.is_empty() {
        return String::new();
    }
    if value.starts_with('/') {
        return clean_path(value);
    }
    join_path(&[&dir_path(config_path), value])
}

/// `target` relative to `base` after cleaning both; `None` when one is
/// absolute and the other is not, or when `base` climbs above the common
/// prefix.
pub(crate) fn relative_path(base: &str, target: &str) -> Option<String> {
    let base = clean_path(base);
    let target = clean_path(target);
    if base.starts_with('/') != target.starts_with('/') {
        return None;
    }
    let base_path = PathBuf::from(&base);
    let target_path = PathBuf::from(&target);
    let base_parts: Vec<Component> = base_path.components().collect();
    let target_parts: Vec<Component> = target_path.components().collect();
    let base_parts: Vec<Component> = base_parts
        .into_iter()
        .filter(|part| *part != Component::CurDir)
        .collect();
    let target_parts: Vec<Component> = target_parts
        .into_iter()
        .filter(|part| *part != Component::CurDir)
        .collect();
    let common = base_parts
        .iter()
        .zip(target_parts.iter())
        .take_while(|(left, right)| left == right)
        .count();
    if base_parts[common..].contains(&Component::ParentDir) {
        return None;
    }
    let mut result: Vec<String> = Vec::new();
    for _ in common..base_parts.len() {
        result.push("..".to_owned());
    }
    for part in &target_parts[common..] {
        result.push(Path::new(part.as_os_str()).to_string_lossy().into_owned());
    }
    if result.is_empty() {
        return Some(".".to_owned());
    }
    Some(result.join("/"))
}

/// [`swlsp_textutil::lowercase`] as a method, for case-insensitive keys.
pub(crate) trait Lowercase {
    fn lowercase(&self) -> String;
}

impl Lowercase for str {
    fn lowercase(&self) -> String {
        swlsp_textutil::lowercase(self)
    }
}

impl Lowercase for String {
    fn lowercase(&self) -> String {
        swlsp_textutil::lowercase(self)
    }
}

/// A non-negative decimal index (optional sign); -1 otherwise.
pub(crate) fn numeric_index(value: &str) -> i32 {
    match value.parse::<i64>() {
        Ok(index) if (0..=i64::from(i32::MAX)).contains(&index) => index as i32,
        _ => -1,
    }
}

/// ASCII case-insensitive substring search.
pub(crate) fn contains_fold_ascii(content: &[u8], needle: &str) -> bool {
    let needle = needle.as_bytes();
    let Some((&first, _)) = needle.split_first() else {
        return true;
    };
    // Candidate starts are found with a vectorized search for either case
    // of the first byte; indexers prefilter every PHP file with this.
    memchr::memchr2_iter(
        first.to_ascii_lowercase(),
        first.to_ascii_uppercase(),
        content,
    )
    .any(|start| {
        content
            .get(start..start + needle.len())
            .is_some_and(|window| window.eq_ignore_ascii_case(needle))
    })
}
