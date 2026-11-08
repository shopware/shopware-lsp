//! Twig globals registered on the compiled `twig` service.

use swlsp_parser_xml::query as xmlquery;
use swlsp_syntax::Tree;

use crate::model::ContainerTwigGlobal;
use crate::util::{xml_attr, xml_element_text};

pub fn parse_xml_twig_globals(path: &str, source: &str) -> Vec<ContainerTwigGlobal> {
    let parse = swlsp_parser_xml::parse(source);
    parse_xml_twig_globals_tree(path, Some(&parse.tree))
}

pub fn parse_xml_twig_globals_tree(path: &str, tree: Option<&Tree>) -> Vec<ContainerTwigGlobal> {
    let Some(tree) = tree else {
        return Vec::new();
    };
    let mut result = Vec::new();
    for service in xmlquery::elements(&tree.root(), &["service"]) {
        if xml_attr(&service, "id") != "twig" {
            continue;
        }
        for call in xmlquery::child_elements(&service, &["call"]) {
            if xml_attr(&call, "method") != "addGlobal" {
                continue;
            }
            let arguments = xmlquery::child_elements(&call, &["argument"]);
            if arguments.len() != 2 {
                continue;
            }
            let (name, range) = xml_element_text(&arguments[0]);
            if name.is_empty() {
                continue;
            }
            let mut value = ContainerTwigGlobal {
                name,
                path: path.to_owned(),
                range,
                ..ContainerTwigGlobal::default()
            };
            let attributes = xmlquery::attribute_values(&arguments[1]);
            let id = attributes.get("id").map(String::as_str).unwrap_or("");
            let kind = attributes.get("type").map(String::as_str).unwrap_or("");
            if !id.is_empty() && (kind == "service" || kind.is_empty()) {
                value.service_id = id.to_owned();
            } else {
                value.value = xml_element_text(&arguments[1]).0;
            }
            result.push(value);
        }
    }
    result
}
