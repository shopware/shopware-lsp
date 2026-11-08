//! Syntax helpers shared by the evaluator (port of the node helpers in
//! `helpers.go` and `flow_helpers.go`).

use std::borrow::Cow;

use swlsp_parser_php::query;
use swlsp_parser_php::syntax::*;
use swlsp_php_types::{Type, equal_fold, go_to_lower};
use swlsp_syntax::rowan::NodeOrToken;
use swlsp_syntax::{Kind, SyntaxNode, SyntaxNodeExt, TextRange};

/// Source text of a node. Trees are parsed from one source snapshot whose
/// root starts at offset zero, so node ranges index the source directly.
#[derive(Clone, Copy)]
pub(crate) struct Source<'a>(pub(crate) &'a str);

impl<'a> Source<'a> {
    pub(crate) fn text(self, node: &SyntaxNode) -> &'a str {
        range_text(self.0, node.text_range())
    }

    pub(crate) fn opt_text(self, node: Option<&SyntaxNode>) -> &'a str {
        node.map_or("", |node| self.text(node))
    }
}

pub(crate) fn range_text(source: &str, range: TextRange) -> &str {
    let start = usize::from(range.start());
    let end = usize::from(range.end());
    source.get(start..end).unwrap_or("")
}

pub(crate) fn start(node: &SyntaxNode) -> u32 {
    node.text_range().start().into()
}

pub(crate) fn trimmed_start(node: &SyntaxNode) -> u32 {
    node.range_trimmed_trivia().start().into()
}

pub(crate) fn first_node(node: &SyntaxNode) -> Option<SyntaxNode> {
    node.children().next()
}

pub(crate) fn last_node(node: &SyntaxNode) -> Option<SyntaxNode> {
    node.last_child()
}

/// Direct child nodes (tokens skipped), materialized once.
pub(crate) fn direct_nodes(node: &SyntaxNode) -> Vec<SyntaxNode> {
    node.children().collect()
}

/// The first direct operator token (assignment, binary, unary or keyword
/// operator).
pub(crate) fn direct_operator(node: &SyntaxNode) -> String {
    for element in node.children_with_tokens() {
        let NodeOrToken::Token(token) = element else {
            continue;
        };
        let kind = token.kind();
        if kind.is_trivia() {
            continue;
        }
        match kind {
            TK_EQUALS | TK_OPERATOR | TK_PIPE | TK_AMPERSAND | TK_QUESTION => {
                return token.text().to_owned();
            }
            TK_KEYWORD => {
                let text = token.text();
                if ["instanceof", "and", "or", "xor"]
                    .iter()
                    .any(|keyword| equal_fold(text, keyword))
                {
                    return text.to_owned();
                }
            }
            _ => {}
        }
    }
    String::new()
}

pub(crate) fn has_direct_token(node: &SyntaxNode, kind: Kind) -> bool {
    node.children_with_tokens()
        .any(|element| element.as_token().is_some_and(|token| token.kind() == kind))
}

pub(crate) fn has_direct_token_text(node: &SyntaxNode, text: &str) -> bool {
    node.children_with_tokens().any(|element| {
        element
            .as_token()
            .is_some_and(|token| equal_fold(token.text(), text))
    })
}

pub(crate) fn is_expression_kind(kind: Kind) -> bool {
    matches!(
        kind,
        PHP_ASSIGNMENT_EXPRESSION
            | PHP_BINARY_EXPRESSION
            | PHP_UNARY_EXPRESSION
            | PHP_TERNARY_EXPRESSION
            | PHP_MEMBER_CALL
            | PHP_SCOPED_CALL
            | PHP_FUNCTION_CALL
            | PHP_MEMBER_ACCESS
            | PHP_SCOPED_ACCESS
            | PHP_ARRAY_ACCESS
            | PHP_OBJECT_CREATION
            | PHP_CLOSURE
            | PHP_ARROW_FUNCTION
            | PHP_MATCH_EXPRESSION
            | PHP_THROW_EXPRESSION
            | PHP_YIELD_EXPRESSION
            | PHP_CLONE_EXPRESSION
            | PHP_CAST_EXPRESSION
            | PHP_ARRAY
            | PHP_STRING
            | PHP_NAME
            | PHP_VARIABLE
            | PHP_NUMBER
            | PHP_BOOLEAN
            | PHP_NULL
            | PHP_PARENTHESIZED
    )
}

pub(crate) fn is_type_kind(kind: Kind) -> bool {
    matches!(
        kind,
        PHP_TYPE | PHP_NULLABLE_TYPE | PHP_UNION_TYPE | PHP_INTERSECTION_TYPE
    )
}

pub(crate) fn compact(source: &str) -> Cow<'_, str> {
    swlsp_textutil::compact_whitespace(source)
}

/// Go `strings.ToLower(strings.TrimPrefix(name, "\\"))`.
pub(crate) fn lower_trimmed(name: &str) -> String {
    go_to_lower(name.strip_prefix('\\').unwrap_or(name))
}

pub(crate) fn trim_backslash(name: &str) -> &str {
    name.strip_prefix('\\').unwrap_or(name)
}

pub(crate) fn trim_quotes(name: &str) -> &str {
    name.trim_matches(['"', '\''])
}

pub(crate) fn cast_type(source: Source<'_>, node: &SyntaxNode) -> Type {
    let text = go_to_lower(&compact(source.text(node)));
    let starts = |prefixes: &[&str]| prefixes.iter().any(|prefix| text.starts_with(prefix));
    if starts(&["(int)", "(integer)"]) {
        Type::INT
    } else if starts(&["(float)", "(double)", "(real)"]) {
        Type::FLOAT
    } else if starts(&["(string)", "(binary)"]) {
        Type::STRING
    } else if starts(&["(bool)", "(boolean)"]) {
        Type::BOOL
    } else if starts(&["(array)"]) {
        Type::array(Type::MIXED, Type::MIXED)
    } else if starts(&["(object)"]) {
        Type::OBJECT
    } else {
        Type::UNKNOWN
    }
}

pub(crate) fn is_call_named(node: &SyntaxNode, name: &str) -> bool {
    query::call_at(node).is_some_and(|call| {
        equal_fold(
            trim_backslash(&query::call_method_name(&call)),
            trim_backslash(name),
        )
    })
}

/// Lowercased call target name without a leading backslash.
pub(crate) fn lower_call_name(node: &SyntaxNode) -> String {
    lower_trimmed(&query::call_method_name(node))
}

pub(crate) fn variable_key(node: &SyntaxNode) -> String {
    query::variable_key(node)
}

pub(crate) fn is_null_node(source: Source<'_>, node: Option<&SyntaxNode>) -> bool {
    let Some(node) = node else {
        return false;
    };
    node.kind() == PHP_NULL
        || node.kind() == PHP_NAME && equal_fold(&compact(source.text(node)), "null")
}

/// The stable environment key of a repeated variable, member or array
/// expression; empty for other expressions.
pub(crate) fn flow_expression_key(source: Source<'_>, node: Option<&SyntaxNode>) -> String {
    let Some(node) = node else {
        return String::new();
    };
    match node.kind() {
        PHP_VARIABLE => query::variable_key(node),
        PHP_MEMBER_ACCESS | PHP_MEMBER_CALL | PHP_SCOPED_ACCESS | PHP_SCOPED_CALL
        | PHP_ARRAY_ACCESS => {
            let text = range_text(source.0, node.range_trimmed_trivia());
            normalize_flow_expression(text).into_owned()
        }
        _ => String::new(),
    }
}

/// Unwraps value-preserving condition syntax (parentheses, assignment).
pub(crate) fn condition_flow_expression_key(
    source: Source<'_>,
    node: Option<&SyntaxNode>,
) -> String {
    let Some(node) = node else {
        return String::new();
    };
    match node.kind() {
        PHP_PARENTHESIZED => condition_flow_expression_key(source, first_node(node).as_ref()),
        PHP_ASSIGNMENT_EXPRESSION => flow_expression_key(source, first_node(node).as_ref()),
        _ => flow_expression_key(source, Some(node)),
    }
}

/// Removes whitespace and normalizes `?->` to `->`. Borrows compact input.
pub(crate) fn normalize_flow_expression(source: &str) -> Cow<'_, str> {
    let source = source.trim();
    let bytes = source.as_bytes();
    let needs = bytes.iter().enumerate().any(|(index, &byte)| match byte {
        b' ' | b'\t' | b'\r' | b'\n' => true,
        b'?' => index + 2 < bytes.len() && bytes[index + 1] == b'-' && bytes[index + 2] == b'>',
        _ => false,
    });
    if !needs {
        return Cow::Borrowed(source);
    }
    let mut result = Vec::with_capacity(bytes.len());
    let mut index = 0;
    while index < bytes.len() {
        match bytes[index] {
            b' ' | b'\t' | b'\r' | b'\n' => {}
            b'?' if index + 2 < bytes.len()
                && bytes[index + 1] == b'-'
                && bytes[index + 2] == b'>' =>
            {
                result.extend_from_slice(b"->");
                index += 3;
                continue;
            }
            byte => result.push(byte),
        }
        index += 1;
    }
    // Only ASCII bytes were removed or replaced, so UTF-8 stays valid.
    Cow::Owned(String::from_utf8(result).unwrap_or_default())
}

pub(crate) fn is_true_condition(source: Source<'_>, node: &SyntaxNode) -> bool {
    let mut current = Some(node.clone());
    while let Some(node) = current.as_ref() {
        if node.kind() != PHP_PARENTHESIZED {
            break;
        }
        current = first_node(node);
    }
    current.is_some_and(|node| {
        node.kind() == PHP_BOOLEAN && equal_fold(&compact(source.text(&node)), "true")
    })
}

/// The `$key` and `$value` targets of a foreach statement.
pub(crate) fn direct_foreach_variables(
    node: &SyntaxNode,
) -> (Option<SyntaxNode>, Option<SyntaxNode>) {
    let mut in_targets = false;
    let mut key = None;
    let mut value: Option<SyntaxNode> = None;
    for element in node.children_with_tokens() {
        match element {
            NodeOrToken::Token(token) => {
                if equal_fold(token.text(), "as") {
                    in_targets = true;
                } else if in_targets && token.kind() == TK_CLOSE_PAREN {
                    return (key, value);
                }
            }
            NodeOrToken::Node(child) => {
                if !in_targets || child.kind() != PHP_VARIABLE {
                    continue;
                }
                if value.is_some() {
                    key = value.take();
                }
                value = Some(child);
            }
        }
    }
    (key, value)
}

pub(crate) fn contains_direct_yield(node: &SyntaxNode) -> bool {
    for child in node.children() {
        if child.kind() == PHP_YIELD_EXPRESSION {
            return true;
        }
        if matches!(
            child.kind(),
            PHP_METHOD_DECLARATION | PHP_FUNCTION_DECLARATION | PHP_CLOSURE | PHP_ARROW_FUNCTION
        ) {
            continue;
        }
        if contains_direct_yield(&child) {
            return true;
        }
    }
    false
}

pub(crate) fn match_arm_is_default(node: &SyntaxNode) -> bool {
    node.children_with_tokens().any(|element| {
        element
            .as_token()
            .is_some_and(|token| equal_fold(token.text().trim(), "default"))
    })
}

/// The leading `/** ... */` comment that only comment trivia separates from
/// the node.
pub(crate) fn leading_doc_comment<'a>(source: Source<'a>, node: &SyntaxNode) -> &'a str {
    let range = node.text_range();
    let trimmed = node.range_trimmed_trivia();
    if trimmed.start() <= range.start() {
        return "";
    }
    let text = source.text(node);
    let prefix_length = usize::from(trimmed.start() - range.start()).min(text.len());
    let Some(prefix) = text.get(..prefix_length) else {
        return "";
    };
    let Some(start) = prefix.rfind("/**") else {
        return "";
    };
    let Some(end) = prefix[start..].find("*/") else {
        return "";
    };
    let end = end + start + 2;
    if !only_comment_trivia(&prefix[end..]) {
        return "";
    }
    &prefix[start..end]
}

fn only_comment_trivia(mut text: &str) -> bool {
    loop {
        text = text.trim();
        if text.is_empty() {
            return true;
        }
        if text.starts_with("//") || text.starts_with('#') {
            match text.find('\n') {
                Some(newline) => {
                    text = &text[newline + 1..];
                    continue;
                }
                None => return true,
            }
        }
        if let Some(rest) = text.strip_prefix("/*") {
            match rest.find("*/") {
                Some(end) => text = &rest[end + 2..],
                None => return false,
            }
            continue;
        }
        return false;
    }
}

/// The receiver of a `Receiver::class` access.
pub(crate) fn class_constant_receiver(
    source: Source<'_>,
    node: Option<&SyntaxNode>,
) -> Option<SyntaxNode> {
    let node = node?;
    if (node.kind() != PHP_MEMBER_ACCESS && node.kind() != PHP_SCOPED_ACCESS)
        || !has_direct_token(node, TK_SCOPE_RESOLUTION)
    {
        return None;
    }
    let nodes = direct_nodes(node);
    if nodes.len() < 2 || !equal_fold(&query::name_value(&nodes[nodes.len() - 1]), "class") {
        return None;
    }
    let _ = source;
    Some(nodes[0].clone())
}

/// The variable a predicate tests, unwrapping parentheses and `?? null`.
pub(crate) fn predicate_flow_target(
    source: Source<'_>,
    node: Option<SyntaxNode>,
) -> Option<SyntaxNode> {
    let node = node?;
    if node.kind() == PHP_PARENTHESIZED {
        return predicate_flow_target(source, first_node(&node));
    }
    if node.kind() == PHP_BINARY_EXPRESSION && direct_operator(&node) == "??" {
        let nodes = direct_nodes(&node);
        if nodes.len() >= 2 && is_null_node(source, nodes.last()) {
            return predicate_flow_target(source, Some(nodes[0].clone()));
        }
    }
    Some(node)
}
