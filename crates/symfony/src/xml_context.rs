//! Cursor contexts in Symfony XML service files.

use swlsp_parser_xml::query as xmlquery;
use swlsp_syntax::SyntaxNode;

use crate::reference::normalize_service_reference;
use crate::util::{GoLower, go_trim_space, xml_attr};

const XML_TAGGED_ARGUMENT_TYPES: [&str; 3] = ["tagged_iterator", "tagged_locator", "tagged"];

fn attribute_name(attribute: &SyntaxNode) -> String {
    xmlquery::attribute_name(attribute).unwrap_or_default()
}

fn element_name(element: &SyntaxNode) -> String {
    xmlquery::element_name(element).unwrap_or_default()
}

/// Whether `node` is the `id` or `class` attribute of a `<service>`.
pub fn xml_service_is_service_id(node: &SyntaxNode) -> bool {
    let Some(attribute) = xmlquery::attribute_at(node) else {
        return false;
    };
    let name = attribute_name(&attribute);
    xmlquery::element_at(&attribute).is_some_and(|element| {
        element_name(&element) == "service" && (name == "id" || name == "class")
    })
}

pub fn xml_service_is_service_reference(node: &SyntaxNode) -> bool {
    xml_service_reference_name(node).is_some()
}

/// The referenced service at `node`. `Some("")` marks a reference position
/// whose value is still empty.
pub fn xml_service_reference_name(node: &SyntaxNode) -> Option<String> {
    let attribute = xmlquery::attribute_at(node)?;
    let element = xmlquery::element_at(&attribute)?;
    let name = attribute_name(&attribute);
    let element_name = element_name(&element);
    let accepted =
        (element_name == "argument" && name == "id" && xml_attr(&element, "type") == "service")
            || (element_name == "alias" && name == "service")
            || (element_name == "service" && name == "decorates")
            || (element_name == "factory" && name == "service");
    if !accepted {
        return None;
    }
    let raw = xmlquery::attribute_value(&attribute).unwrap_or_default();
    let value = go_trim_space(&raw);
    if value.is_empty() || value == "@" || value == "@?" || value == "@!" {
        return Some(String::new());
    }
    if let Some(normalized) = normalize_service_reference(value) {
        return Some(normalized);
    }
    let value = go_trim_space(value.strip_prefix('@').unwrap_or(value));
    (!value.is_empty() && !value.contains(['%', '$', '{', '}'])).then(|| value.to_owned())
}

/// Whether the element at `node` tolerates a missing service.
pub fn xml_service_reference_optional(node: &SyntaxNode) -> bool {
    let Some(element) = xmlquery::element_at(node) else {
        return false;
    };
    matches!(
        xml_attr(&element, "on-invalid").to_go_lower().as_str(),
        "ignore" | "null" | "ignore_uninitialized"
    )
}

pub fn xml_service_is_argument_tag(node: &SyntaxNode) -> bool {
    let Some(attribute) = xmlquery::attribute_at(node) else {
        return false;
    };
    if attribute_name(&attribute) != "tag" {
        return false;
    }
    let Some(element) = xmlquery::element_at(&attribute) else {
        return false;
    };
    element_name(&element) == "argument"
        && XML_TAGGED_ARGUMENT_TYPES.contains(&xml_attr(&element, "type").as_str())
}

pub fn xml_service_is_tag_element(node: &SyntaxNode) -> bool {
    let Some(attribute) = xmlquery::attribute_at(node) else {
        return false;
    };
    attribute_name(&attribute) == "name"
        && xmlquery::element_at(&attribute).is_some_and(|element| element_name(&element) == "tag")
}

pub fn xml_service_is_parameter_reference(node: &SyntaxNode) -> bool {
    xmlquery::element_at(node).is_some_and(|element| {
        element_name(&element) == "argument" && xmlquery::text_content(&element).contains('%')
    })
}

/// The `id` of the nearest enclosing `<service>`.
pub fn xml_current_service_id(node: &SyntaxNode) -> String {
    let mut current = xmlquery::element_at(node);
    while let Some(element) = current {
        if element_name(&element) == "service" {
            return xml_attr(&element, "id");
        }
        current = xmlquery::parent_element(&element);
    }
    String::new()
}

pub fn xml_context_value(node: &SyntaxNode) -> String {
    xmlquery::node_value(node)
}

pub fn xml_parameter_reference_name(node: &SyntaxNode) -> String {
    let Some(element) = xmlquery::element_at(node) else {
        return String::new();
    };
    let value = xmlquery::text_content(&element);
    let Some(start) = value.find('%') else {
        return String::new();
    };
    let Some(end) = value[start + 1..].find('%') else {
        return String::new();
    };
    value[start + 1..start + 1 + end].to_owned()
}

/// The static class name of a `<service class="...">` attribute.
pub fn xml_class_reference_name(node: &SyntaxNode) -> Option<String> {
    let attribute = xmlquery::attribute_at(node)?;
    if attribute_name(&attribute) != "class" {
        return None;
    }
    let element = xmlquery::element_at(&attribute)?;
    if element_name(&element) != "service" {
        return None;
    }
    let raw = xmlquery::attribute_value(&attribute).unwrap_or_default();
    let value = go_trim_space(&raw);
    (!value.is_empty() && value.contains('\\') && !value.contains(['%', '$', '{', '}']))
        .then(|| value.to_owned())
}

#[cfg(test)]
#[path = "tests_refs/xml_context.rs"]
mod tests;
