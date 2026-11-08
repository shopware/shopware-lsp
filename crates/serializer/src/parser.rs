use std::collections::HashSet;

use swlsp_parser_php::query;
use swlsp_parser_php::syntax::{
    PHP_BINARY_EXPRESSION, PHP_MEMBER_ACCESS, PHP_SCOPED_ACCESS, PHP_STRING,
};
use swlsp_php::NameResolver;
use swlsp_php_types::go_to_lower;
use swlsp_syntax::{SyntaxNode, SyntaxNodeExt, TextRange, TextSize};

use crate::index::{TargetKind, Usage};

/// Every `deserialize()` call in `root` whose second argument names a class
/// (`Foo::class`, `Foo::class . '[]'`, or a string literal), deduplicated.
pub fn usages_in_document(path: &str, root: &SyntaxNode) -> Vec<Usage> {
    let mut resolver = None;
    let mut result = Vec::new();
    let mut seen = HashSet::new();
    for call in query::calls(root, &[]) {
        if !query::call_method_name(&call).eq_ignore_ascii_case("deserialize") {
            continue;
        }
        let Some(target) = query::argument_expression(&call, 1) else {
            continue;
        };
        let resolver = resolver.get_or_insert_with(|| NameResolver::new(root));
        let Some((class_name, range, kind)) = serializer_target(&target, resolver) else {
            continue;
        };
        if class_name.is_empty() {
            continue;
        }
        let class = normalize_class(&class_name).to_owned();
        if !seen.insert((go_to_lower(&class), range)) {
            continue;
        }
        result.push(Usage {
            class,
            file: path.to_owned(),
            range,
            kind,
        });
    }
    result
}

/// The usage whose target range contains `offset` (its end inclusive).
pub fn usage_at(root: &SyntaxNode, offset: u32) -> Option<Usage> {
    let offset = TextSize::from(offset);
    usages_in_document("", root)
        .into_iter()
        .find(|usage| usage.range.contains_inclusive(offset))
}

fn serializer_target(
    target: &SyntaxNode,
    resolver: &NameResolver,
) -> Option<(String, TextRange, TargetKind)> {
    if let Some((class_name, range)) = class_constant_target(target) {
        return Some((
            resolver.resolve(&class_name),
            range,
            TargetKind::ClassConstant,
        ));
    }
    let literal = query::string_at(target)?;
    let value = normalize_class(&query::string_value(&literal)).to_owned();
    if value.is_empty() {
        return None;
    }
    Some((value, string_value_range(&literal), TargetKind::String))
}

fn class_constant_target(target: &SyntaxNode) -> Option<(String, TextRange)> {
    let (access, range) = match target.kind() {
        PHP_SCOPED_ACCESS | PHP_MEMBER_ACCESS => (target.clone(), target.range_trimmed_trivia()),
        PHP_BINARY_EXPRESSION => {
            let children: Vec<SyntaxNode> = target.children().collect();
            let [left, right] = children.as_slice() else {
                return None;
            };
            if !matches!(left.kind(), PHP_SCOPED_ACCESS | PHP_MEMBER_ACCESS)
                || right.kind() != PHP_STRING
                || query::string_value(right) != "[]"
                || binary_operator(target, left, right) != "."
            {
                return None;
            }
            (left.clone(), left.range_trimmed_trivia())
        }
        _ => return None,
    };
    let class_name = query::class_constant_name(&access);
    (!class_name.is_empty()).then_some((class_name, range))
}

/// The trimmed text between the two operands of a binary expression.
fn binary_operator(expression: &SyntaxNode, left: &SyntaxNode, right: &SyntaxNode) -> String {
    let expression_range = expression.text_range();
    let left_range = left.text_range();
    let right_range = right.text_range();
    if left_range.end() < expression_range.start()
        || right_range.start() < left_range.end()
        || right_range.start() > expression_range.end()
    {
        return String::new();
    }
    let start = usize::from(left_range.end() - expression_range.start());
    let end = usize::from(right_range.start() - expression_range.start());
    let text = expression.text().to_string();
    text.get(start..end)
        .map_or_else(String::new, |operator| operator.trim().to_owned())
}

/// The literal's range inside matching quotes.
fn string_value_range(node: &SyntaxNode) -> TextRange {
    let range = node.range_trimmed_trivia();
    let text = node.text().to_string();
    let text = text.trim().as_bytes();
    if text.len() >= 2
        && matches!(text[0], b'\'' | b'"')
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

pub(crate) fn normalize_class(name: &str) -> &str {
    let name = name.trim();
    let name = name.strip_suffix("[]").unwrap_or(name);
    name.strip_prefix('\\').unwrap_or(name)
}
