//! Static member chains rooted at a fixed global.

use crate::*;

/// A statically named member access rooted at `prefix`. Returns the receiver
/// path between the prefix and the member, and the member name (empty for an
/// incomplete trailing dot).
pub(crate) fn javascript_static_member(
    node: &SyntaxNode,
    prefix: &str,
) -> Option<(Vec<String>, String)> {
    let member = jsquery::member_expression_at(node)?;
    let direct_receiver = member.children().next()?;
    if *node != member && javascript_node_within(node, &direct_receiver) {
        return None;
    }
    let (segments, incomplete) =
        javascript_static_expression(&javascript_trimmed_node_text(&member), prefix)?;
    if segments.is_empty() && !incomplete {
        return None;
    }
    if incomplete {
        let names = static_expression_segment_names(&segments);
        if names.len() != segments.len() {
            return None;
        }
        return Some((names, String::new()));
    }
    let last = segments.last()?;
    if last.called || last.indexed {
        return None;
    }
    let receiver = static_expression_segment_names(&segments[..segments.len() - 1]);
    if receiver.len() != segments.len() - 1 {
        return None;
    }
    Some((receiver, last.name.clone()))
}

pub(crate) fn javascript_static_member_name_node(
    node: &SyntaxNode,
    prefix: &str,
) -> Option<SyntaxNode> {
    let (_, name) = javascript_static_member(node, prefix)?;
    if name.is_empty() {
        return None;
    }
    let member = jsquery::member_expression_at(node)?;
    let mut result = None;
    for child in member.children().skip(1) {
        if child.kind() == jssyntax::JS_IDENTIFIER
            && javascript_trimmed_node_text(&child).trim() == name
        {
            result = Some(child);
        }
    }
    result
}

/// The node text without leading trivia.
pub(crate) fn javascript_trimmed_node_text(node: &SyntaxNode) -> String {
    let full = node.text_range();
    let trimmed = node.range_trimmed_trivia();
    let text = node.text().to_string();
    let start = usize::from(trimmed.start() - full.start());
    let end = usize::from(trimmed.end() - full.start());
    if start > end || end > text.len() {
        return text;
    }
    text[start..end].to_owned()
}

/// Parses `expression` as a static member chain rooted at `prefix`. Returns
/// the segments after the prefix and whether the chain ends in a dot.
pub(crate) fn javascript_static_expression(
    expression: &str,
    prefix: &str,
) -> Option<(Vec<VueStaticExpressionSegment>, bool)> {
    let expression = expression.trim();
    if prefix.is_empty()
        || !expression.starts_with(prefix)
        || (expression.len() > prefix.len()
            && is_vue_identifier_part(expression.as_bytes()[prefix.len()]))
    {
        return None;
    }
    let incomplete = expression.ends_with('.') || expression.ends_with("?.");
    let mut parse_value = expression;
    if incomplete {
        parse_value = parse_value.strip_suffix('.').unwrap_or(parse_value);
        parse_value = parse_value.strip_suffix('?').unwrap_or(parse_value);
    }
    let (segments, end) = vue_static_expression_segments(parse_value, prefix.len())?;
    if !parse_value.get(end..).unwrap_or("").trim().is_empty() {
        return None;
    }
    Some((segments, incomplete))
}

/// Parses a dotted receiver path such as `api.languageId`.
pub(crate) fn static_expression_path(path: &str) -> Option<Vec<VueStaticExpressionSegment>> {
    let path = path.trim();
    if path.is_empty() {
        return Some(Vec::new());
    }
    let (segments, end) = vue_static_expression_segments(&format!(".{path}"), 0)?;
    (end == path.len() + 1).then_some(segments)
}

/// Segment names, or an empty list when a segment is called, indexed or
/// unnamed.
pub(crate) fn static_expression_segment_names(
    segments: &[VueStaticExpressionSegment],
) -> Vec<String> {
    let mut result = Vec::with_capacity(segments.len());
    for segment in segments {
        if segment.called || segment.indexed || segment.name.is_empty() {
            return Vec::new();
        }
        result.push(segment.name.clone());
    }
    result
}
