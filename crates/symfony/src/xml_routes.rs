//! Symfony routing XML.

use swlsp_parser_xml::query as xmlquery;
use swlsp_syntax::{LineIndex, SyntaxNodeExt, Tree};

use crate::model::Route;
use crate::util::{line_at, xml_attr};
use crate::yaml_routes::split_methods;

/// Parses Symfony routing XML using the native lossless frontend.
pub fn parse_xml_routes(file_path: &str, source: &str) -> Vec<Route> {
    let parse = swlsp_parser_xml::parse(source);
    let line_index = LineIndex::new(parse.tree.source_arc().clone());
    parse_xml_routes_tree(file_path, Some(&parse.tree), &line_index)
}

pub fn parse_xml_routes_tree(
    file_path: &str,
    tree: Option<&Tree>,
    line_index: &LineIndex,
) -> Vec<Route> {
    let Some(tree) = tree else {
        return Vec::new();
    };
    let Some(root) = xmlquery::elements(&tree.root(), &["routes"])
        .into_iter()
        .next()
    else {
        return Vec::new();
    };
    let mut routes = Vec::new();
    for node in xmlquery::child_elements(&root, &["route"]) {
        let attributes = xmlquery::attribute_values(&node);
        let get = |name: &str| attributes.get(name).cloned().unwrap_or_default();
        let name = get("id");
        let path = swlsp_admin_twigmigration::unescape_string(&get("path"));
        if name.is_empty() || path.is_empty() {
            continue;
        }
        let mut controller = get("controller");
        if controller.is_empty() {
            for default in xmlquery::child_elements(&node, &["default"]) {
                if xml_attr(&default, "key") == "_controller" {
                    controller = xmlquery::text_content(&default).trim().to_owned();
                    break;
                }
            }
        }
        routes.push(Route {
            name,
            path,
            controller,
            methods: split_methods(&get("methods")),
            file_path: file_path.to_owned(),
            line: line_at(line_index, node.range_trimmed_trivia().start()),
            ..Route::default()
        });
    }
    routes
}

#[cfg(test)]
#[path = "tests_routes/xml_routes.rs"]
mod tests;
