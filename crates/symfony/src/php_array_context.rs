//! Context of strings inside Symfony's native PHP array service
//! configuration. Only top-level returned arrays and `App::config` roots are
//! accepted, so ordinary application arrays do not gain container semantics.

use swlsp_parser_php::query as phpquery;
use swlsp_parser_php::syntax as phpsyntax;
use swlsp_php::NameResolver;
use swlsp_syntax::{SyntaxNode, TextRange};

use crate::php_array_services::{php_array_node, php_array_option_value};
use crate::php_context::{PhpConfigReferenceKind, short_php_call_name};
use crate::php_services::static_php_value;
use crate::reference::parse_service_reference;

/// A method-name string in configured calls or factory tuples.
#[derive(Clone, Debug, Default, PartialEq, Eq)]
pub struct PhpArrayServiceMethodReference {
    pub service: String,
    pub class: String,
    pub method: String,
    pub range: TextRange,
    pub allow_static: bool,
}

/// Classifies values in Symfony's legacy/native PHP array service
/// configuration.
pub fn php_array_service_reference_at(node: &SyntaxNode) -> PhpConfigReferenceKind {
    use PhpConfigReferenceKind as Kind;
    let Some(literal) = phpquery::string_at(node) else {
        return Kind::None;
    };
    if let Some(call) = phpquery::call_at(&literal)
        && call.kind() == phpsyntax::PHP_FUNCTION_CALL
        && phpquery::string_argument_index(&literal) == Some(0)
        && matches!(
            short_php_call_name(&phpquery::call_method_name(&call)),
            "service" | "service_closure" | "param" | "tagged_iterator" | "tagged_locator"
        )
    {
        return Kind::None;
    }
    let Some(path) = php_array_service_path(&literal) else {
        return Kind::None;
    };
    let value = phpquery::string_value(&literal);
    let value = value.trim();
    let is_service = value.starts_with('@') && !value.starts_with("@@");
    if path.is_empty() {
        return if is_service {
            Kind::Service
        } else {
            Kind::None
        };
    }
    let path: Vec<&str> = path.iter().map(String::as_str).collect();
    match path.as_slice() {
        ["decorates" | "parent"] => return Kind::Service,
        ["class"] => return Kind::Class,
        ["factory", "0"] => return Kind::Service,
        _ => {}
    }
    if php_array_argument_path(&path) {
        if value.is_empty() || value.starts_with('%') {
            return Kind::Parameter;
        }
        if is_service || parse_service_reference(value).is_some() {
            return Kind::Service;
        }
        if value.contains('\\') {
            return Kind::Class;
        }
        return Kind::None;
    }
    if php_array_tag_path(&path) {
        return Kind::Tag;
    }
    Kind::None
}

pub fn php_array_parameter_completion_at(node: &SyntaxNode) -> bool {
    php_array_service_reference_at(node) == PhpConfigReferenceKind::Parameter
}

pub fn php_array_service_context_at(node: &SyntaxNode) -> bool {
    phpquery::string_at(node).is_some_and(|literal| php_array_service_path(&literal).is_some())
}

/// Resolves method-name strings in configured calls and factory tuples.
/// Receiver class constants and import aliases are normalized immediately;
/// service IDs remain available for container-based resolution.
pub fn php_array_service_method_at(
    root: &SyntaxNode,
    node: &SyntaxNode,
) -> Option<PhpArrayServiceMethodReference> {
    let literal = phpquery::string_at(node)?;
    let path = php_array_service_path(&literal)?;
    let mut reference = PhpArrayServiceMethodReference {
        method: phpquery::string_value(&literal),
        range: phpquery::string_content_range(&literal),
        ..PhpArrayServiceMethodReference::default()
    };
    let resolver = NameResolver::new(root);
    let path: Vec<&str> = path.iter().map(String::as_str).collect();
    match path.as_slice() {
        ["calls", _, "0"] => {
            let entry = php_array_service_entry_at(&literal)?;
            let id = phpquery::array_item_key(&entry)
                .and_then(|key| static_php_value(&key.text().to_string(), &resolver, ""))
                .unwrap_or_default();
            let definition = php_array_node(phpquery::array_item_value(&entry));
            if let Some(class_node) =
                php_array_option_value(definition.as_ref(), "class", &resolver, "")
            {
                reference.class = static_php_value(&class_node.text().to_string(), &resolver, "")
                    .unwrap_or_default();
                if !reference.class.is_empty() {
                    reference.class = format!(
                        "\\{}",
                        reference
                            .class
                            .strip_prefix('\\')
                            .unwrap_or(&reference.class)
                    );
                }
            }
            if reference.class.is_empty() && id.contains('\\') {
                reference.class = format!("\\{}", id.strip_prefix('\\').unwrap_or(&id));
            }
            reference.service = id;
        }
        ["factory", "1"] => {
            let entry = php_array_service_entry_at(&literal);
            let definition = php_array_node(entry.as_ref().and_then(phpquery::array_item_value));
            let factory = php_array_node(php_array_option_value(
                definition.as_ref(),
                "factory",
                &resolver,
                "",
            ));
            let items = factory
                .as_ref()
                .map(phpquery::array_items)
                .unwrap_or_default();
            if items.len() < 2 {
                return None;
            }
            let receiver = phpquery::array_item_value(&items[0]);
            let receiver_text = receiver
                .map(|receiver| receiver.text().to_string())
                .unwrap_or_default();
            let value = static_php_value(&receiver_text, &resolver, "")
                .filter(|value| !value.is_empty())?;
            match parse_service_reference(&value) {
                Some((service, _)) => reference.service = service,
                None => {
                    reference.class = format!("\\{}", value.strip_prefix('\\').unwrap_or(&value))
                }
            }
            reference.allow_static = true;
        }
        _ => return None,
    }
    if reference.class.is_empty() && reference.service.is_empty() {
        return None;
    }
    Some(reference)
}

fn php_array_argument_path(path: &[&str]) -> bool {
    (path.len() >= 2 && path[0] == "arguments")
        || (path.len() >= 4 && path[0] == "calls" && path[2] == "1")
}

fn php_array_tag_path(path: &[&str]) -> bool {
    (path.len() == 2 && path[0] == "tags")
        || (path.len() == 3 && path[0] == "tags" && path[2] == "name")
}

/// The key path from a service entry down to `node`, or `None` outside
/// accepted service configuration.
fn php_array_service_path(node: &SyntaxNode) -> Option<Vec<String>> {
    let mut current = node.clone();
    let mut reversed = Vec::new();
    loop {
        let item = php_array_value_item_at(&current)?;
        if php_array_is_service_entry(&item) {
            reversed.reverse();
            return Some(reversed);
        }
        let array = php_array_parent(&item)?;
        let segment = php_array_item_segment(&array, &item)?;
        reversed.push(segment);
        current = array;
    }
}

fn php_array_service_entry_at(node: &SyntaxNode) -> Option<SyntaxNode> {
    let mut current = node.clone();
    loop {
        let item = php_array_value_item_at(&current)?;
        if php_array_is_service_entry(&item) {
            return Some(item);
        }
        current = php_array_parent(&item)?;
    }
}

fn php_array_is_service_entry(item: &SyntaxNode) -> bool {
    if phpquery::array_item_key(item).is_none() {
        return false;
    }
    let Some(services_array) = php_array_parent(item) else {
        return false;
    };
    let Some(services_item) = php_array_value_item_at(&services_array) else {
        return false;
    };
    if php_array_static_string(phpquery::array_item_key(&services_item).as_ref()) != "services"
        || !php_array_same_node(
            phpquery::array_item_value(&services_item).as_ref(),
            Some(&services_array),
        )
    {
        return false;
    }
    php_array_accepted_root(php_array_parent(&services_item).as_ref())
}

/// Whether `array` is a top-level returned array or the first argument of a
/// `::config()` call outside functions and classes.
pub(crate) fn php_array_accepted_root(array: Option<&SyntaxNode>) -> bool {
    let Some(array) = array.filter(|array| array.kind() == phpsyntax::PHP_ARRAY) else {
        return false;
    };
    if let Some(parent) = array.parent()
        && parent.kind() == phpsyntax::PHP_RETURN_STATEMENT
        && phpquery::function_like_at(&parent).is_none()
        && phpquery::class_at(&parent).is_none()
    {
        return true;
    }
    let Some(call) = phpquery::call_at(array) else {
        return false;
    };
    call.kind() == phpsyntax::PHP_SCOPED_CALL
        && phpquery::call_method_name(&call).eq_ignore_ascii_case("config")
        && php_array_same_node(
            phpquery::argument_expression(&call, 0).as_ref(),
            Some(array),
        )
        && phpquery::function_like_at(&call).is_none()
        && phpquery::class_at(&call).is_none()
}

fn php_array_value_item_at(node: &SyntaxNode) -> Option<SyntaxNode> {
    let current = node
        .ancestors()
        .find(|current| current.kind() == phpsyntax::PHP_ARRAY_ITEM)?;
    let value = phpquery::array_item_value(&current)?;
    let outer = value.text_range();
    let inner = node.text_range();
    (inner.start() >= outer.start() && inner.end() <= outer.end()).then_some(current)
}

fn php_array_parent(node: &SyntaxNode) -> Option<SyntaxNode> {
    node.ancestors()
        .skip(1)
        .find(|current| current.kind() == phpsyntax::PHP_ARRAY)
}

fn php_array_item_segment(array: &SyntaxNode, item: &SyntaxNode) -> Option<String> {
    if let Some(key) = phpquery::array_item_key(item) {
        let value = php_array_static_string(Some(&key));
        return (!value.is_empty()).then_some(value);
    }
    phpquery::array_items(array)
        .iter()
        .position(|candidate| php_array_same_node(Some(candidate), Some(item)))
        .map(|index| index.to_string())
}

fn php_array_static_string(node: Option<&SyntaxNode>) -> String {
    let Some(node) = node else {
        return String::new();
    };
    if let Some(literal) = phpquery::string_at(node)
        && php_array_same_node(Some(&literal), Some(node))
    {
        return phpquery::string_value(&literal);
    }
    node.text().to_string().trim().to_owned()
}

fn php_array_same_node(left: Option<&SyntaxNode>, right: Option<&SyntaxNode>) -> bool {
    match (left, right) {
        (Some(left), Some(right)) => left == right || left.text_range() == right.text_range(),
        _ => false,
    }
}

#[cfg(test)]
#[path = "tests_phpsvc/php_array_context.rs"]
mod tests;
