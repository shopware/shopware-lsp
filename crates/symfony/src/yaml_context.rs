//! Cursor contexts in Symfony YAML service files.

use swlsp_parser_yaml::query as yamlquery;
use swlsp_parser_yaml::syntax as yamlsyntax;
use swlsp_syntax::SyntaxNode;

use crate::reference::normalize_service_reference;
use crate::util::go_trim_space;
use crate::yaml_services::{yaml_is_mapping, yaml_property, yaml_scalar};

pub fn is_yaml_service_id(node: &SyntaxNode) -> bool {
    let path = yamlquery::pair_path(node);
    path.len() == 2 && path[0] == "services" && !path[1].starts_with('_')
}

pub fn is_yaml_class_property_in_service(node: &SyntaxNode) -> bool {
    let path = yamlquery::pair_path(node);
    path.len() == 3 && path[0] == "services" && path[2] == "class"
}

pub fn is_yaml_argument_service_id(node: &SyntaxNode) -> bool {
    yaml_service_reference_name(node).is_some()
}

pub fn yaml_context_value(node: &SyntaxNode) -> String {
    if is_yaml_service_id(node) {
        let key = yamlquery::ancestor_pair(node).and_then(|pair| yamlquery::pair_key(&pair));
        return yaml_scalar(key.as_ref());
    }
    if node.kind() == yamlsyntax::YAML_SCALAR {
        return yamlquery::scalar_value(node);
    }
    match yamlquery::ancestor_pair(node).and_then(|pair| yamlquery::pair_value(&pair)) {
        Some(value) if value.kind() == yamlsyntax::YAML_SCALAR => yamlquery::scalar_value(&value),
        _ => String::new(),
    }
}

/// The referenced service at a YAML scalar. `Some("")` marks a reference
/// position whose value is still empty.
pub fn yaml_service_reference_name(node: &SyntaxNode) -> Option<String> {
    if node.kind() != yamlsyntax::YAML_SCALAR {
        return None;
    }
    let pair = yamlquery::ancestor_pair(node)?;
    if yamlquery::pair_key(&pair).as_ref() == Some(node) {
        return None;
    }
    let path = yamlquery::pair_path(node);
    if path.len() < 2 || path[0] != "services" {
        return None;
    }
    let scalar = yamlquery::scalar_value(node);
    let value = go_trim_space(&scalar);
    if value == "@" || value == "@?" || value == "@!" {
        return Some(String::new());
    }
    if let Some(service) = normalize_service_reference(value) {
        return Some(service);
    }
    if path.len() >= 3 && matches!(path[2].as_str(), "alias" | "decorates" | "parent") {
        let value = go_trim_space(value.strip_prefix('@').unwrap_or(value));
        return (!value.is_empty() && !value.contains(['%', '$', '{', '}']))
            .then(|| value.to_owned());
    }
    None
}

/// The static class named at a YAML scalar (an explicit `class:` or a
/// class-named service ID).
pub fn yaml_class_reference_name(node: &SyntaxNode) -> Option<String> {
    if node.kind() != yamlsyntax::YAML_SCALAR {
        return None;
    }
    let path = yamlquery::pair_path(node);
    if path.len() == 3 && path[0] == "services" && path[2] == "class" {
        let value = yamlquery::scalar_value(node);
        return static_php_class_name(&value).then_some(value);
    }
    if path.len() != 2 || path[0] != "services" {
        return None;
    }
    let pair = yamlquery::ancestor_pair(node)?;
    if yamlquery::pair_key(&pair).as_ref() != Some(node) {
        return None;
    }
    let service_id = yamlquery::scalar_value(node);
    let value = yamlquery::pair_value(&pair);
    if yaml_is_mapping(value.as_ref())
        && (yaml_property(value.as_ref(), "class").is_some()
            || yaml_property(value.as_ref(), "resource").is_some()
            || yaml_property(value.as_ref(), "exclude").is_some())
    {
        return None;
    }
    static_php_class_name(&service_id).then_some(service_id)
}

pub(crate) fn static_php_class_name(value: &str) -> bool {
    !value.is_empty() && value.contains('\\') && !value.contains(['%', '$', '{', '}', '@'])
}

#[cfg(test)]
#[path = "tests_refs/yaml_context.rs"]
mod tests;
