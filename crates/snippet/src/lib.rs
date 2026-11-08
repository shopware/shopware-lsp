//! Storefront and Administration translation snippets (port of Go
//! `internal/snippet`).

mod admin_javascript_reference;
mod admin_twig_reference;
mod document_memo;
mod indexer;
mod json;

pub use admin_javascript_reference::{
    admin_javascript_string_reference, admin_javascript_string_references,
};
pub use admin_twig_reference::{
    AdminTwigReference, admin_twig_reference_at_offset, admin_twig_references,
};
pub use indexer::SnippetIndexer;

use std::collections::BTreeMap;

use serde::{Deserialize, Serialize};
use swlsp_parser_json::syntax::{
    JSON_BOOLEAN, JSON_NULL_VALUE, JSON_NUMBER, JSON_OBJECT, JSON_STRING,
};
use swlsp_syntax::{LineIndex, SyntaxNode, SyntaxNodeExt, Tree};

/// One flattened snippet key (`parent.child`) with its scalar text.
#[derive(Clone, Debug, Default, PartialEq, Eq, Serialize, Deserialize)]
pub struct Snippet {
    pub key: String,
    pub text: String,
    pub file: String,
    /// 1-based line of the value.
    pub line: u32,
}

/// Parses a snippet JSON document into flattened keys.
pub fn parse_snippet_file(document: &str, file_path: &str) -> BTreeMap<String, Snippet> {
    let parse = swlsp_parser_json::parse(document);
    let line_index = LineIndex::new(parse.tree.source_arc().clone());
    parse_snippet_tree(&parse.tree, &line_index, file_path)
}

/// Flattens nested objects into dotted keys; non-object documents yield no
/// snippets. A later duplicate key replaces an earlier one.
pub fn parse_snippet_tree(
    tree: &Tree,
    line_index: &LineIndex,
    file_path: &str,
) -> BTreeMap<String, Snippet> {
    let mut result = BTreeMap::new();
    if let Some(root) = json::root_value(&tree.root()).filter(|root| root.kind() == JSON_OBJECT) {
        extract_values("", &root, &mut result, file_path, line_index);
    }
    result
}

fn extract_values(
    prefix: &str,
    object: &SyntaxNode,
    result: &mut BTreeMap<String, Snippet>,
    file_path: &str,
    line_index: &LineIndex,
) {
    for pair in json::pairs(object) {
        let (Some(key), Some(value)) = (json::pair_key(&pair), json::pair_value(&pair)) else {
            continue;
        };
        let key = json::string_value(&key);
        let full_key = if prefix.is_empty() {
            key
        } else {
            format!("{prefix}.{key}")
        };
        match value.kind() {
            JSON_OBJECT => extract_values(&full_key, &value, result, file_path, line_index),
            JSON_STRING | JSON_NUMBER | JSON_BOOLEAN | JSON_NULL_VALUE => {
                let (line, _) = line_index.position(value.range_trimmed_trivia().start().into());
                let snippet = Snippet {
                    key: full_key.clone(),
                    text: json::scalar_text(&value),
                    file: file_path.to_owned(),
                    line: line + 1,
                };
                result.insert(full_key, snippet);
            }
            _ => {}
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn parse_snippet_file_flattens_nested_keys() {
        let path = concat!(env!("CARGO_MANIFEST_DIR"), "/testdata/nested.json");
        let content = std::fs::read_to_string(path).unwrap();
        let result = parse_snippet_file(&content, "testdata/nested.json");
        let expected: BTreeMap<String, Snippet> = [
            (
                "foo.foo.name",
                Snippet {
                    key: "foo.foo.name".into(),
                    text: "title".into(),
                    file: "testdata/nested.json".into(),
                    line: 5,
                },
            ),
            (
                "foo.name",
                Snippet {
                    key: "foo.name".into(),
                    text: "title".into(),
                    file: "testdata/nested.json".into(),
                    line: 3,
                },
            ),
        ]
        .into_iter()
        .map(|(key, snippet)| (key.to_owned(), snippet))
        .collect();
        assert_eq!(result, expected);
    }

    #[test]
    fn scalars_keep_their_text() {
        let result = parse_snippet_file(
            r#"{"a": 1, "b": true, "c": null, "d": [1], "e": "x\"y"}"#,
            "f.json",
        );
        let texts: Vec<(&str, &str)> = result
            .iter()
            .map(|(key, snippet)| (key.as_str(), snippet.text.as_str()))
            .collect();
        assert_eq!(
            texts,
            [("a", "1"), ("b", "true"), ("c", "null"), ("e", "x\\\"y")]
        );
        assert!(parse_snippet_file("[1]", "f.json").is_empty());
    }
}
