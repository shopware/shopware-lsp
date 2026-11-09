//! Custom Twig tags supplied by token parsers and a lossless tag scanner.

use serde::{Deserialize, Serialize};
use swlsp_parser_php::query as phpquery;
use swlsp_parser_php::syntax::*;
use swlsp_php::NameResolver;
use swlsp_syntax::{LineIndex, SyntaxNode, SyntaxNodeExt, TextRange};

use crate::extension_parser::{
    CallableDeprecation, last_name_part, last_string_argument, method_named,
};
use crate::path::{base, ext};

/// A tag supplied by a Twig token parser.
#[derive(Clone, Debug, Default, PartialEq, Eq, Serialize, Deserialize)]
pub struct TwigTag {
    pub name: String,
    pub class: String,
    pub file_path: String,
    pub line: i64,
    pub range: TextRange,
    pub deprecated: bool,
    pub deprecation: String,
    pub deprecated_range: TextRange,
}

#[derive(Clone, Debug, Default, PartialEq, Eq)]
pub struct TwigTagUsage {
    pub name: String,
    pub range: TextRange,
}

pub(crate) fn range(start: usize, end: usize) -> TextRange {
    TextRange::new((start as u32).into(), (end as u32).into())
}

pub fn parse_twig_token_parsers(
    file_path: &str,
    root: &SyntaxNode,
    content: &str,
    line_index: &LineIndex,
) -> Vec<TwigTag> {
    if !is_twig_token_parser_candidate(content) {
        return Vec::new();
    }
    let resolver = NameResolver::new(root);
    let file_base = base(file_path);
    let file_stem = &file_base[..file_base.len() - ext(file_base).len()];
    let mut result = Vec::new();
    for class in phpquery::classes(root) {
        let short_name = phpquery::class_name(&class);
        if !is_token_parser_class(&class, Some(&resolver))
            || short_name.ends_with("Test")
            || file_stem.ends_with("Test")
        {
            continue;
        }
        let Some(get_tag) = method_named(&class, "getTag") else {
            continue;
        };
        let deprecation = token_parser_deprecation(&class);
        let class_name = resolver.resolve(&short_name);
        for string_node in phpquery::nodes(&get_tag, &[PHP_STRING]) {
            if !string_returned_by_method(&string_node, &get_tag) {
                continue;
            }
            let name = phpquery::string_value(&string_node);
            if name.trim().is_empty() {
                continue;
            }
            let name_range = php_string_value_range(&string_node, &name);
            let line = i64::from(line_index.position(u32::from(name_range.start())).0) + 1;
            result.push(TwigTag {
                name,
                class: class_name.clone(),
                file_path: file_path.to_owned(),
                line,
                range: name_range,
                deprecated: deprecation.deprecated,
                deprecation: deprecation.message.clone(),
                deprecated_range: deprecation.source_range,
            });
        }
    }
    result
}

pub(crate) fn is_twig_token_parser_candidate(content: &str) -> bool {
    content.contains("TokenParser") && content.contains("getTag")
}

fn find(haystack: &[u8], needle: &[u8]) -> Option<usize> {
    memchr::memmem::find(haystack, needle)
}

pub(crate) fn find_bytes(haystack: &[u8], needle: &[u8]) -> Option<usize> {
    find(haystack, needle)
}

pub(crate) fn rfind_bytes(haystack: &[u8], needle: &[u8]) -> Option<usize> {
    memchr::memmem::rfind(haystack, needle)
}

pub(crate) fn lossy(bytes: &[u8]) -> String {
    String::from_utf8_lossy(bytes).into_owned()
}

/// Tag names opening `{% ... %}` blocks, skipping comments and raw/verbatim
/// bodies.
pub fn twig_tag_usages(content: &[u8]) -> Vec<TwigTagUsage> {
    let mut result = Vec::new();
    let mut verbatim_end = "";
    let mut offset = 0;
    // The next `{#` at or after `offset`, remembered across iterations so
    // the search does not restart from every tag (quadratic in tag count).
    // `None` = not searched yet, `Some(None)` = no comment left.
    let mut next_comment: Option<Option<usize>> = None;
    while offset + 2 <= content.len() {
        let relative = find(&content[offset..], b"{%");
        let comment = if verbatim_end.is_empty() {
            if next_comment.is_none_or(|next| next.is_some_and(|next| next < offset)) {
                next_comment = Some(find(&content[offset..], b"{#").map(|found| offset + found));
            }
            next_comment.flatten().map(|next| next - offset)
        } else {
            None
        };
        if let Some(comment) = comment
            && relative.is_none_or(|relative| comment < relative)
        {
            let comment_start = offset + comment + 2;
            let Some(comment_end) = find(&content[comment_start..], b"#}") else {
                break;
            };
            offset = comment_start + comment_end + 2;
            continue;
        }
        let Some(relative) = relative else {
            break;
        };
        let open = offset + relative;
        let mut cursor = open + 2;
        if cursor < content.len() && content[cursor] == b'-' {
            cursor += 1;
        }
        while cursor < content.len() && is_twig_tag_space(content[cursor]) {
            cursor += 1;
        }
        let start = cursor;
        while cursor < content.len() && is_twig_tag_name_byte(content[cursor]) {
            cursor += 1;
        }
        if cursor > start {
            let name = lossy(&content[start..cursor]);
            if verbatim_end.is_empty() {
                verbatim_end = match name.to_ascii_lowercase().as_str() {
                    "raw" => "endraw",
                    "verbatim" => "endverbatim",
                    _ => "",
                };
                result.push(TwigTagUsage {
                    name,
                    range: range(start, cursor),
                });
            } else if name.eq_ignore_ascii_case(verbatim_end) {
                verbatim_end = "";
            }
        }
        match find(&content[cursor..], b"%}") {
            Some(close) => offset = cursor + close + 2,
            None => break,
        }
    }
    result
}

fn is_token_parser_class(class: &SyntaxNode, resolver: Option<&NameResolver>) -> bool {
    phpquery::class_extends(class)
        .into_iter()
        .chain(phpquery::class_implements(class))
        .any(|name| {
            let resolved = match resolver {
                Some(resolver) => resolver.resolve(&name),
                None => name,
            };
            let part = last_name_part(&resolved);
            if matches!(
                part,
                "TokenParserInterface"
                    | "Twig_TokenParserInterface"
                    | "AbstractTokenParser"
                    | "Twig_TokenParser"
            ) {
                return true;
            }
            // Custom intermediate token-parser base classes are common; the
            // conventional name retains transitive parsers on a cold index.
            swlsp_textutil::lowercase(part).contains("tokenparser")
        })
}

fn token_parser_deprecation(class: &SyntaxNode) -> CallableDeprecation {
    if let Some((documentation, range)) = leading_php_doc(class)
        && let Some(message) = php_doc_deprecation(&documentation)
    {
        return CallableDeprecation {
            deprecated: true,
            message,
            source_range: range,
            ..CallableDeprecation::default()
        };
    }
    for attribute in phpquery::attributes(class) {
        if !swlsp_textutil::eq_ignore_case(
            last_name_part(&phpquery::attribute_name(&attribute)),
            "Deprecated",
        ) {
            continue;
        }
        return CallableDeprecation {
            deprecated: true,
            source_range: attribute.range_trimmed_trivia(),
            ..CallableDeprecation::default()
        };
    }
    let Some(parse) = method_named(class, "parse") else {
        return CallableDeprecation::default();
    };
    for call in phpquery::calls(&parse, &[]) {
        let name = phpquery::call_method_name(&call);
        if call.kind() != PHP_FUNCTION_CALL
            || !swlsp_textutil::eq_ignore_case(
                name.strip_prefix('\\').unwrap_or(&name),
                "trigger_deprecation",
            )
        {
            continue;
        }
        return CallableDeprecation {
            deprecated: true,
            message: last_string_argument(&call),
            source_range: call.range_trimmed_trivia(),
            ..CallableDeprecation::default()
        };
    }
    CallableDeprecation::default()
}

fn string_returned_by_method(string_node: &SyntaxNode, method: &SyntaxNode) -> bool {
    let mut returned = false;
    for current in string_node.ancestors().skip(1) {
        if current == *method {
            break;
        }
        match current.kind() {
            PHP_RETURN_STATEMENT => returned = true,
            PHP_METHOD_DECLARATION
            | PHP_FUNCTION_DECLARATION
            | PHP_CLOSURE
            | PHP_ARROW_FUNCTION => return false,
            _ => {}
        }
    }
    returned
}

fn php_string_value_range(node: &SyntaxNode, value: &str) -> TextRange {
    if value.is_empty() {
        return TextRange::default();
    }
    let trimmed = node.range_trimmed_trivia();
    let text = node.text().to_string();
    let node_start = u32::from(node.text_range().start());
    let start = (u32::from(trimmed.start()) - node_start) as usize;
    let end = (u32::from(trimmed.end()) - node_start) as usize;
    if end >= start
        && end <= text.len()
        && let Some(index) = text.get(start..end).and_then(|slice| slice.find(value))
    {
        let begin = u32::from(trimmed.start()) as usize + index;
        return range(begin, begin + value.len());
    }
    trimmed
}

fn leading_php_doc(node: &SyntaxNode) -> Option<(String, TextRange)> {
    let full = node.text_range();
    let trimmed = node.range_trimmed_trivia();
    if trimmed.start() <= full.start() {
        return None;
    }
    let text = node.text().to_string();
    let prefix_length = (u32::from(trimmed.start() - full.start()) as usize).min(text.len());
    let prefix = text.get(..prefix_length)?;
    let start = prefix.rfind("/**")?;
    let relative_end = prefix[start..].find("*/")?;
    let end = start + relative_end + 2;
    if !prefix[end..].trim().is_empty() {
        return None;
    }
    let base = u32::from(full.start()) as usize;
    Some((
        prefix[start..end].to_owned(),
        range(base + start, base + end),
    ))
}

fn php_doc_deprecation(documentation: &str) -> Option<String> {
    let index = documentation.to_ascii_lowercase().find("@deprecated")?;
    let message = &documentation[index + "@deprecated".len()..];
    let message = match message.find(['\r', '\n']) {
        Some(end) => &message[..end],
        None => message,
    };
    Some(message.trim_matches(['*', '/']).trim().to_owned())
}

pub(crate) fn is_twig_tag_space(value: u8) -> bool {
    matches!(value, b' ' | b'\t' | b'\r' | b'\n')
}

pub(crate) fn is_twig_tag_name_byte(value: u8) -> bool {
    value == b'_' || value == b'-' || value.is_ascii_alphanumeric() || value >= 0x80
}
