//! Messenger references at a cursor (port of Go `messenger/reference.go`).

use swlsp_parser_php::query as phpquery;
use swlsp_parser_php::syntax::{PHP_ARRAY, PHP_RETURN_STATEMENT, PHP_YIELD_EXPRESSION};
use swlsp_parser_xml::query as xmlquery;
use swlsp_parser_yaml::query as yamlquery;
use swlsp_parser_yaml::syntax::YAML_SCALAR;
use swlsp_php::{NameResolver, PhpContext, PhpIndex, name_resolver_for};
use swlsp_php_resolver::MemberResolver;
use swlsp_php_semantic::{Symbol, SymbolKind, Visibility};
use swlsp_php_types::{Type, equal_fold, go_to_lower};
use swlsp_syntax::{Kind, SyntaxNode};

use crate::index::normalize_name;
use crate::parser::{
    is_as_message_handler, node_contains, resolved_class_name, service_tag_class, string_argument,
};

const MESSAGE_SUBSCRIBER_INTERFACE: &str =
    "Symfony\\Component\\Messenger\\Handler\\MessageSubscriberInterface";

#[derive(Clone, Debug, PartialEq, Eq)]
pub struct HandlerMethodReference {
    pub name: String,
    pub class: String,
    pub node: SyntaxNode,
}

#[derive(Clone, Copy, Debug, Default, PartialEq, Eq)]
pub enum ReferenceRole {
    #[default]
    None,
    Message,
    HandlerMethod,
}

#[derive(Clone, Debug, PartialEq, Eq)]
pub struct Reference {
    pub role: ReferenceRole,
    pub name: String,
    pub node: SyntaxNode,
    pub class: String,
    pub method: String,
}

/// The Messenger reference at `node` in the document at `path`.
pub fn reference_at(
    context: Option<&PhpContext>,
    path: &str,
    root: &SyntaxNode,
    node: &SyntaxNode,
) -> Option<Reference> {
    match go_to_lower(go_extension(path)).as_str() {
        ".php" => php_reference_at(context, root, node),
        ".yaml" | ".yml" => yaml_reference_at(root, node),
        ".xml" => xml_reference_at(node),
        _ => None,
    }
}

pub fn php_reference_at(
    context: Option<&PhpContext>,
    root: &SyntaxNode,
    node: &SyntaxNode,
) -> Option<Reference> {
    if let Some(legacy) = handler_method_reference_at(context, root, node) {
        return Some(Reference {
            role: ReferenceRole::HandlerMethod,
            name: legacy.name.clone(),
            node: legacy.node,
            class: legacy.class,
            method: legacy.name,
        });
    }
    let resolver = name_resolver_for(context, root);
    if let Some(attribute) = phpquery::attribute_at(node)
        && is_as_message_handler(&attribute, &resolver)
    {
        let class_name = resolved_class_name(phpquery::class_at(&attribute).as_ref(), &resolver);
        for (index, argument) in phpquery::arguments(&attribute).enumerate() {
            let Some(expression) = phpquery::argument_expression(&attribute, index) else {
                continue;
            };
            if !node_contains(&expression, node) {
                continue;
            }
            let mut name = go_to_lower(&phpquery::argument_name(&argument));
            if name.is_empty() {
                match index {
                    2 => name = "handles".to_owned(),
                    3 => name = "method".to_owned(),
                    _ => {}
                }
            }
            match name.as_str() {
                "handles" => {
                    return Some(Reference {
                        role: ReferenceRole::Message,
                        name: message_name(&expression, &resolver),
                        node: expression,
                        class: class_name,
                        method: String::new(),
                    });
                }
                "method" => {
                    let value = string_node_value(&expression);
                    return Some(Reference {
                        role: ReferenceRole::HandlerMethod,
                        name: value.clone(),
                        node: expression,
                        class: class_name,
                        method: value,
                    });
                }
                _ => {}
            }
        }
    }

    let call = phpquery::call_at(node)?;
    if !equal_fold(&phpquery::call_method_name(&call), "tag")
        || string_argument(phpquery::argument_expression(&call, 0).as_ref())
            != "messenger.message_handler"
    {
        return None;
    }
    let options = phpquery::argument_expression(&call, 1)
        .and_then(|argument| phpquery::array_at(&argument))?;
    let item = phpquery::array_item_at(node)?;
    if !node_contains(&options, &item) {
        return None;
    }
    let key = go_to_lower(
        &phpquery::array_item_key(&item)
            .map(|key| phpquery::string_value(&key))
            .unwrap_or_default(),
    );
    let value = phpquery::array_item_value(&item)?;
    if !node_contains(&value, node) {
        return None;
    }
    let class_name = service_tag_class(&call, &resolver);
    match key.as_str() {
        "handles" => Some(Reference {
            role: ReferenceRole::Message,
            name: message_name(&value, &resolver),
            node: value,
            class: class_name,
            method: String::new(),
        }),
        "method" => {
            let method = string_node_value(&value);
            Some(Reference {
                role: ReferenceRole::HandlerMethod,
                name: method.clone(),
                node: value,
                class: class_name,
                method,
            })
        }
        _ => None,
    }
}

pub fn yaml_reference_at(root: &SyntaxNode, node: &SyntaxNode) -> Option<Reference> {
    let scalar = ancestor(node, YAML_SCALAR)?;
    let pair = yamlquery::ancestor_pair(&scalar)?;
    if yamlquery::pair_value(&pair).as_ref() != Some(&scalar) {
        return None;
    }
    let property = go_to_lower(
        &yamlquery::pair_key(&pair)
            .map(|key| yamlquery::scalar_value(&key))
            .unwrap_or_default(),
    );
    if property != "handles" && property != "method" {
        return None;
    }
    let tag = yaml_tag_mapping_at(&pair)?;
    if yamlquery::property(&tag, "name").map(|name| yamlquery::scalar_value(&name))
        != Some("messenger.message_handler".to_owned())
    {
        return None;
    }
    let (class_name, service) = yaml_service_at(root, &scalar);
    let value = yamlquery::scalar_value(&scalar);
    if property == "handles" {
        return Some(Reference {
            role: ReferenceRole::Message,
            name: normalize_name(&value).to_owned(),
            node: scalar,
            class: class_name,
            method: String::new(),
        });
    }
    if service.is_empty() && class_name.is_empty() {
        return None;
    }
    Some(Reference {
        role: ReferenceRole::HandlerMethod,
        name: value.clone(),
        node: scalar,
        class: class_name,
        method: value,
    })
}

pub fn xml_reference_at(node: &SyntaxNode) -> Option<Reference> {
    let attribute = xmlquery::attribute_at(node)?;
    let property = go_to_lower(&xmlquery::attribute_name(&attribute).unwrap_or_default());
    if property != "handles" && property != "method" {
        return None;
    }
    let tag = xmlquery::element_at(&attribute)?;
    if xmlquery::element_name(&tag).as_deref() != Some("tag")
        || xml_attribute_value(&tag, "name") != "messenger.message_handler"
    {
        return None;
    }
    let mut service = xmlquery::parent_element(&tag);
    while let Some(current) = &service {
        if xmlquery::element_name(current).as_deref() == Some("service") {
            break;
        }
        service = xmlquery::parent_element(current);
    }
    let mut class_name = String::new();
    if let Some(service) = &service {
        class_name = normalize_name(&xml_attribute_value(service, "class")).to_owned();
        if class_name.is_empty() {
            let id = xml_attribute_value(service, "id");
            if id.contains('\\') {
                class_name = normalize_name(&id).to_owned();
            }
        }
    }
    let value = xmlquery::attribute_value(&attribute).unwrap_or_default();
    if property == "handles" {
        return Some(Reference {
            role: ReferenceRole::Message,
            name: normalize_name(&value).to_owned(),
            node: attribute,
            class: class_name,
            method: String::new(),
        });
    }
    Some(Reference {
        role: ReferenceRole::HandlerMethod,
        name: value.clone(),
        node: attribute,
        class: class_name,
        method: value,
    })
}

fn xml_attribute_value(element: &SyntaxNode, name: &str) -> String {
    xmlquery::attribute(element, name)
        .and_then(|attribute| xmlquery::attribute_value(&attribute))
        .unwrap_or_default()
}

/// Recognizes legacy Messenger subscriber method declarations in
/// `MessageSubscriberInterface::getHandledMessages()`:
///
/// ```php
/// return [Message::class => 'handle'];
/// yield Message::class => ['method' => 'handle'];
/// ```
pub fn handler_method_reference_at(
    context: Option<&PhpContext>,
    root: &SyntaxNode,
    node: &SyntaxNode,
) -> Option<HandlerMethodReference> {
    let literal = phpquery::string_at(node)?;
    let method = phpquery::method_at(&literal)?;
    if !equal_fold(&phpquery::method_name(&method), "getHandledMessages")
        || phpquery::function_like_at(&literal).as_ref() != Some(&method)
    {
        return None;
    }
    let class = phpquery::class_at(&method);
    let resolver = NameResolver::new(root);
    let class_name = resolved_class_name(class.as_ref(), &resolver);
    if !is_message_subscriber(context, class.as_ref(), &class_name, &resolver)
        || !is_handler_method_value(&literal, &method)
    {
        return None;
    }
    Some(HandlerMethodReference {
        name: phpquery::string_value(&literal),
        class: class_name,
        node: literal,
    })
}

/// Public non-magic methods of `class_name` (including inherited ones),
/// sorted by name case-insensitively.
pub fn public_handler_methods(index: Option<&PhpIndex>, class_name: &str) -> Vec<Symbol> {
    let Some(index) = index else {
        return Vec::new();
    };
    if class_name.is_empty() {
        return Vec::new();
    }
    let snapshot = index.semantic_snapshot();
    let members = MemberResolver {
        snapshot: Some(&snapshot),
    }
    .all(&Type::named(
        class_name.strip_prefix('\\').unwrap_or(class_name),
        Vec::new(),
    ));
    let mut seen = std::collections::HashSet::with_capacity(members.len());
    let mut result: Vec<Symbol> = Vec::new();
    for member in members {
        let symbol = member.symbol;
        if symbol.kind != SymbolKind::Method
            || symbol.visibility != Visibility::Public
            || equal_fold(&symbol.name, "getHandledMessages")
            || symbol.name.starts_with("__")
        {
            continue;
        }
        if !seen.insert(go_to_lower(&symbol.name)) {
            continue;
        }
        result.push(symbol);
    }
    result.sort_by_cached_key(|symbol| go_to_lower(&symbol.name));
    result
}

fn is_handler_method_value(literal: &SyntaxNode, method: &SyntaxNode) -> bool {
    if let Some(yield_node) = ancestor(literal, PHP_YIELD_EXPRESSION)
        && phpquery::function_like_at(&yield_node).as_ref() == Some(method)
    {
        let children: Vec<SyntaxNode> = yield_node.children().collect();
        if children.len() < 2 || !node_contains(&children[1], literal) {
            return false;
        }
        if children[1] == *literal {
            return true;
        }
        return is_named_method_option(literal, &children[1]);
    }

    let Some(statement) = ancestor(literal, PHP_RETURN_STATEMENT) else {
        return false;
    };
    if phpquery::function_like_at(&statement).as_ref() != Some(method) {
        return false;
    }
    let Some(array) = phpquery::direct_child(&statement, PHP_ARRAY) else {
        return false;
    };
    if !node_contains(&array, literal) {
        return false;
    }
    for item in phpquery::array_items(&array) {
        if !node_contains(&item, literal) {
            continue;
        }
        let (Some(_), Some(value)) = (
            phpquery::array_item_key(&item),
            phpquery::array_item_value(&item),
        ) else {
            return false;
        };
        if !node_contains(&value, literal) {
            return false;
        }
        if value == *literal {
            return true;
        }
        return is_named_method_option(literal, &value);
    }
    false
}

fn is_named_method_option(literal: &SyntaxNode, value: &SyntaxNode) -> bool {
    let Some(item) = phpquery::array_item_at(literal) else {
        return false;
    };
    if !node_contains(value, &item) {
        return false;
    }
    match (
        phpquery::array_item_key(&item),
        phpquery::array_item_value(&item),
    ) {
        (Some(key), Some(item_value)) => {
            equal_fold(&phpquery::string_value(&key), "method")
                && node_contains(&item_value, literal)
        }
        _ => false,
    }
}

fn is_message_subscriber(
    context: Option<&PhpContext>,
    class: Option<&SyntaxNode>,
    class_name: &str,
    resolver: &NameResolver,
) -> bool {
    let Some(class) = class else {
        return false;
    };
    if let Some(context) = context
        && !class_name.is_empty()
        && context.snapshot.relations().is_subtype(
            &Type::named(class_name, Vec::new()),
            &Type::named(MESSAGE_SUBSCRIBER_INTERFACE, Vec::new()),
        )
    {
        return true;
    }
    phpquery::class_implements(class).iter().any(|implemented| {
        let resolved = resolver.resolve(implemented);
        equal_fold(
            resolved.strip_prefix('\\').unwrap_or(&resolved),
            MESSAGE_SUBSCRIBER_INTERFACE,
        )
    })
}

fn ancestor(node: &SyntaxNode, kind: Kind) -> Option<SyntaxNode> {
    node.ancestors().find(|current| current.kind() == kind)
}

fn message_name(node: &SyntaxNode, resolver: &NameResolver) -> String {
    let class_name = phpquery::class_constant_name(node);
    if !class_name.is_empty() {
        return normalize_name(&resolver.resolve(&class_name)).to_owned();
    }
    normalize_name(&string_node_value(node)).to_owned()
}

fn string_node_value(node: &SyntaxNode) -> String {
    phpquery::string_at(node)
        .map(|literal| phpquery::string_value(&literal))
        .unwrap_or_default()
}

fn yaml_tag_mapping_at(node: &SyntaxNode) -> Option<SyntaxNode> {
    node.ancestors().find(|current| {
        yamlquery::is_mapping(current) && yamlquery::property(current, "name").is_some()
    })
}

fn yaml_service_at(root: &SyntaxNode, node: &SyntaxNode) -> (String, String) {
    let path = yamlquery::pair_path(node);
    if path.len() < 2 || path[0] != "services" {
        return (String::new(), String::new());
    }
    let service_id = path[1].clone();
    let definition = yamlquery::root_value(root)
        .and_then(|root| yamlquery::property(&root, "services"))
        .and_then(|services| yamlquery::property(&services, &service_id));
    let class = definition
        .and_then(|definition| yamlquery::property(&definition, "class"))
        .map(|class| yamlquery::scalar_value(&class))
        .unwrap_or_default();
    let mut class_name = normalize_name(&class).to_owned();
    if class_name.is_empty() && service_id.contains('\\') {
        class_name = normalize_name(&service_id).to_owned();
    }
    (class_name, service_id)
}

/// Go `filepath.Ext`: the suffix from the final `.` of the last element.
fn go_extension(path: &str) -> &str {
    let name = &path[path.rfind('/').map_or(0, |index| index + 1)..];
    name.rfind('.').map_or("", |index| &name[index..])
}
