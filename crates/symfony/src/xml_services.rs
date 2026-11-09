//! Symfony XML service configuration.

use swlsp_parser_xml::query as xmlquery;
use swlsp_syntax::{LineIndex, SyntaxNode, SyntaxNodeExt, TextRange, Tree};

use crate::model::{Parameter, Service, ServicePrototype, TagMap};
use crate::util::{
    configured_service_bool, deprecated_config_value, deprecation_message, node_line,
    service_config_path, xml_attr, xml_attribute_content_range, xml_element_text,
};

/// Parses Symfony XML service definitions. Malformed editor input is
/// tolerated and any structurally complete definitions are returned.
pub fn parse_xml_services(path: &str, source: &str) -> (Vec<Service>, Vec<Parameter>) {
    let parse = swlsp_parser_xml::parse(source);
    let line_index = LineIndex::new(parse.tree.source_arc().clone());
    parse_xml_services_tree(path, Some(&parse.tree), &line_index)
}

pub fn parse_xml_services_tree(
    path: &str,
    tree: Option<&Tree>,
    line_index: &LineIndex,
) -> (Vec<Service>, Vec<Parameter>) {
    let (services, parameters, _) = parse_xml_service_config_tree(path, tree, line_index);
    (services, parameters)
}

pub(crate) fn parse_xml_service_config_tree(
    path: &str,
    tree: Option<&Tree>,
    line_index: &LineIndex,
) -> (Vec<Service>, Vec<Parameter>, Vec<ServicePrototype>) {
    let Some(tree) = tree else {
        return Default::default();
    };
    let root = tree.root();
    let Some(container) = xmlquery::elements(&root, &["container"]).into_iter().next() else {
        return Default::default();
    };
    let mut services = Vec::new();
    let mut parameters = Vec::new();
    let mut prototypes = Vec::new();
    for child in xmlquery::child_elements(&container, &[]) {
        match xmlquery::element_name(&child).as_deref().unwrap_or("") {
            "service" => {
                let service = process_xml_service(&child, line_index, path, false, false);
                if !service.id.is_empty() {
                    services.push(service);
                }
            }
            "alias" => {
                let alias = process_xml_alias(&child, line_index, path);
                if !alias.id.is_empty() {
                    services.push(alias);
                }
            }
            "services" => {
                let defaults = xmlquery::child_element(&child, &["defaults"]);
                let (default_autowire, default_autowire_set) =
                    xml_service_autowire(defaults.as_ref());
                for node in xmlquery::child_elements(&child, &["service"]) {
                    let service = process_xml_service(
                        &node,
                        line_index,
                        path,
                        default_autowire,
                        default_autowire_set,
                    );
                    if !service.id.is_empty() {
                        services.push(service);
                    }
                }
                for node in xmlquery::child_elements(&child, &["alias"]) {
                    let alias = process_xml_alias(&node, line_index, path);
                    if !alias.id.is_empty() {
                        services.push(alias);
                    }
                }
                for node in xmlquery::child_elements(&child, &["prototype"]) {
                    let prototype = process_xml_prototype(
                        &node,
                        line_index,
                        path,
                        default_autowire,
                        default_autowire_set,
                    );
                    if !prototype.namespace.is_empty() && !prototype.resource.is_empty() {
                        prototypes.push(prototype);
                    }
                }
            }
            "parameters" => {
                for node in xmlquery::child_elements(&child, &["parameter"]) {
                    let parameter = process_xml_parameter(&node, line_index, path);
                    if !parameter.name.is_empty() {
                        parameters.push(parameter);
                    }
                }
            }
            "parameter" => {
                let parameter = process_xml_parameter(&child, line_index, path);
                if !parameter.name.is_empty() {
                    parameters.push(parameter);
                }
            }
            _ => {}
        }
    }
    (services, parameters, prototypes)
}

fn attr_range(node: &SyntaxNode, name: &str) -> TextRange {
    xml_attribute_content_range(xmlquery::attribute(node, name).as_ref())
}

fn tag_names(node: &SyntaxNode) -> TagMap {
    let mut tags = TagMap::new();
    for tag in xmlquery::child_elements(node, &["tag"]) {
        let name = xml_attr(&tag, "name");
        if !name.is_empty() {
            tags.insert(name, String::new());
        }
    }
    tags
}

fn process_xml_service(
    node: &SyntaxNode,
    line_index: &LineIndex,
    path: &str,
    default_autowire: bool,
    default_autowire_set: bool,
) -> Service {
    let attributes = xmlquery::attribute_values(node);
    let get = |name: &str| attributes.get(name).cloned().unwrap_or_default();
    let id = get("id");
    if id.is_empty() || id.contains(' ') {
        return Service::default();
    }
    let mut class = get("class");
    let mut class_range = attr_range(node, "class");
    if class.is_empty() && get("alias").is_empty() {
        class = id.clone();
        class_range = attr_range(node, "id");
    }
    let (deprecated_value, deprecated_range) = xml_service_deprecation(node, &attributes);
    let mut service = Service {
        id_range: attr_range(node, "id"),
        id,
        class,
        alias_target: get("alias"),
        decorates: get("decorates"),
        parent: get("parent"),
        autowire: default_autowire,
        autowire_set: default_autowire_set,
        deprecated: deprecated_config_value(&deprecated_value),
        deprecation: deprecation_message(&deprecated_value),
        tags: TagMap::new(),
        path: path.to_owned(),
        line: node_line(line_index, node),
        range: node.range_trimmed_trivia(),
        class_range,
        decorates_range: attr_range(node, "decorates"),
        parent_range: attr_range(node, "parent"),
        deprecated_range,
        ..Service::default()
    };
    if let Some(value) = attributes.get("autowire") {
        service.autowire = configured_service_bool(value).0;
        service.autowire_set = true;
    }
    service.tags = tag_names(node);
    service
}

fn xml_service_deprecation(
    node: &SyntaxNode,
    attributes: &std::collections::BTreeMap<String, String>,
) -> (String, TextRange) {
    let value = attributes.get("deprecated").cloned().unwrap_or_default();
    let value_range = attr_range(node, "deprecated");
    if let Some(deprecated) = xmlquery::child_element(node, &["deprecated"]) {
        let (text, text_range) = xml_element_text(&deprecated);
        if !text.is_empty() {
            return (text, text_range);
        }
        return ("true".to_owned(), deprecated.range_trimmed_trivia());
    }
    (value, value_range)
}

fn process_xml_alias(node: &SyntaxNode, line_index: &LineIndex, path: &str) -> Service {
    let attributes = xmlquery::attribute_values(node);
    let get = |name: &str| attributes.get(name).cloned().unwrap_or_default();
    if get("id").is_empty() || get("service").is_empty() {
        return Service::default();
    }
    let (deprecated_value, deprecated_range) = xml_service_deprecation(node, &attributes);
    Service {
        id: get("id"),
        alias_target: get("service"),
        deprecated: deprecated_config_value(&deprecated_value),
        deprecation: deprecation_message(&deprecated_value),
        path: path.to_owned(),
        line: node_line(line_index, node),
        range: node.range_trimmed_trivia(),
        id_range: attr_range(node, "id"),
        deprecated_range,
        ..Service::default()
    }
}

fn process_xml_prototype(
    node: &SyntaxNode,
    line_index: &LineIndex,
    path: &str,
    default_autowire: bool,
    default_autowire_set: bool,
) -> ServicePrototype {
    let namespace = xml_attr(node, "namespace")
        .trim()
        .strip_prefix('\\')
        .map(str::to_owned)
        .unwrap_or_else(|| xml_attr(node, "namespace").trim().to_owned());
    let resource = service_config_path(path, &xml_attr(node, "resource"));
    let mut prototype = ServicePrototype {
        namespace,
        resource,
        autowire: default_autowire,
        autowire_set: default_autowire_set,
        path: path.to_owned(),
        line: node_line(line_index, node),
        range: node.range_trimmed_trivia(),
        namespace_range: attr_range(node, "namespace"),
        resource_range: attr_range(node, "resource"),
        ..ServicePrototype::default()
    };
    if let Some(value) = xmlquery::attribute(node, "autowire") {
        prototype.autowire =
            configured_service_bool(&xmlquery::attribute_value(&value).unwrap_or_default()).0;
        prototype.autowire_set = true;
    }
    let exclude = xml_attr(node, "exclude");
    let exclude = exclude.trim();
    if !exclude.is_empty() {
        prototype.excludes = vec![service_config_path(path, exclude)];
    }
    prototype.tags = tag_names(node);
    prototype
}

fn xml_service_autowire(node: Option<&SyntaxNode>) -> (bool, bool) {
    let Some(node) = node else {
        return (false, false);
    };
    let Some(attribute) = xmlquery::attribute(node, "autowire") else {
        return (false, false);
    };
    (
        configured_service_bool(&xmlquery::attribute_value(&attribute).unwrap_or_default()).0,
        true,
    )
}

fn process_xml_parameter(node: &SyntaxNode, line_index: &LineIndex, path: &str) -> Parameter {
    let attributes = xmlquery::attribute_values(node);
    let get = |name: &str| attributes.get(name).cloned().unwrap_or_default();
    let name = get("key");
    if name.is_empty() {
        return Parameter::default();
    }
    let value = if get("type") == "service" && !get("id").is_empty() {
        format!("@{}", get("id"))
    } else if !get("value").is_empty() {
        get("value")
    } else {
        xmlquery::text_content(node).trim().to_owned()
    };
    Parameter {
        name,
        value,
        path: path.to_owned(),
        line: node_line(line_index, node),
    }
}
