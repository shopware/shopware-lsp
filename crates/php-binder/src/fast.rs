//! Allocation-free equivalents of hot syntax queries. They walk the green
//! tree instead of materializing red nodes and tokens, and return exactly
//! what the shared `SyntaxNodeExt`/`query` helpers return.

use swlsp_parser_php::syntax::*;
use swlsp_syntax::rowan::{GreenNodeData, NodeOrToken};
use swlsp_syntax::{Kind, SyntaxNode, TextRange, TextSize};

pub(crate) trait Fast {
    /// `SyntaxNodeExt::range_trimmed_trivia`.
    fn trimmed(&self) -> TextRange;
}

impl Fast for SyntaxNode {
    fn trimmed(&self) -> TextRange {
        let range = self.text_range();
        let end = u32::from(range.end());
        let mut offset = u32::from(range.start());
        let mut start = offset;
        leading_trivia(&self.green(), &mut offset, end, &mut start);
        TextRange::new(TextSize::from(start), range.end())
    }
}

/// Returns `true` once a non-trivia token was reached.
fn leading_trivia(green: &GreenNodeData, offset: &mut u32, end: u32, start: &mut u32) -> bool {
    for child in green.children() {
        match child {
            NodeOrToken::Node(node) => {
                if leading_trivia(node, offset, end, start) {
                    return true;
                }
            }
            NodeOrToken::Token(token) => {
                if !Kind(token.kind().0).is_trivia() {
                    return true;
                }
                *offset += u32::from(token.text_len());
                if *offset < end {
                    *start = *offset;
                }
            }
        }
    }
    false
}

/// `query::name_value`.
pub(crate) fn name_value(node: &SyntaxNode, source: &str) -> String {
    if node.kind() != PHP_NAME {
        return String::new();
    }
    let text = &source[node.trimmed()];
    swlsp_textutil::compact_whitespace(text).into_owned()
}

pub(crate) fn name_of(node: Option<&SyntaxNode>, source: &str) -> String {
    node.map(|node| name_value(node, source))
        .unwrap_or_default()
}

fn first_token_text(green: &GreenNodeData, kind: Kind) -> Option<&str> {
    for child in green.children() {
        match child {
            NodeOrToken::Node(node) => {
                if let Some(text) = first_token_text(node, kind) {
                    return Some(text);
                }
            }
            NodeOrToken::Token(token) => {
                if token.kind().0 == kind.0 {
                    return Some(token.text());
                }
            }
        }
    }
    None
}

/// `query::variable_key` for a `PHP_VARIABLE` node (or a node inside one).
pub(crate) fn variable_key(node: &SyntaxNode) -> String {
    let variable = if node.kind() == PHP_VARIABLE {
        node.clone()
    } else {
        match node.ancestors().find(|n| n.kind() == PHP_VARIABLE) {
            Some(variable) => variable,
            None => return String::new(),
        }
    };
    first_token_text(&variable.green(), TK_VARIABLE)
        .map(str::to_owned)
        .unwrap_or_default()
}

/// `query::variable_name`.
pub(crate) fn variable_name(node: &SyntaxNode) -> String {
    let key = variable_key(node);
    key.trim_start_matches('$').to_owned()
}

/// `query::parameter_name`.
pub(crate) fn parameter_name(node: &SyntaxNode) -> String {
    if node.kind() != PHP_PARAMETER {
        return String::new();
    }
    first_token_text(&node.green(), TK_VARIABLE)
        .map(str::to_owned)
        .unwrap_or_default()
}

/// Ranges of `return` statements directly inside a function body (not in
/// nested functions, closures, or methods), in source order, found without
/// materializing red nodes.
pub(crate) fn direct_return_ranges(body: &SyntaxNode, out: &mut Vec<TextRange>) {
    let mut offset = u32::from(body.text_range().start());
    green_returns(&body.green(), &mut offset, out);
}

fn green_returns(green: &GreenNodeData, offset: &mut u32, out: &mut Vec<TextRange>) {
    for child in green.children() {
        match child {
            NodeOrToken::Token(token) => *offset += u32::from(token.text_len()),
            NodeOrToken::Node(node) => {
                let start = *offset;
                let kind = Kind(node.kind().0);
                match kind {
                    PHP_RETURN_STATEMENT => {
                        *offset += u32::from(node.text_len());
                        out.push(TextRange::new(start.into(), (*offset).into()));
                    }
                    PHP_METHOD_DECLARATION
                    | PHP_FUNCTION_DECLARATION
                    | PHP_CLOSURE
                    | PHP_ARROW_FUNCTION => {
                        *offset += u32::from(node.text_len());
                    }
                    _ => green_returns(node, offset, out),
                }
            }
        }
    }
}

/// The node of `kind` spanning exactly `range` below `root`.
pub(crate) fn node_with_range(
    root: &SyntaxNode,
    range: TextRange,
    kind: Kind,
) -> Option<SyntaxNode> {
    let mut current = root.clone();
    loop {
        if current.text_range() == range && current.kind() == kind {
            return Some(current);
        }
        current = current
            .children()
            .find(|child| child.text_range().contains_range(range))?;
    }
}

/// `query::parameter_type`/`property_type`/`method_return_type` for the
/// declaration node itself: the compacted text of its first type child.
pub(crate) fn direct_type_text(node: &SyntaxNode, source: &str) -> String {
    node.children()
        .find(|child| crate::declaration_references::is_type_node(child.kind()))
        .map_or_else(String::new, |child| {
            compact_expression(&source[child.text_range()])
        })
}

/// `query::parameter_optional`.
pub(crate) fn parameter_optional(node: &SyntaxNode) -> bool {
    first_token_text(&node.green(), TK_EQUALS).is_some()
}

/// Removes white space and comment-like regions (Go `compactExpression`).
fn compact_expression(text: &str) -> String {
    let bytes = text.as_bytes();
    let needs_slow = bytes.iter().enumerate().any(|(index, &byte)| match byte {
        b' ' | b'\t' | b'\r' | b'\n' => true,
        b'/' => matches!(bytes.get(index + 1), Some(b'/' | b'*')),
        _ => false,
    });
    if !needs_slow {
        return text.to_owned();
    }
    let mut out = Vec::with_capacity(bytes.len());
    let mut line_comment = false;
    let mut block_comment = false;
    let mut index = 0;
    while index < bytes.len() {
        let byte = bytes[index];
        let next = bytes.get(index + 1).copied();
        if line_comment {
            if byte == b'\n' || byte == b'\r' {
                line_comment = false;
            }
        } else if block_comment {
            if byte == b'*' && next == Some(b'/') {
                block_comment = false;
                index += 1;
            }
        } else if byte == b'/' && next == Some(b'/') {
            line_comment = true;
            index += 1;
        } else if byte == b'/' && next == Some(b'*') {
            block_comment = true;
            index += 1;
        } else if !matches!(byte, b' ' | b'\t' | b'\r' | b'\n') {
            out.push(byte);
        }
        index += 1;
    }
    String::from_utf8(out)
        .unwrap_or_else(|error| String::from_utf8_lossy(error.as_bytes()).into_owned())
}
