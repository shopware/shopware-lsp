use std::collections::{HashMap, HashSet};

use swlsp_parser_js::query as js;
use swlsp_parser_js::syntax::{
    JS_IDENTIFIER, JS_OBJECT, JS_PROPERTY, JS_STRING, JS_VARIABLE_DECLARATION,
};
use swlsp_syntax::{SyntaxNode, TextRange, Tree};

use crate::model::{Kind, Resource};
use crate::parser::{javascript_string_range, resolve_relative_target};
use crate::paths::dir;

/// Entries of every `input: {...}` object (or object variable) in a Vite
/// config; spread variables are followed.
pub(crate) fn parse_vite_config(path: &str, tree: Option<&Tree>) -> Vec<Resource> {
    let Some(tree) = tree else {
        return Vec::new();
    };
    let root = tree.root();
    let variables = object_variables(&root);
    let mut result = Vec::new();
    let mut seen = HashSet::new();
    for property in js::nodes(&root, &[JS_PROPERTY]) {
        if js::property_name(&property).as_deref() != Some("input") {
            continue;
        }
        let object = object_value(js::property_value(&property).as_ref(), &variables);
        collect_entries(
            path,
            object,
            &variables,
            &mut seen,
            &mut HashSet::new(),
            &mut result,
        );
    }
    result
}

fn object_variables(root: &SyntaxNode) -> HashMap<String, SyntaxNode> {
    let mut result = HashMap::new();
    for declaration in js::nodes(root, &[JS_VARIABLE_DECLARATION]) {
        let mut name = String::new();
        let mut object = None;
        for child in declaration.children() {
            match child.kind() {
                JS_IDENTIFIER if name.is_empty() => {
                    name = js::identifier_text(&child).unwrap_or_default();
                }
                JS_OBJECT if object.is_none() => object = Some(child),
                _ => {}
            }
        }
        if let Some(object) = object.filter(|_| !name.is_empty()) {
            result.insert(name, object);
        }
    }
    result
}

fn object_value(
    value: Option<&SyntaxNode>,
    variables: &HashMap<String, SyntaxNode>,
) -> Option<SyntaxNode> {
    let value = value?;
    match value.kind() {
        JS_OBJECT => Some(value.clone()),
        JS_IDENTIFIER => variables
            .get(&js::identifier_text(value).unwrap_or_default())
            .cloned(),
        _ => None,
    }
}

fn collect_entries(
    path: &str,
    object: Option<SyntaxNode>,
    variables: &HashMap<String, SyntaxNode>,
    seen: &mut HashSet<String>,
    visited: &mut HashSet<SyntaxNode>,
    result: &mut Vec<Resource>,
) {
    let Some(object) = object else {
        return;
    };
    if !visited.insert(object.clone()) {
        return;
    }
    for property in js::properties(&object) {
        let name = js::property_name(&property).unwrap_or_default();
        if property.text().to_string().trim().starts_with("...") {
            collect_entries(
                path,
                variables.get(&name).cloned(),
                variables,
                seen,
                visited,
                result,
            );
            continue;
        }
        if name.is_empty() || !seen.insert(name.to_lowercase()) {
            continue;
        }
        let target = js::property_value(&property)
            .filter(|value| value.kind() == JS_STRING)
            .map_or_else(String::new, |value| {
                resolve_relative_target(&dir(path), &js::string_value(&value))
            });
        result.push(Resource {
            name,
            file: path.to_owned(),
            target,
            kind: Kind::ViteEntry,
            range: js::property_name_node(&property).map_or_else(TextRange::default, |name| {
                javascript_string_range(Some(&name))
            }),
            ..Resource::default()
        });
    }
}
