//! Derives PHP literal types directly from lossless syntax (port of
//! `internal/php/literal`).

use swlsp_parser_php::query;
use swlsp_parser_php::syntax::{PHP_BOOLEAN, PHP_NULL, PHP_NUMBER, PHP_STRING};
use swlsp_php_types::{Type, equal_fold};
use swlsp_syntax::{SyntaxNode, SyntaxNodeExt};

/// Returns the intrinsic type of a scalar literal node. These types do not
/// depend on flow state and therefore need no semantic side-table entry.
pub fn type_of(node: &SyntaxNode) -> Option<Type> {
    match node.kind() {
        PHP_STRING => Some(Type::literal_string(&query::string_value(node))),
        PHP_NUMBER => Some(number_type(&trimmed_node_text(node))),
        PHP_BOOLEAN => {
            if equal_fold(node.text().to_string().trim(), "true") {
                Some(Type::true_type())
            } else {
                Some(Type::false_type())
            }
        }
        PHP_NULL => Some(Type::null()),
        _ => None,
    }
}

fn trimmed_node_text(node: &SyntaxNode) -> String {
    let full = node.text_range();
    let trimmed = node.range_trimmed_trivia();
    let text = node.text().to_string();
    let start = usize::from(trimmed.start() - full.start());
    let end = usize::from(trimmed.end()).saturating_sub(usize::from(full.start()));
    if start > end
        || end > text.len()
        || !text.is_char_boundary(start)
        || !text.is_char_boundary(end)
    {
        return text;
    }
    text[start..end].to_owned()
}

fn number_type(text: &str) -> Type {
    let text = text.trim().replace('_', "");
    let unsigned = text.trim_start_matches(['+', '-']);
    let lower = unsigned.to_ascii_lowercase();
    if lower.starts_with("0x") || lower.starts_with("0b") || lower.starts_with("0o") {
        return Type::literal_int(&text);
    }
    if lower.contains(['.', 'e']) {
        if text.parse::<f64>().is_ok() {
            return Type::literal_float(&text);
        }
        return Type::float();
    }
    Type::literal_int(&text)
}

#[cfg(test)]
mod tests {
    use super::*;
    use swlsp_parser_php::syntax::PHP_VARIABLE;

    #[test]
    fn type_of_scalar_literals() {
        let root = swlsp_parser_php::root(
            "<?php\n// leading trivia\n\t1_024;\n\t1.5e2;\n\t0xDEAD;\n\t\"value\";\ntrue;\nnull;\n",
        );
        let numbers = query::nodes(&root, &[PHP_NUMBER]);
        assert_eq!(numbers.len(), 3);
        assert_eq!(type_of(&numbers[0]).unwrap().to_string(), "1024");
        assert_eq!(type_of(&numbers[1]).unwrap().to_string(), "1.5e2");
        assert_eq!(type_of(&numbers[2]).unwrap().to_string(), "0xDEAD");

        let strings = query::nodes(&root, &[PHP_STRING]);
        assert_eq!(strings.len(), 1);
        assert_eq!(type_of(&strings[0]).unwrap().to_string(), "\"value\"");
        let boolean = &query::nodes(&root, &[PHP_BOOLEAN])[0];
        assert_eq!(type_of(boolean).unwrap().to_string(), "true");
        let null = &query::nodes(&root, &[PHP_NULL])[0];
        assert_eq!(type_of(null).unwrap().to_string(), "null");
    }

    #[test]
    fn type_of_rejects_non_literal_syntax() {
        let root = swlsp_parser_php::root("<?php $value;");
        let variable = &query::nodes(&root, &[PHP_VARIABLE])[0];
        assert!(type_of(variable).is_none());
    }
}
