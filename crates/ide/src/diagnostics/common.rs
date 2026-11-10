//! Helpers shared by several analyzer groups.

use swlsp_parser_php::query as phpquery;
use swlsp_parser_php::syntax::{
    PHP_ARGUMENT_LIST, PHP_NAME, PHP_NAMED_ARGUMENT, PHP_PARENTHESIZED,
};
use swlsp_php_semantic::Snapshot;
use swlsp_php_types::{Kind as TypeKind, Type};
use swlsp_syntax::{Kind, SyntaxNode, SyntaxNodeExt, TextRange, TextSize};
use swlsp_textutil::{eq_ignore_case, lowercase};

/// Candidates within edit distance 2 (case-insensitive), ranked by
/// `suggestion.Similar`.
pub fn admin_nearby_suggestions<S: AsRef<str>>(input: &str, candidates: &[S]) -> Vec<String> {
    let lowered_input = lowercase(input);
    let nearby: Vec<&str> = candidates
        .iter()
        .map(AsRef::as_ref)
        .filter(|candidate| {
            bounded_admin_edit_distance(&lowered_input, &lowercase(candidate), 2) <= 2
        })
        .collect();
    swlsp_suggestion::similar(input, &nearby)
}

/// Levenshtein distance over chars, capped at `limit + 1`. Pairs whose lengths
/// alone differ by more than `limit` skip the distance computation.
pub fn bounded_admin_edit_distance(left: &str, right: &str, limit: usize) -> usize {
    if left.chars().count().abs_diff(right.chars().count()) > limit {
        return limit + 1;
    }
    strsim::levenshtein(left, right).min(limit + 1)
}

/// The trimmed range of a JavaScript string literal without its quotes.
pub fn javascript_string_content_range(node: &SyntaxNode, source: &str) -> TextRange {
    let range = node.range_trimmed_trivia();
    let (start, end) = (
        u32::from(range.start()) as usize,
        u32::from(range.end()) as usize,
    );
    if end - start < 2 || end > source.len() {
        return range;
    }
    let bytes = source.as_bytes();
    let quote = bytes[start];
    if matches!(quote, b'\'' | b'"' | b'`') && bytes[end - 1] == quote {
        return TextRange::new(
            range.start() + TextSize::from(1),
            range.end() - TextSize::from(1),
        );
    }
    range
}

/// `node` or its nearest ancestor of `kind`.
pub fn ancestor_php_node(node: &SyntaxNode, kind: Kind) -> Option<SyntaxNode> {
    node.ancestors().find(|current| current.kind() == kind)
}

pub fn short_php_name(name: &str) -> &str {
    let name = name.trim();
    let name = name.strip_prefix('\\').unwrap_or(name);
    match name.rfind('\\') {
        Some(separator) => &name[separator + 1..],
        None => name,
    }
}

/// The last direct `PHP_NAME` before the argument list.
pub fn call_target_name(call: &SyntaxNode) -> Option<SyntaxNode> {
    let mut target = None;
    for child in call.children() {
        if child.kind() == PHP_ARGUMENT_LIST {
            return target;
        }
        if child.kind() == PHP_NAME {
            target = Some(child);
        }
    }
    target
}

/// Whether `value` or one of its type arguments is an object type that is a
/// subtype of `target`.
pub fn php_type_is_subtype(value: &Type, snapshot: Option<&Snapshot>, target: &str) -> bool {
    let Some(snapshot) = snapshot else {
        return false;
    };
    if value.kind() == TypeKind::Object && !value.name().is_empty() {
        return snapshot.is_subtype_of(value.name(), target);
    }
    value
        .arguments()
        .iter()
        .any(|argument| php_type_is_subtype(argument, Some(snapshot), target))
}

/// The expression passed for `parameter_name` (named) or at `position`
/// (positional).
pub fn local_call_argument(
    call: &SyntaxNode,
    parameter_name: &str,
    position: usize,
) -> Option<SyntaxNode> {
    let mut positional = 0;
    for argument in phpquery::arguments(call) {
        let name = phpquery::argument_name(&argument);
        if !name.is_empty() {
            if eq_ignore_case(&name, parameter_name) {
                return local_argument_expression(&argument);
            }
            continue;
        }
        if positional == position {
            return local_argument_expression(&argument);
        }
        positional += 1;
    }
    None
}

/// The first child node of an argument, skipping the parameter name of a named
/// argument.
pub fn local_argument_expression(argument: &SyntaxNode) -> Option<SyntaxNode> {
    let mut skip_named_parameter = argument.kind() == PHP_NAMED_ARGUMENT;
    for child in argument.children() {
        if skip_named_parameter && child.kind() == PHP_NAME {
            skip_named_parameter = false;
            continue;
        }
        return Some(child);
    }
    None
}

pub fn unwrap_php_parentheses(node: &SyntaxNode) -> SyntaxNode {
    let mut node = node.clone();
    while node.kind() == PHP_PARENTHESIZED {
        let Some(child) = node.children().next() else {
            break;
        };
        node = child;
    }
    node
}

/// The first child node of a return statement.
pub fn direct_return_expression(php_return: &SyntaxNode) -> Option<SyntaxNode> {
    php_return.children().next()
}

/// The range of `value` inside the trimmed node text, or the trimmed node
/// range.
pub fn value_node_text_range(node: Option<&SyntaxNode>, value: &str) -> TextRange {
    let Some(node) = node else {
        return TextRange::default();
    };
    let trimmed = node.range_trimmed_trivia();
    if value.is_empty() {
        return trimmed;
    }
    let node_range = node.text_range();
    let text = node.text().to_string();
    let start = u32::from(trimmed.start()).wrapping_sub(u32::from(node_range.start())) as usize;
    let end = u32::from(trimmed.end()).wrapping_sub(u32::from(node_range.start())) as usize;
    if end < start || end > text.len() {
        return trimmed;
    }
    match text.get(start..end).and_then(|slice| slice.find(value)) {
        Some(index) => TextRange::new(
            trimmed.start() + TextSize::from(index as u32),
            trimmed.start() + TextSize::from((index + value.len()) as u32),
        ),
        None => trimmed,
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn edit_distance_is_bounded() {
        assert_eq!(bounded_admin_edit_distance("kitten", "sitting", 3), 3);
        assert_eq!(bounded_admin_edit_distance("a", "abcd", 2), 3);
        assert_eq!(bounded_admin_edit_distance("same", "same", 2), 0);
    }

    #[test]
    fn short_php_name_strips_namespace() {
        assert_eq!(short_php_name(" \\Foo\\Bar "), "Bar");
        assert_eq!(short_php_name("Bar"), "Bar");
    }
}
