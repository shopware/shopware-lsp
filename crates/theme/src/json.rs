//! The JSON queries theme configuration needs (Go `internal/parser/json/query`).

use swlsp_parser_json::syntax::{
    JSON_ARRAY, JSON_BOOLEAN, JSON_NULL_VALUE, JSON_NUMBER, JSON_OBJECT, JSON_PAIR, JSON_STRING,
};
use swlsp_syntax::SyntaxNode;

fn is_value(node: &SyntaxNode) -> bool {
    matches!(
        node.kind(),
        JSON_OBJECT | JSON_ARRAY | JSON_STRING | JSON_NUMBER | JSON_BOOLEAN | JSON_NULL_VALUE
    )
}

pub(crate) fn root_value(root: &SyntaxNode) -> Option<SyntaxNode> {
    root.children().find(is_value)
}

pub(crate) fn pairs(object: &SyntaxNode) -> impl Iterator<Item = SyntaxNode> + use<> {
    let children = (object.kind() == JSON_OBJECT).then(|| object.children());
    children
        .into_iter()
        .flatten()
        .filter(|child| child.kind() == JSON_PAIR)
}

pub(crate) fn pair_key(pair: &SyntaxNode) -> Option<SyntaxNode> {
    pair.children().find(|child| child.kind() == JSON_STRING)
}

pub(crate) fn pair_value(pair: &SyntaxNode) -> Option<SyntaxNode> {
    let mut children = pair.children();
    children.find(|child| child.kind() == JSON_STRING)?;
    children.find(is_value)
}

pub(crate) fn property(object: &SyntaxNode, name: &str) -> Option<SyntaxNode> {
    pairs(object)
        .find(|pair| pair_key(pair).is_some_and(|key| string_value(&key) == name))
        .and_then(|pair| pair_value(&pair))
}

/// String content without the surrounding quotes; escapes are kept.
pub(crate) fn string_value(node: &SyntaxNode) -> String {
    if node.kind() != JSON_STRING {
        return String::new();
    }
    let text = node.text().to_string();
    let text = text.trim();
    let text = text.strip_prefix('"').unwrap_or(text);
    text.strip_suffix('"').unwrap_or(text).to_owned()
}

/// Go `strconv.ParseBool` of a JSON boolean.
pub(crate) fn boolean_value(node: &SyntaxNode) -> Option<bool> {
    if node.kind() != JSON_BOOLEAN {
        return None;
    }
    match node.text().to_string().trim() {
        "1" | "t" | "T" | "TRUE" | "true" | "True" => Some(true),
        "0" | "f" | "F" | "FALSE" | "false" | "False" => Some(false),
        _ => None,
    }
}

/// Go `strconv.Atoi` of a JSON number.
pub(crate) fn integer_value(node: &SyntaxNode) -> Option<i64> {
    if node.kind() != JSON_NUMBER {
        return None;
    }
    let text = node.text().to_string();
    let text = text.trim();
    let digits = text.strip_prefix(['+', '-']).unwrap_or(text);
    if digits.is_empty() || !digits.bytes().all(|byte| byte.is_ascii_digit()) {
        return None;
    }
    text.parse().ok()
}
