//! Symfony UX Twig Component metadata from the compiled container.

use std::collections::HashMap;

use swlsp_parser_xml::query as xmlquery;
use swlsp_syntax::{TextRange, Tree};

use crate::model::ContainerTwigComponent;
use crate::util::{xml_attr, xml_attribute_content_range, xml_element_text};

const TWIG_COMPONENT_FACTORY_SERVICE: &str = "ux.twig_component.component_factory";

pub fn parse_xml_twig_components(path: &str, source: &str) -> Vec<ContainerTwigComponent> {
    let parse = swlsp_parser_xml::parse(source);
    parse_xml_twig_components_tree(path, Some(&parse.tree))
}

pub fn parse_xml_twig_components_tree(
    path: &str,
    tree: Option<&Tree>,
) -> Vec<ContainerTwigComponent> {
    let Some(tree) = tree else {
        return Vec::new();
    };
    let mut result: Vec<ContainerTwigComponent> = Vec::new();
    let mut by_name: HashMap<String, usize> = HashMap::new();
    for service in xmlquery::elements(&tree.root(), &["service"]) {
        if xml_attr(&service, "id") != TWIG_COMPONENT_FACTORY_SERVICE {
            continue;
        }
        let arguments = xmlquery::child_elements(&service, &["argument"]);
        if arguments.len() < 5 {
            continue;
        }
        for component in xmlquery::child_elements(&arguments[4], &["argument"]) {
            let mut name = xml_attr(&component, "key").trim().to_owned();
            let mut name_range =
                xml_attribute_content_range(xmlquery::attribute(&component, "key").as_ref());
            let mut values: HashMap<String, String> = HashMap::new();
            let mut ranges: HashMap<String, TextRange> = HashMap::new();
            for value in xmlquery::child_elements(&component, &["argument"]) {
                let key = xml_attr(&value, "key").trim().to_owned();
                if key.is_empty() {
                    continue;
                }
                let (text, range) = xml_element_text(&value);
                values.insert(key.clone(), text);
                ranges.insert(key, range);
            }
            let value = |key: &str| values.get(key).map(String::as_str).unwrap_or("");
            let range = |key: &str| ranges.get(key).copied().unwrap_or_default();
            let configured = value("key").trim();
            if !configured.is_empty() {
                name = configured.to_owned();
                name_range = range("key");
            }
            if name.is_empty() {
                continue;
            }
            let entry = ContainerTwigComponent {
                name: name.clone(),
                class: normalize_container_component_class(value("class")),
                template: value("template").trim().to_owned(),
                template_from_method: value("template_from_method").trim().to_owned(),
                path: path.to_owned(),
                name_range,
                class_range: range("class"),
                template_range: range("template"),
            };
            match by_name.get(&name) {
                Some(&index) => result[index] = entry,
                None => {
                    by_name.insert(name, result.len());
                    result.push(entry);
                }
            }
        }
        if arguments.len() < 6 {
            continue;
        }
        for mapping in xmlquery::child_elements(&arguments[5], &["argument"]) {
            let class = normalize_container_component_class(&xml_attr(&mapping, "key"));
            let (name, name_range) = xml_element_text(&mapping);
            if class.is_empty() || name.is_empty() {
                continue;
            }
            let class_range =
                xml_attribute_content_range(xmlquery::attribute(&mapping, "key").as_ref());
            if let Some(&index) = by_name.get(&name) {
                if result[index].class.is_empty() {
                    result[index].class = class;
                    result[index].class_range = class_range;
                }
                continue;
            }
            by_name.insert(name.clone(), result.len());
            result.push(ContainerTwigComponent {
                name,
                class,
                path: path.to_owned(),
                name_range,
                class_range,
                ..ContainerTwigComponent::default()
            });
        }
    }
    result
}

fn normalize_container_component_class(value: &str) -> String {
    value.trim().trim_matches('\\').to_owned()
}
