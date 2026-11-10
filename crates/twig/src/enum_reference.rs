//! `enum()` / `enum_cases()` class references.

use swlsp_parser_twig::query as twigquery;
use swlsp_syntax::{SyntaxNode, SyntaxNodeExt, TextRange};

#[derive(Clone, Debug, PartialEq, Eq)]
pub struct EnumReference {
    pub name: String,
    pub node: SyntaxNode,
    pub range: TextRange,
}

pub fn enum_reference_at(node: &SyntaxNode) -> Option<EnumReference> {
    enum_reference(&twigquery::literal_string_at(node)?)
}

pub fn enum_completion_at(node: &SyntaxNode) -> bool {
    twigquery::literal_string_at(node).is_some_and(|literal| {
        twigquery::string_in_function(&literal, &["enum", "enum_cases"])
            && twigquery::function_argument_index(&literal) == Some(0)
            && twigquery::string_is_static(&literal)
    })
}

pub fn enum_references(root: &SyntaxNode) -> Vec<EnumReference> {
    twigquery::string_arguments_in_functions(root, &["enum", "enum_cases"])
        .iter()
        .filter_map(enum_reference)
        .collect()
}

fn enum_reference(literal: &SyntaxNode) -> Option<EnumReference> {
    if !enum_completion_at(literal) {
        return None;
    }
    let raw = twigquery::string_value(literal).unwrap_or_default();
    let name = unescape_twig_class_name(&raw);
    if name.is_empty() {
        return None;
    }
    let mut start = u32::from(literal.range_trimmed_trivia().start());
    if let Some(relative) = literal.text_string().find(&raw) {
        start = u32::from(literal.text_range().start()) + relative as u32;
    }
    Some(EnumReference {
        name,
        node: literal.clone(),
        range: TextRange::new(start.into(), (start + raw.len() as u32).into()),
    })
}

pub(crate) fn unescape_twig_class_name(value: &str) -> String {
    let value = value.trim();
    value
        .strip_prefix('\\')
        .unwrap_or(value)
        .replace("\\\\", "\\")
}

pub fn escape_twig_class_name(value: &str) -> String {
    value
        .strip_prefix('\\')
        .unwrap_or(value)
        .replace('\\', "\\\\")
}
