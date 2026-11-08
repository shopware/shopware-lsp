//! Symfony YAML service configuration.

use swlsp_parser_yaml::query as yamlquery;
use swlsp_parser_yaml::syntax as yamlsyntax;
use swlsp_syntax::{LineIndex, SyntaxNode, SyntaxNodeExt, TextRange, Tree};

use crate::model::{Parameter, Service, ServicePrototype, TagMap};
use crate::util::{
    configured_service_bool, deprecated_config_value, deprecation_message, line_at,
    service_config_path, yaml_scalar_content_range,
};

/// Parses Symfony YAML service definitions and parameters.
pub fn parse_yaml_services(path: &str, source: &str) -> (Vec<Service>, Vec<Parameter>) {
    let parse = swlsp_parser_yaml::parse(source);
    let line_index = LineIndex::new(parse.tree.source_arc().clone());
    parse_yaml_services_tree(path, Some(&parse.tree), &line_index)
}

pub fn parse_yaml_services_tree(
    path: &str,
    tree: Option<&Tree>,
    line_index: &LineIndex,
) -> (Vec<Service>, Vec<Parameter>) {
    let (services, parameters, _) = parse_yaml_service_config_tree(path, tree, line_index);
    (services, parameters)
}

/// Go `yamlquery.Property` tolerating a missing mapping.
pub(crate) fn yaml_property(mapping: Option<&SyntaxNode>, name: &str) -> Option<SyntaxNode> {
    mapping.and_then(|mapping| yamlquery::property(mapping, name))
}

/// Go `yamlquery.ScalarValue` tolerating a missing node.
pub(crate) fn yaml_scalar(node: Option<&SyntaxNode>) -> String {
    node.map(yamlquery::scalar_value).unwrap_or_default()
}

pub(crate) fn yaml_is_mapping(node: Option<&SyntaxNode>) -> bool {
    node.is_some_and(yamlquery::is_mapping)
}

pub(crate) fn parse_yaml_service_config_tree(
    path: &str,
    tree: Option<&Tree>,
    line_index: &LineIndex,
) -> (Vec<Service>, Vec<Parameter>, Vec<ServicePrototype>) {
    let Some(tree) = tree else {
        return Default::default();
    };
    let root = yamlquery::root_value(&tree.root());
    if !yaml_is_mapping(root.as_ref()) {
        return Default::default();
    }
    let (services, prototypes) = parse_yaml_services_mapping(
        yaml_property(root.as_ref(), "services").as_ref(),
        path,
        line_index,
    );
    let parameters = parse_yaml_parameters(
        yaml_property(root.as_ref(), "parameters").as_ref(),
        path,
        line_index,
    );
    (services, parameters, prototypes)
}

fn parse_yaml_services_mapping(
    mapping: Option<&SyntaxNode>,
    path: &str,
    line_index: &LineIndex,
) -> (Vec<Service>, Vec<ServicePrototype>) {
    let Some(mapping) = mapping.filter(|node| yamlquery::is_mapping(node)) else {
        return Default::default();
    };
    let mut services = Vec::new();
    let mut prototypes = Vec::new();
    let defaults = yamlquery::property(mapping, "_defaults");
    let (default_autowire, default_autowire_set) =
        yaml_service_autowire(yaml_property(defaults.as_ref(), "autowire").as_ref());
    for pair in yamlquery::pairs(mapping) {
        let key = yamlquery::pair_key(&pair);
        let service_id = yaml_scalar(key.as_ref());
        if service_id.is_empty() || service_id.starts_with('_') {
            continue;
        }
        let Some(key) = key else { continue };
        let line = line_at(line_index, key.range_trimmed_trivia().start());
        let value = yamlquery::pair_value(&pair);
        if service_id.ends_with('\\')
            && let Some(value) = value.as_ref().filter(|value| yamlquery::is_mapping(value))
            && yamlquery::property(value, "resource").is_some()
        {
            let prototype = parse_yaml_prototype(
                &service_id,
                &key,
                value,
                path,
                line,
                default_autowire,
                default_autowire_set,
            );
            if !prototype.resource.is_empty() {
                prototypes.push(prototype);
            }
            continue;
        }
        let mut service = Service {
            id_range: yaml_scalar_content_range(Some(&key)),
            id: service_id,
            path: path.to_owned(),
            line,
            range: pair.range_trimmed_trivia(),
            autowire: default_autowire,
            autowire_set: default_autowire_set,
            ..Service::default()
        };
        match value.as_ref() {
            Some(value) if yamlquery::is_mapping(value) => {
                parse_yaml_service_config(&mut service, value)
            }
            Some(value)
                if value.kind() == yamlsyntax::YAML_SCALAR && !yamlquery::is_null(value) =>
            {
                let scalar = yamlquery::scalar_value(value);
                if let Some(target) = scalar.strip_prefix('@') {
                    service.alias_target = target.to_owned();
                } else {
                    service.class = scalar;
                    service.class_range = yaml_scalar_content_range(Some(value));
                }
            }
            _ => {}
        }
        if service.class.is_empty() && service.alias_target.is_empty() {
            service.class = service.id.clone();
            service.class_range = service.id_range;
        }
        services.push(service);
    }
    (services, prototypes)
}

fn trim_at(value: String) -> String {
    match value.strip_prefix('@') {
        Some(rest) => rest.to_owned(),
        None => value,
    }
}

fn parse_yaml_service_config(service: &mut Service, mapping: &SyntaxNode) {
    if let Some(value) = yamlquery::property(mapping, "autowire") {
        service.autowire = configured_service_bool(&yamlquery::scalar_value(&value)).0;
        service.autowire_set = true;
    }
    if let Some(value) = yamlquery::property(mapping, "class") {
        service.class = yamlquery::scalar_value(&value);
        service.class_range = yaml_scalar_content_range(Some(&value));
    }
    if let Some(value) = yamlquery::property(mapping, "alias") {
        service.alias_target = trim_at(yamlquery::scalar_value(&value));
    }
    if let Some(value) = yamlquery::property(mapping, "decorates") {
        service.decorates = trim_at(yamlquery::scalar_value(&value));
        service.decorates_range = yaml_scalar_content_range(Some(&value));
    }
    if let Some(value) = yamlquery::property(mapping, "parent") {
        service.parent = trim_at(yamlquery::scalar_value(&value));
        service.parent_range = yaml_scalar_content_range(Some(&value));
    }
    if let Some(value) = yamlquery::property(mapping, "deprecated") {
        let raw = yamlquery::scalar_value(&value);
        service.deprecated = deprecated_config_value(&raw);
        service.deprecation = deprecation_message(&raw);
        service.deprecated_range = yaml_scalar_content_range(Some(&value));
    }
    if let Some(value) = yamlquery::property(mapping, "tags") {
        parse_yaml_tags(&mut service.tags, &value);
    }
}

fn parse_yaml_prototype(
    namespace: &str,
    namespace_node: &SyntaxNode,
    mapping: &SyntaxNode,
    path: &str,
    line: u32,
    default_autowire: bool,
    default_autowire_set: bool,
) -> ServicePrototype {
    let resource_node = yamlquery::property(mapping, "resource");
    let mut prototype = ServicePrototype {
        namespace: namespace.strip_prefix('\\').unwrap_or(namespace).to_owned(),
        resource: service_config_path(path, &yaml_scalar(resource_node.as_ref())),
        autowire: default_autowire,
        autowire_set: default_autowire_set,
        path: path.to_owned(),
        line,
        range: TextRange::new(
            namespace_node.range_trimmed_trivia().start(),
            mapping.range_trimmed_trivia().end(),
        ),
        namespace_range: yaml_scalar_content_range(Some(namespace_node)),
        resource_range: yaml_scalar_content_range(resource_node.as_ref()),
        ..ServicePrototype::default()
    };
    if let Some(value) = yamlquery::property(mapping, "autowire") {
        prototype.autowire = configured_service_bool(&yamlquery::scalar_value(&value)).0;
        prototype.autowire_set = true;
    }
    if let Some(exclude) = yamlquery::property(mapping, "exclude") {
        for value in yaml_string_values(Some(&exclude)) {
            prototype.excludes.push(service_config_path(path, &value));
        }
    }
    if let Some(tags) = yamlquery::property(mapping, "tags") {
        parse_yaml_tags(&mut prototype.tags, &tags);
    }
    prototype
}

fn yaml_service_autowire(node: Option<&SyntaxNode>) -> (bool, bool) {
    match node {
        None => (false, false),
        Some(node) => (
            configured_service_bool(&yamlquery::scalar_value(node)).0,
            true,
        ),
    }
}

fn parse_yaml_tags(tags: &mut TagMap, value: &SyntaxNode) {
    if yamlquery::is_sequence(value) {
        for item in yamlquery::items(value) {
            if let Some(value) = yamlquery::item_value(&item) {
                parse_yaml_tag(tags, &value);
            }
        }
        return;
    }
    parse_yaml_tag(tags, value);
}

fn parse_yaml_tag(tags: &mut TagMap, value: &SyntaxNode) {
    let tag = match value.kind() {
        yamlsyntax::YAML_SCALAR => yamlquery::scalar_value(value),
        yamlsyntax::YAML_FLOW_MAPPING | yamlsyntax::YAML_MAPPING => {
            yaml_scalar(yamlquery::property(value, "name").as_ref())
        }
        _ => String::new(),
    };
    if !tag.is_empty() {
        tags.insert(tag, String::new());
    }
}

fn parse_yaml_parameters(
    mapping: Option<&SyntaxNode>,
    path: &str,
    line_index: &LineIndex,
) -> Vec<Parameter> {
    let Some(mapping) = mapping.filter(|node| yamlquery::is_mapping(node)) else {
        return Vec::new();
    };
    let mut parameters = Vec::new();
    for pair in yamlquery::pairs(mapping) {
        let Some(key) = yamlquery::pair_key(&pair) else {
            continue;
        };
        let name = yamlquery::scalar_value(&key);
        if name.is_empty() {
            continue;
        }
        let line = line_at(line_index, key.range_trimmed_trivia().start());
        let mut parameter = Parameter {
            name,
            path: path.to_owned(),
            line,
            ..Parameter::default()
        };
        if let Some(value) = yamlquery::pair_value(&pair)
            && value.kind() == yamlsyntax::YAML_SCALAR
            && !yamlquery::is_null(&value)
        {
            parameter.value = yamlquery::scalar_value(&value);
        }
        parameters.push(parameter);
    }
    parameters
}

pub(crate) fn yaml_string_values(node: Option<&SyntaxNode>) -> Vec<String> {
    let Some(node) = node else {
        return Vec::new();
    };
    if yamlquery::is_sequence(node) {
        return yamlquery::items(node)
            .map(|item| yaml_scalar(yamlquery::item_value(&item).as_ref()))
            .filter(|value| !value.is_empty())
            .collect();
    }
    let value = yamlquery::scalar_value(node);
    if value.is_empty() {
        Vec::new()
    } else {
        vec![value]
    }
}
