//! `twig_component` bundle configuration in YAML and PHP (port of
//! `parser_config.go` and `parser_config_php.go`).

use std::collections::HashSet;

use swlsp_parser_php::query as phpquery;
use swlsp_parser_php::syntax::*;
use swlsp_parser_yaml::query as yamlquery;
use swlsp_parser_yaml::syntax::YAML_PAIR;
use swlsp_php_types::go_to_lower;
use swlsp_syntax::{Kind, SyntaxNode, SyntaxNodeExt, TextRange, TextSize};

use crate::model::{Namespace, normalize_class, normalize_component_name, normalize_directory};

const TWIG_COMPONENT_EXTENSION_NAME: &str = "twig_component";

pub(crate) fn configuration_in_yaml(
    path: &str,
    root: &SyntaxNode,
) -> (Vec<Namespace>, Vec<String>) {
    let mut namespaces = Vec::new();
    let mut anonymous = Vec::new();
    for pair in yamlquery::nodes(root, &[YAML_PAIR]) {
        let key = yamlquery::pair_key(&pair);
        if key.as_ref().map(yamlquery::scalar_value).as_deref() != Some("twig_component") {
            continue;
        }
        let Some(config) = yamlquery::pair_value(&pair).filter(yamlquery::is_mapping) else {
            continue;
        };
        if let Some(value) = yamlquery::property(&config, "anonymous_template_directory") {
            let directory = normalize_directory(&yamlquery::scalar_value(&value));
            if !directory.is_empty() {
                anonymous.push(directory);
            }
        }
        let Some(defaults) = yamlquery::property(&config, "defaults").filter(yamlquery::is_mapping)
        else {
            continue;
        };
        for entry in yamlquery::pairs(&defaults) {
            let entry_key = yamlquery::pair_key(&entry);
            let class_prefix = normalize_class(
                &entry_key
                    .as_ref()
                    .map(yamlquery::scalar_value)
                    .unwrap_or_default(),
            );
            let value = yamlquery::pair_value(&entry);
            let mut directory = "components".to_owned();
            let mut name_prefix = String::new();
            match &value {
                Some(value) if yamlquery::is_mapping(value) => {
                    if let Some(configured) = yamlquery::property(value, "template_directory") {
                        directory = yamlquery::scalar_value(&configured);
                    }
                    name_prefix = yamlquery::property(value, "name_prefix")
                        .map(|node| yamlquery::scalar_value(&node))
                        .unwrap_or_default();
                }
                Some(value) => directory = yamlquery::scalar_value(value),
                None => {}
            }
            let directory = normalize_directory(&directory);
            if class_prefix.is_empty() || directory.is_empty() {
                continue;
            }
            namespaces.push(Namespace {
                class_prefix,
                template_directory: directory,
                name_prefix: normalize_component_name(&name_prefix),
                file: path.to_owned(),
                range: entry_key
                    .as_ref()
                    .map(quoted_value_range)
                    .unwrap_or_default(),
            });
        }
    }
    (unique_namespaces(namespaces), unique_strings(anonymous))
}

/// The trimmed node range without one pair of matching single or double
/// quotes (Go `scalarValueRange`/`stringValueRange`).
pub(crate) fn quoted_value_range(node: &SyntaxNode) -> TextRange {
    let range = node.range_trimmed_trivia();
    let text = node.text().to_string();
    let text = text.trim().as_bytes();
    if text.len() >= 2
        && (text[0] == b'\'' || text[0] == b'"')
        && text[text.len() - 1] == text[0]
        && range.end() > range.start() + TextSize::from(1)
    {
        return TextRange::new(
            range.start() + TextSize::from(1),
            range.end() - TextSize::from(1),
        );
    }
    range
}

pub(crate) fn unique_namespaces(values: Vec<Namespace>) -> Vec<Namespace> {
    let mut seen = HashSet::with_capacity(values.len());
    values
        .into_iter()
        .filter(|value| {
            seen.insert(format!(
                "{}\0{}\0{}",
                go_to_lower(&value.class_prefix),
                value.template_directory,
                value.name_prefix
            ))
        })
        .collect()
}

pub(crate) fn unique_strings(values: Vec<String>) -> Vec<String> {
    let mut seen = HashSet::with_capacity(values.len());
    values
        .into_iter()
        .filter(|value| !value.is_empty() && seen.insert(value.clone()))
        .collect()
}

// --- PHP ---------------------------------------------------------------------

/// Extracts bundle configuration from the portable PHP forms accepted by the
/// Config component:
///
///   - `return ['twig_component' => [...]];`
///   - `return App::config(['twig_component' => [...]]);`
///   - `App::config(['twig_component' => [...]]);`
///   - a returned `ContainerConfigurator` closure calling `extension()`.
pub(crate) fn configuration_in_php(path: &str, root: &SyntaxNode) -> (Vec<Namespace>, Vec<String>) {
    let mut out = PhpConfig {
        path,
        namespaces: Vec::new(),
        anonymous: Vec::new(),
    };
    for statement in phpquery::nodes(root, &[PHP_RETURN_STATEMENT]) {
        if !is_top_level_php_config_node(&statement) {
            continue;
        }
        if let Some(array) = direct_returned_php_config_array(&statement) {
            out.visit_config_array(Some(&array));
        }
        if let Some(closure) = direct_returned_php_config_closure(&statement) {
            out.visit_extension_config_calls(&closure);
        }
    }
    for call in phpquery::calls(root, &[]) {
        if !is_php_app_config_call(&call) || !is_top_level_php_config_node(&call) {
            continue;
        }
        if let Some(array) =
            phpquery::argument_expression(&call, 0).and_then(|value| phpquery::array_at(&value))
        {
            out.visit_config_array(Some(&array));
        }
    }
    (
        unique_namespaces(out.namespaces),
        unique_strings(out.anonymous),
    )
}

fn direct_returned_php_config_array(statement: &SyntaxNode) -> Option<SyntaxNode> {
    phpquery::nodes(statement, &[PHP_ARRAY])
        .into_iter()
        .find(|array| closest_ancestor_of_kind(array.parent(), statement, PHP_ARRAY).is_none())
}

fn direct_returned_php_config_closure(statement: &SyntaxNode) -> Option<SyntaxNode> {
    phpquery::nodes(statement, &[PHP_CLOSURE])
        .into_iter()
        .find(|closure| {
            let mut current = closure.parent();
            while let Some(node) = current {
                if node == *statement {
                    return true;
                }
                if matches!(
                    node.kind(),
                    PHP_ARRAY
                        | PHP_ARRAY_ITEM
                        | PHP_ARGUMENT
                        | PHP_NAMED_ARGUMENT
                        | PHP_FUNCTION_CALL
                        | PHP_MEMBER_CALL
                        | PHP_SCOPED_CALL
                ) {
                    return false;
                }
                current = node.parent();
            }
            false
        })
}

struct PhpConfig<'a> {
    path: &'a str,
    namespaces: Vec<Namespace>,
    anonymous: Vec<String>,
}

fn array_value(item: &SyntaxNode) -> Option<SyntaxNode> {
    phpquery::array_item_value(item).and_then(|value| phpquery::array_at(&value))
}

impl PhpConfig<'_> {
    fn visit_config_array(&mut self, array: Option<&SyntaxNode>) {
        let Some(array) = array else {
            return;
        };
        for item in phpquery::array_items(array) {
            let Some(key) = php_config_string(phpquery::array_item_key(&item).as_ref()) else {
                continue;
            };
            if key == TWIG_COMPONENT_EXTENSION_NAME {
                self.visit_twig_component_config(array_value(&item).as_ref());
            } else if key.starts_with("when@") {
                self.visit_config_array(array_value(&item).as_ref());
            }
        }
    }

    fn visit_twig_component_config(&mut self, array: Option<&SyntaxNode>) {
        let Some(array) = array else {
            return;
        };
        for item in phpquery::array_items(array) {
            let Some(key) = php_config_string(phpquery::array_item_key(&item).as_ref()) else {
                continue;
            };
            match key.as_str() {
                "defaults" => self.visit_defaults(array_value(&item).as_ref()),
                "anonymous_template_directory" => {
                    if let Some(directory) =
                        php_config_string(phpquery::array_item_value(&item).as_ref())
                    {
                        let directory = normalize_directory(&directory);
                        if !directory.is_empty() {
                            self.anonymous.push(directory);
                        }
                    }
                }
                _ => {}
            }
        }
    }

    fn visit_defaults(&mut self, array: Option<&SyntaxNode>) {
        let Some(array) = array else {
            return;
        };
        for item in phpquery::array_items(array) {
            let key_node = phpquery::array_item_key(&item);
            let Some(class_prefix) = php_config_string(key_node.as_ref()) else {
                continue;
            };
            let value = phpquery::array_item_value(&item);
            let mut directory = String::new();
            let mut name_prefix = String::new();
            if let Some(configured) = php_config_string(value.as_ref()) {
                directory = configured;
            } else if let Some(options) = value.as_ref().and_then(phpquery::array_at) {
                directory = "components".to_owned();
                for option in phpquery::array_items(&options) {
                    let Some(name) = php_config_string(phpquery::array_item_key(&option).as_ref())
                    else {
                        continue;
                    };
                    let Some(configured) =
                        php_config_string(phpquery::array_item_value(&option).as_ref())
                    else {
                        continue;
                    };
                    match name.as_str() {
                        "template_directory" => directory = configured,
                        "name_prefix" => name_prefix = configured,
                        _ => {}
                    }
                }
            }
            let class_prefix = normalize_class(&class_prefix);
            let directory = normalize_directory(&directory);
            if class_prefix.is_empty() || directory.is_empty() {
                continue;
            }
            self.namespaces.push(Namespace {
                class_prefix,
                template_directory: directory,
                name_prefix: normalize_component_name(&name_prefix),
                file: self.path.to_owned(),
                range: key_node
                    .as_ref()
                    .map(phpquery::string_content_range)
                    .unwrap_or_default(),
            });
        }
    }

    fn visit_extension_config_calls(&mut self, closure: &SyntaxNode) {
        let parameters: HashSet<String> = phpquery::parameters(closure)
            .map(|parameter| {
                phpquery::parameter_name(&parameter)
                    .strip_prefix('$')
                    .map(str::to_owned)
                    .unwrap_or_else(|| phpquery::parameter_name(&parameter))
            })
            .filter(|name| !name.is_empty())
            .collect();
        if parameters.is_empty() {
            return;
        }
        for call in phpquery::calls(closure, &[]) {
            if phpquery::call_method_name(&call) != "extension"
                || closest_function_like(&call).as_ref() != Some(closure)
            {
                continue;
            }
            let Some(receiver) = phpquery::call_receiver(&call) else {
                continue;
            };
            if !parameters.contains(&phpquery::variable_name(&receiver)) {
                continue;
            }
            let extension = php_config_string(phpquery::argument_expression(&call, 0).as_ref());
            if extension.as_deref() != Some(TWIG_COMPONENT_EXTENSION_NAME) {
                continue;
            }
            let options = phpquery::argument_expression(&call, 1)
                .and_then(|value| phpquery::array_at(&value));
            self.visit_twig_component_config(options.as_ref());
        }
    }
}

/// A plain quoted PHP string literal without interpolation, with `\\` and
/// the quote escape unescaped.
pub(crate) fn php_config_string(node: Option<&SyntaxNode>) -> Option<String> {
    let node = node?;
    let literal = phpquery::string_at(node)?;
    if literal.text_range() != node.text_range() {
        return None;
    }
    let text = literal.text().to_string();
    let text = text.trim().as_bytes();
    if text.len() < 2 {
        return None;
    }
    let quote = text[0];
    if (quote != b'\'' && quote != b'"') || text[text.len() - 1] != quote {
        return None;
    }
    let value = phpquery::string_value(&literal);
    if quote == b'"' && value.contains('$') {
        return None;
    }
    let value = value.replace("\\\\", "\\");
    Some(if quote == b'\'' {
        value.replace("\\'", "'")
    } else {
        value.replace("\\\"", "\"")
    })
}

fn is_php_app_config_call(call: &SyntaxNode) -> bool {
    let name = go_to_lower(phpquery::call_name(call).trim());
    let name = name.strip_prefix('\\').unwrap_or(&name);
    name == "app::config" || name.ends_with("\\app::config")
}

fn is_top_level_php_config_node(node: &SyntaxNode) -> bool {
    !node.ancestors().skip(1).any(|current| {
        matches!(
            current.kind(),
            PHP_METHOD_DECLARATION
                | PHP_FUNCTION_DECLARATION
                | PHP_CLOSURE
                | PHP_ARROW_FUNCTION
                | PHP_CLASS_DECLARATION
                | PHP_ANONYMOUS_CLASS
        )
    })
}

fn closest_function_like(node: &SyntaxNode) -> Option<SyntaxNode> {
    node.ancestors().skip(1).find(|current| {
        matches!(
            current.kind(),
            PHP_METHOD_DECLARATION | PHP_FUNCTION_DECLARATION | PHP_CLOSURE | PHP_ARROW_FUNCTION
        )
    })
}

fn closest_ancestor_of_kind(
    node: Option<SyntaxNode>,
    stop: &SyntaxNode,
    kind: Kind,
) -> Option<SyntaxNode> {
    let mut current = node;
    while let Some(node) = current {
        if node == *stop {
            return None;
        }
        if node.kind() == kind {
            return Some(node);
        }
        current = node.parent();
    }
    None
}
