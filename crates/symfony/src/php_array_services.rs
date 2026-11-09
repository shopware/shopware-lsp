//! Symfony's native/legacy PHP array service configuration
//! (`return ['services' => [...]]` and `App::config([...])`).

use std::collections::HashMap;

use swlsp_parser_php::query as phpquery;
use swlsp_parser_php::syntax as phpsyntax;
use swlsp_php::NameResolver;
use swlsp_syntax::{LineIndex, SyntaxNode, SyntaxNodeExt, TextRange};

use crate::model::{Parameter, PhpServiceConfig, Service, ServicePrototype, TagMap};
use crate::php_array_context::php_array_accepted_root;
use crate::php_services::{
    config_path, is_static_parameter_expression, php_expression_content_range, sort_parameters,
    sort_services, static_php_value,
};
use crate::reference::parse_service_reference;
use crate::util::{configured_service_bool, line_at};

pub(crate) fn parse_php_array_service_config(
    path: &str,
    root: &SyntaxNode,
    line_index: &LineIndex,
) -> PhpServiceConfig {
    let resolver = NameResolver::new(root);
    let mut services: HashMap<String, Service> = HashMap::new();
    let mut parameters: HashMap<String, Parameter> = HashMap::new();
    let mut prototypes = Vec::new();
    for config in php_array_config_roots(root) {
        for entry in phpquery::array_items(&config) {
            let key =
                php_array_static_value(phpquery::array_item_key(&entry).as_ref(), &resolver, path);
            let value = php_array_node(phpquery::array_item_value(&entry));
            match key.as_str() {
                "parameters" => parse_php_array_parameters(
                    path,
                    value.as_ref(),
                    line_index,
                    &resolver,
                    &mut parameters,
                ),
                "services" => {
                    let (parsed, parsed_prototypes) =
                        parse_php_array_services(path, value.as_ref(), line_index, &resolver);
                    services.extend(parsed);
                    prototypes.extend(parsed_prototypes);
                }
                _ => {}
            }
        }
    }
    let mut result = PhpServiceConfig {
        services: services.into_values().collect(),
        parameters: parameters.into_values().collect(),
        prototypes,
        ..PhpServiceConfig::default()
    };
    sort_services(&mut result.services);
    sort_parameters(&mut result.parameters);
    result
}

pub(crate) fn php_array_config_roots(root: &SyntaxNode) -> Vec<SyntaxNode> {
    phpquery::arrays(root)
        .into_iter()
        .filter(|array| php_array_accepted_root(Some(array)))
        .collect()
}

fn items(array: Option<&SyntaxNode>) -> Vec<SyntaxNode> {
    array.map(phpquery::array_items).unwrap_or_default()
}

fn parse_php_array_parameters(
    path: &str,
    array: Option<&SyntaxNode>,
    line_index: &LineIndex,
    resolver: &NameResolver,
    result: &mut HashMap<String, Parameter>,
) {
    for item in items(array) {
        let name = php_array_static_value(phpquery::array_item_key(&item).as_ref(), resolver, path);
        if name.is_empty() {
            continue;
        }
        let expression = phpquery::array_item_value(&item);
        let mut value = php_array_static_value_ok(expression.as_ref(), resolver, path);
        if value.is_none()
            && let Some(expression) = expression.as_ref()
        {
            let text = expression.text().to_string();
            if is_static_parameter_expression(&text) {
                value = Some(text.trim().to_owned());
            }
        }
        result.insert(
            name.clone(),
            Parameter {
                name,
                value: value.unwrap_or_default(),
                path: path.to_owned(),
                line: php_array_line(line_index, &item),
            },
        );
    }
}

fn parse_php_array_services(
    path: &str,
    array: Option<&SyntaxNode>,
    line_index: &LineIndex,
    resolver: &NameResolver,
) -> (HashMap<String, Service>, Vec<ServicePrototype>) {
    let mut result = HashMap::new();
    let Some(array) = array else {
        return (result, Vec::new());
    };
    let default_tags = php_array_default_tags(array, resolver, path);
    let (default_autowire, default_autowire_set) = php_array_autowire(
        php_array_entry_array(Some(array), "_defaults", resolver, path).as_ref(),
        resolver,
        path,
    );
    let instanceof_tags = php_array_instanceof_tags(array, resolver, path);
    let mut prototypes = Vec::new();
    for item in phpquery::array_items(array) {
        let id_node = phpquery::array_item_key(&item);
        let id = php_array_static_value(id_node.as_ref(), resolver, path);
        if id.is_empty() || id.starts_with('_') {
            continue;
        }
        let Some(value) = phpquery::array_item_value(&item) else {
            continue;
        };
        if let Some(definition) = php_array_node(Some(value.clone())) {
            let options = php_array_options(&definition, resolver, path);
            let option = |name: &str| options.get(name).map(String::as_str);
            let option_node =
                |name: &str| php_array_option_value(Some(&definition), name, resolver, path);
            if let Some(resource) = option("resource")
                && id.ends_with('\\')
                && let Some(id_node) = id_node.as_ref()
            {
                prototypes.push(php_array_prototype(
                    path,
                    &id,
                    id_node,
                    resource,
                    &definition,
                    line_index,
                    resolver,
                    &default_tags,
                    &instanceof_tags,
                    default_autowire,
                    default_autowire_set,
                ));
                continue;
            }
            let mut service = Service {
                id: id.clone(),
                autowire: default_autowire,
                autowire_set: default_autowire_set,
                tags: default_tags.clone(),
                instanceof_tags: instanceof_tags.clone(),
                path: path.to_owned(),
                line: php_array_line(line_index, &item),
                range: item.range_trimmed_trivia(),
                id_range: php_expression_content_range(id_node.as_ref()),
                deprecated_range: TextRange::default(),
                ..Service::default()
            };
            if let Some(autowire) = option_node("autowire") {
                service.autowire = configured_service_bool(&php_array_static_value(
                    Some(&autowire),
                    resolver,
                    path,
                ))
                .0;
                service.autowire_set = true;
            }
            if let Some(class) = option("class") {
                service.class = class.to_owned();
                service.class_range = php_expression_content_range(option_node("class").as_ref());
            }
            if let Some(target) = option("alias") {
                service.alias_target = php_array_normalize_service(target);
            }
            if let Some(target) = option("decorates") {
                service.decorates = php_array_normalize_service(target);
                service.decorates_range =
                    php_expression_content_range(option_node("decorates").as_ref());
            }
            if let Some(parent) = option("parent") {
                service.parent = php_array_normalize_service(parent);
                service.parent_range = php_expression_content_range(option_node("parent").as_ref());
            }
            if let Some(deprecated) = option("deprecated")
                && !deprecated.is_empty()
                && !deprecated.eq_ignore_ascii_case("false")
            {
                service.deprecated = true;
                if !deprecated.eq_ignore_ascii_case("true") {
                    service.deprecation = deprecated.to_owned();
                }
                service.deprecated_range =
                    php_expression_content_range(option_node("deprecated").as_ref());
            }
            let tags = php_array_node(option_node("tags"));
            service
                .tags
                .extend(php_array_tags(tags.as_ref(), resolver, path));
            if service.class.is_empty()
                && service.alias_target.is_empty()
                && !option("abstract")
                    .unwrap_or("")
                    .eq_ignore_ascii_case("true")
                && id.contains('\\')
            {
                service.class = id.strip_prefix('\\').unwrap_or(&id).to_owned();
                service.class_range = service.id_range;
            }
            result.insert(id, service);
            continue;
        }
        let Some(target) = php_array_static_value_ok(Some(&value), resolver, path) else {
            continue;
        };
        let target = php_array_normalize_service(&target);
        if target.is_empty() {
            continue;
        }
        result.insert(
            id.clone(),
            Service {
                id,
                alias_target: target,
                path: path.to_owned(),
                line: php_array_line(line_index, &item),
                range: item.range_trimmed_trivia(),
                id_range: php_expression_content_range(id_node.as_ref()),
                ..Service::default()
            },
        );
    }
    (result, prototypes)
}

fn php_array_default_tags(array: &SyntaxNode, resolver: &NameResolver, path: &str) -> TagMap {
    let definition = php_array_entry_array(Some(array), "_defaults", resolver, path);
    let tags = php_array_node(php_array_option_value(
        definition.as_ref(),
        "tags",
        resolver,
        path,
    ));
    php_array_tags(tags.as_ref(), resolver, path)
}

fn php_array_instanceof_tags(array: &SyntaxNode, resolver: &NameResolver, path: &str) -> TagMap {
    let definitions = php_array_entry_array(Some(array), "_instanceof", resolver, path);
    let mut result = TagMap::new();
    for item in items(definitions.as_ref()) {
        let class_name =
            php_array_static_value(phpquery::array_item_key(&item).as_ref(), resolver, path);
        let config = php_array_node(phpquery::array_item_value(&item));
        let Some(config) = config.filter(|_| !class_name.is_empty()) else {
            continue;
        };
        let tags = php_array_node(php_array_option_value(
            Some(&config),
            "tags",
            resolver,
            path,
        ));
        for tag in php_array_tags(tags.as_ref(), resolver, path).into_keys() {
            result.insert(
                tag,
                class_name
                    .strip_prefix('\\')
                    .unwrap_or(&class_name)
                    .to_owned(),
            );
        }
    }
    result
}

#[allow(clippy::too_many_arguments)]
fn php_array_prototype(
    path: &str,
    namespace: &str,
    namespace_node: &SyntaxNode,
    resource: &str,
    definition: &SyntaxNode,
    line_index: &LineIndex,
    resolver: &NameResolver,
    default_tags: &TagMap,
    instanceof_tags: &TagMap,
    default_autowire: bool,
    default_autowire_set: bool,
) -> ServicePrototype {
    let option_node = |name: &str| php_array_option_value(Some(definition), name, resolver, path);
    let mut prototype = ServicePrototype {
        namespace: namespace.strip_prefix('\\').unwrap_or(namespace).to_owned(),
        resource: config_path(path, resource),
        autowire: default_autowire,
        autowire_set: default_autowire_set,
        tags: default_tags.clone(),
        instanceof_tags: instanceof_tags.clone(),
        path: path.to_owned(),
        line: php_array_line(line_index, definition),
        range: TextRange::new(
            namespace_node.range_trimmed_trivia().start(),
            definition.range_trimmed_trivia().end(),
        ),
        namespace_range: php_expression_content_range(Some(namespace_node)),
        resource_range: php_expression_content_range(option_node("resource").as_ref()),
        ..ServicePrototype::default()
    };
    if let Some(autowire) = option_node("autowire") {
        prototype.autowire =
            configured_service_bool(&php_array_static_value(Some(&autowire), resolver, path)).0;
        prototype.autowire_set = true;
    }
    let exclude = option_node("exclude");
    if let Some(values) = php_array_node(exclude.clone()) {
        for item in phpquery::array_items(&values) {
            if let Some(value) = php_array_static_value_ok(
                phpquery::array_item_value(&item).as_ref(),
                resolver,
                path,
            ) {
                prototype.excludes.push(config_path(path, &value));
            }
        }
    } else if let Some(value) = php_array_static_value_ok(exclude.as_ref(), resolver, path) {
        prototype.excludes = vec![config_path(path, &value)];
    }
    let tags = php_array_node(option_node("tags"));
    prototype
        .tags
        .extend(php_array_tags(tags.as_ref(), resolver, path));
    prototype
}

fn php_array_autowire(
    definition: Option<&SyntaxNode>,
    resolver: &NameResolver,
    path: &str,
) -> (bool, bool) {
    let Some(value) = php_array_option_value(definition, "autowire", resolver, path) else {
        return (false, false);
    };
    (
        configured_service_bool(&php_array_static_value(Some(&value), resolver, path)).0,
        true,
    )
}

fn php_array_tags(array: Option<&SyntaxNode>, resolver: &NameResolver, path: &str) -> TagMap {
    let mut result = TagMap::new();
    for item in items(array) {
        let value = phpquery::array_item_value(&item);
        if let Some(config) = php_array_node(value.clone()) {
            let name_node = php_array_option_value(Some(&config), "name", resolver, path);
            let name = php_array_static_value(name_node.as_ref(), resolver, path);
            if !name.is_empty() {
                result.insert(name, String::new());
            }
            continue;
        }
        let name = php_array_static_value(value.as_ref(), resolver, path);
        if !name.is_empty() {
            result.insert(name, String::new());
        }
    }
    result
}

fn php_array_options(
    array: &SyntaxNode,
    resolver: &NameResolver,
    path: &str,
) -> HashMap<String, String> {
    let mut result = HashMap::new();
    for item in phpquery::array_items(array) {
        let key = php_array_static_value(phpquery::array_item_key(&item).as_ref(), resolver, path);
        if key.is_empty() {
            continue;
        }
        if let Some(value) =
            php_array_static_value_ok(phpquery::array_item_value(&item).as_ref(), resolver, path)
        {
            result.insert(key, value);
        }
    }
    result
}

/// The value of the first item of `array` whose static key equals `name`.
pub(crate) fn php_array_option_value(
    array: Option<&SyntaxNode>,
    name: &str,
    resolver: &NameResolver,
    path: &str,
) -> Option<SyntaxNode> {
    items(array)
        .into_iter()
        .find(|item| {
            php_array_static_value(phpquery::array_item_key(item).as_ref(), resolver, path) == name
        })
        .and_then(|item| phpquery::array_item_value(&item))
}

pub(crate) fn php_array_entry_array(
    array: Option<&SyntaxNode>,
    name: &str,
    resolver: &NameResolver,
    path: &str,
) -> Option<SyntaxNode> {
    php_array_node(php_array_option_value(array, name, resolver, path))
}

pub(crate) fn php_array_node(node: Option<SyntaxNode>) -> Option<SyntaxNode> {
    node.filter(|node| node.kind() == phpsyntax::PHP_ARRAY)
}

pub(crate) fn php_array_static_value(
    node: Option<&SyntaxNode>,
    resolver: &NameResolver,
    path: &str,
) -> String {
    php_array_static_value_ok(node, resolver, path).unwrap_or_default()
}

pub(crate) fn php_array_static_value_ok(
    node: Option<&SyntaxNode>,
    resolver: &NameResolver,
    path: &str,
) -> Option<String> {
    static_php_value(&node?.text().to_string(), resolver, path)
}

fn php_array_normalize_service(value: &str) -> String {
    match parse_service_reference(value) {
        Some((service, _)) => service,
        None => value.trim().to_owned(),
    }
}

fn php_array_line(line_index: &LineIndex, node: &SyntaxNode) -> u32 {
    line_at(line_index, node.text_range().start())
}

/// Merges fluent configuration (`base`, which wins) with array configuration.
pub(crate) fn merge_php_service_configs(
    mut base: PhpServiceConfig,
    additional: PhpServiceConfig,
) -> PhpServiceConfig {
    let mut services: HashMap<String, Service> = HashMap::new();
    for service in additional
        .services
        .into_iter()
        .chain(std::mem::take(&mut base.services))
    {
        services.insert(service.id.clone(), service);
    }
    let mut parameters: HashMap<String, Parameter> = HashMap::new();
    for parameter in additional
        .parameters
        .into_iter()
        .chain(std::mem::take(&mut base.parameters))
    {
        parameters.insert(parameter.name.clone(), parameter);
    }
    base.services = services.into_values().collect();
    base.parameters = parameters.into_values().collect();
    base.prototypes.extend(additional.prototypes);
    base.method_references.extend(additional.method_references);
    sort_services(&mut base.services);
    sort_parameters(&mut base.parameters);
    base
}
