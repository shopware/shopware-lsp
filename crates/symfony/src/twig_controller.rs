//! Twig `controller()` references.

use swlsp_parser_twig::ast::{AstNode, TwigLiteralString};
use swlsp_parser_twig::query as twigquery;
use swlsp_parser_twig::syntax as twigsyntax;
use swlsp_syntax::{SyntaxNode, SyntaxNodeExt, TextRange, TextSize};

use crate::controller::{ControllerReference, parse_controller_reference};
use crate::util::{GoLower, go_trim_space};

/// A statically knowable first argument of Twig's `controller()` function.
/// `range` covers the string contents without quotes.
#[derive(Clone, Debug, PartialEq, Eq)]
pub struct TwigControllerValue {
    pub value: String,
    pub node: SyntaxNode,
    pub range: TextRange,
}

/// A parsed `controller()` argument and its source.
#[derive(Clone, Debug, PartialEq, Eq)]
pub struct TwigControllerReference {
    pub reference: ControllerReference,
    pub node: SyntaxNode,
    pub range: TextRange,
}

/// Mirrors Twig's escaped namespace spelling and the reference plugin's
/// controller index normalization.
pub fn normalize_twig_controller_value(value: &str) -> String {
    go_trim_space(value)
        .replace("\\\\", "\\")
        .trim_start_matches('\\')
        .to_owned()
}

/// The stable, case-insensitive usage-index key.
pub fn controller_reference_key(reference: &ControllerReference) -> String {
    format!(
        "{}::{}",
        go_trim_space(&reference.target).trim_start_matches('\\'),
        go_trim_space(&reference.method)
    )
    .to_go_lower()
}

fn is_controller_call(call: &SyntaxNode) -> bool {
    twigquery::function_name(call)
        .is_some_and(|name| swlsp_php_types::equal_fold(&name, "controller"))
}

/// Direct static first arguments of `controller()`.
pub fn twig_controller_values(root: &SyntaxNode) -> Vec<TwigControllerValue> {
    let mut result = Vec::new();
    for call in twigquery::iterate_nodes(root, &[twigsyntax::TWIG_FUNCTION_CALL]) {
        if !is_controller_call(&call) {
            continue;
        }
        let Some(literal) = twigquery::string_argument(&call, 0) else {
            continue;
        };
        if !twigquery::string_is_static(&literal) {
            continue;
        }
        result.push(TwigControllerValue {
            value: normalize_twig_controller_value(
                &twigquery::string_value(&literal).unwrap_or_default(),
            ),
            range: twig_controller_string_range(&literal),
            node: literal,
        });
    }
    result
}

/// Every parseable static `controller()` usage.
pub fn twig_controller_references(root: &SyntaxNode) -> Vec<TwigControllerReference> {
    twig_controller_values(root)
        .into_iter()
        .filter_map(|value| {
            Some(TwigControllerReference {
                reference: parse_twig_controller_reference(&value.value)?,
                node: value.node,
                range: value.range,
            })
        })
        .collect()
}

/// The `controller()` string containing `node`. An empty value is accepted
/// so completion works inside `controller('')`.
pub fn twig_controller_value_at(node: &SyntaxNode) -> Option<TwigControllerValue> {
    let literal = twigquery::literal_string_at(node)?;
    if !twigquery::string_is_static(&literal) {
        return None;
    }
    let call = twigquery::function_call_at(&literal)?;
    if !is_controller_call(&call) || twigquery::string_argument(&call, 0).as_ref() != Some(&literal)
    {
        return None;
    }
    Some(TwigControllerValue {
        value: normalize_twig_controller_value(
            &twigquery::string_value(&literal).unwrap_or_default(),
        ),
        range: twig_controller_string_range(&literal),
        node: literal,
    })
}

pub fn twig_controller_reference_at(node: &SyntaxNode) -> Option<TwigControllerReference> {
    let value = twig_controller_value_at(node)?;
    Some(TwigControllerReference {
        reference: parse_twig_controller_reference(&value.value)?,
        node: value.node,
        range: value.range,
    })
}

/// Additionally accepts Symfony's legacy `Bundle:Controller:action` shortcut.
pub fn parse_twig_controller_reference(value: &str) -> Option<ControllerReference> {
    if let Some(reference) = parse_controller_reference(value) {
        return Some(reference);
    }
    let value = go_trim_space(value);
    let parts: Vec<&str> = value.split(':').collect();
    if parts.len() != 3 {
        return None;
    }
    let mut trimmed = Vec::with_capacity(3);
    for part in parts {
        let part = go_trim_space(part);
        if part.is_empty() || part.contains(['%', '$', '{', '}']) {
            return None;
        }
        trimmed.push(part);
    }
    Some(ControllerReference {
        value: value.to_owned(),
        target: format!("{}:{}", trimmed[0], trimmed[1]),
        method: trimmed[2].to_owned(),
    })
}

fn twig_controller_string_range(node: &SyntaxNode) -> TextRange {
    if let Some(literal) = TwigLiteralString::cast(node.clone())
        && let Some(inner) = literal.inner()
    {
        return inner.syntax().range_trimmed_trivia();
    }
    let range = node.range_trimmed_trivia();
    let text = node.text().to_string();
    let text = go_trim_space(&text).as_bytes();
    if text.len() >= 2
        && (text[0] == b'\'' || text[0] == b'"')
        && text[text.len() - 1] == text[0]
        && range.end() > range.start() + TextSize::from(1)
    {
        return TextRange::new(
            range.start() + TextSize::from(1),
            range.end() - TextSize::from(1),
        );
    }
    range
}

#[cfg(test)]
#[path = "tests_routectx/twig_controller.rs"]
mod tests;
