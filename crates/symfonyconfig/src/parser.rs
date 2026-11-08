use std::collections::HashSet;

use serde::{Deserialize, Serialize};
use swlsp_parser_php::query;
use swlsp_parser_php::syntax::PHP_STRING;
use swlsp_php::NameResolver;
use swlsp_php_types::go_to_lower;
use swlsp_syntax::{SyntaxNode, TextRange};

const TREE_BUILDER_CLASS: &str = "Symfony\\Component\\Config\\Definition\\Builder\\TreeBuilder";

/// A configuration root declared by a bundle's `getConfigTreeBuilder()`.
#[derive(Clone, Debug, Default, PartialEq, Eq, Serialize, Deserialize)]
pub struct Root {
    pub name: String,
    pub class: String,
    pub file: String,
    #[serde(with = "range_serde")]
    pub range: TextRange,
}

/// Roots named by `new TreeBuilder('name')` or the legacy `->root('name')`
/// inside each class's `getConfigTreeBuilder()`.
pub fn roots_in_php(path: &str, root: &SyntaxNode) -> Vec<Root> {
    let text = root.text().to_string();
    if !text.contains("getConfigTreeBuilder") || !text.contains("TreeBuilder") {
        return Vec::new();
    }
    let resolver = NameResolver::new(root);
    let mut result = Vec::new();
    for class in query::classes(root) {
        for method in query::methods(&class) {
            if !query::method_name(&method).eq_ignore_ascii_case("getConfigTreeBuilder") {
                continue;
            }
            let Some((name, range)) = tree_root_in_method(&method, &resolver) else {
                continue;
            };
            let class_name = resolver.resolve(&query::class_name(&class));
            result.push(Root {
                name,
                class: class_name
                    .strip_prefix('\\')
                    .unwrap_or(&class_name)
                    .to_owned(),
                file: path.to_owned(),
                range,
            });
        }
    }
    unique_roots(result)
}

fn tree_root_in_method(
    method: &SyntaxNode,
    resolver: &NameResolver,
) -> Option<(String, TextRange)> {
    let mut has_tree_builder = false;
    for object in query::object_creations(method, &[]) {
        let class_name = resolver.resolve(&query::object_class_name(&object));
        if query::function_like_at(&object).as_ref() != Some(method)
            || !class_name
                .strip_prefix('\\')
                .unwrap_or(&class_name)
                .eq_ignore_ascii_case(TREE_BUILDER_CLASS)
        {
            continue;
        }
        has_tree_builder = true;
        if let Some(found) = static_argument(&object) {
            return Some(found);
        }
    }
    if !has_tree_builder {
        return None;
    }
    query::calls(method, &[])
        .into_iter()
        .filter(|call| {
            query::function_like_at(call).as_ref() == Some(method)
                && query::call_method_name(call).eq_ignore_ascii_case("root")
        })
        .find_map(|call| static_argument(&call))
}

fn static_argument(call: &SyntaxNode) -> Option<(String, TextRange)> {
    let node = query::argument_expression(call, 0)?;
    let value = static_config_string(&node).filter(|value| !value.is_empty())?;
    Some((value, query::string_content_range(&node)))
}

/// The value of a single- or double-quoted string literal without
/// interpolation.
pub(crate) fn static_config_string(node: &SyntaxNode) -> Option<String> {
    if node.kind() != PHP_STRING {
        return None;
    }
    let text = node.text().to_string();
    let bytes = text.trim().as_bytes();
    if bytes.len() < 2 || !matches!(bytes[0], b'\'' | b'"') || bytes[bytes.len() - 1] != bytes[0] {
        return None;
    }
    if bytes[0] == b'"' && bytes[1..bytes.len() - 1].contains(&b'$') {
        return None;
    }
    Some(query::string_value(node))
}

fn unique_roots(values: Vec<Root>) -> Vec<Root> {
    let mut seen = HashSet::new();
    values
        .into_iter()
        .filter(|value| {
            !value.name.is_empty()
                && seen.insert((go_to_lower(&value.name), value.file.clone(), value.range))
        })
        .collect()
}

mod range_serde {
    use serde::{Deserialize, Deserializer, Serialize, Serializer};
    use swlsp_syntax::TextRange;

    pub fn serialize<S: Serializer>(value: &TextRange, serializer: S) -> Result<S::Ok, S::Error> {
        (u32::from(value.start()), u32::from(value.end())).serialize(serializer)
    }

    pub fn deserialize<'de, D: Deserializer<'de>>(deserializer: D) -> Result<TextRange, D::Error> {
        let (start, end) = <(u32, u32)>::deserialize(deserializer)?;
        if start > end {
            return Err(serde::de::Error::custom("invalid text range"));
        }
        Ok(TextRange::new(start.into(), end.into()))
    }
}
