//! Symfony YAML route definitions.

use swlsp_parser_yaml::query as yamlquery;
use swlsp_syntax::{LineIndex, SyntaxNode, SyntaxNodeExt, Tree};

use crate::model::Route;
use crate::util::line_at;
use crate::yaml_services::{yaml_property, yaml_scalar};

/// Parses Symfony YAML route definitions.
pub fn parse_yaml_routes(file_path: &str, source: &str) -> Vec<Route> {
    let parse = swlsp_parser_yaml::parse(source);
    let line_index = LineIndex::new(parse.tree.source_arc().clone());
    parse_yaml_routes_tree(file_path, Some(&parse.tree), &line_index)
}

pub fn parse_yaml_routes_tree(
    file_path: &str,
    tree: Option<&Tree>,
    line_index: &LineIndex,
) -> Vec<Route> {
    let Some(tree) = tree else {
        return Vec::new();
    };
    let Some(root) = yamlquery::root_value(&tree.root()).filter(yamlquery::is_mapping) else {
        return Vec::new();
    };
    let mut routes = Vec::new();
    for pair in yamlquery::pairs(&root) {
        let Some(key) = yamlquery::pair_key(&pair) else {
            continue;
        };
        let name = yamlquery::scalar_value(&key);
        if name.is_empty() || name.starts_with('_') {
            continue;
        }
        let Some(definition) = yamlquery::pair_value(&pair).filter(yamlquery::is_mapping) else {
            continue;
        };
        let path_node = yamlquery::property(&definition, "path");
        if path_node.as_ref().is_some_and(yamlquery::is_null) {
            continue;
        }
        let path = yaml_scalar(path_node.as_ref());
        if path.is_empty() {
            continue;
        }
        routes.push(Route {
            name,
            path,
            controller: yaml_scalar(yaml_property(Some(&definition), "controller").as_ref()),
            methods: yaml_route_methods(yamlquery::property(&definition, "methods").as_ref()),
            file_path: file_path.to_owned(),
            line: line_at(line_index, key.range_trimmed_trivia().start()),
            ..Route::default()
        });
    }
    routes
}

fn yaml_route_methods(node: Option<&SyntaxNode>) -> Vec<String> {
    let Some(node) = node else {
        return Vec::new();
    };
    if yamlquery::is_sequence(node) {
        return yamlquery::items(node)
            .map(|item| yaml_scalar(yamlquery::item_value(&item).as_ref()))
            .filter(|method| !method.is_empty())
            .collect();
    }
    split_methods(&yamlquery::scalar_value(node))
}

/// Go `strings.FieldsFunc` on `|`, `,` and space.
pub(crate) fn split_methods(value: &str) -> Vec<String> {
    value
        .split(['|', ',', ' '])
        .filter(|method| !method.is_empty())
        .map(str::to_owned)
        .collect()
}

#[cfg(test)]
#[path = "tests_routes/yaml_routes.rs"]
mod tests;
