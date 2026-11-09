//! Event references at a cursor.

use std::collections::HashSet;

use swlsp_parser_php::query as phpquery;
use swlsp_parser_php::syntax::{PHP_ARRAY, PHP_RETURN_STATEMENT};
use swlsp_parser_xml::query as xmlquery;
use swlsp_parser_yaml::query as yamlquery;
use swlsp_parser_yaml::syntax::YAML_SCALAR;
use swlsp_php::{NameResolver, PhpContext, PhpIndex, name_resolver_for};
use swlsp_php_resolver::MemberResolver;
use swlsp_php_semantic::{Symbol, SymbolKind, Visibility};
use swlsp_php_types::Type;
use swlsp_syntax::{SyntaxNode, SyntaxNodeExt};
use swlsp_textutil::{eq_ignore_case, lowercase};

use crate::index::{Index, normalize_name};
use crate::model::{Occurrence, OccurrenceKind};
use crate::parser::{
    EVENT_SUBSCRIBER_INTERFACE, is_as_event_listener, is_dispatcher_type, is_subscriber_class,
    php_string_value, resolved_class_name, service_tag_class,
};

#[derive(Clone, Copy, Debug, Default, PartialEq, Eq)]
pub enum ReferenceRole {
    #[default]
    None,
    Event,
    ListenerMethod,
}

#[derive(Clone, Copy, Debug, Default, PartialEq, Eq)]
pub enum ReferenceOrigin {
    #[default]
    Unknown,
    Dispatch,
    Listener,
}

#[derive(Clone, Debug, PartialEq, Eq)]
pub struct Reference {
    pub role: ReferenceRole,
    pub origin: ReferenceOrigin,
    pub name: String,
    pub node: SyntaxNode,
    pub container: SyntaxNode,
    pub class: String,
    pub method: String,
}

/// The event reference at `node` in the document at `path`.
pub fn reference_at(
    context: Option<&PhpContext>,
    path: &str,
    root: &SyntaxNode,
    node: &SyntaxNode,
) -> Option<Reference> {
    match lowercase(extension_with_dot(path)).as_str() {
        ".php" => php_reference_at(context, root, node),
        ".yaml" | ".yml" => yaml_reference_at(node),
        ".xml" => xml_reference_at(node),
        _ => None,
    }
}

pub fn php_reference_at(
    context: Option<&PhpContext>,
    root: &SyntaxNode,
    node: &SyntaxNode,
) -> Option<Reference> {
    let literal = phpquery::string_at(node)?;
    let name = phpquery::string_value(&literal);
    let resolver = name_resolver_for(context, root);
    let class_name = resolved_class_name(phpquery::class_at(&literal).as_ref(), &resolver);

    if let Some(attribute) = phpquery::attribute_at(&literal)
        && is_as_event_listener(&attribute, &resolver)
    {
        let argument = phpquery::argument_index(&attribute, &literal);
        let argument_name = argument
            .and_then(|index| phpquery::argument(&attribute, index))
            .map(|argument| phpquery::argument_name(&argument))
            .unwrap_or_default();
        let role = if eq_ignore_case(&argument_name, "event")
            || argument_name.is_empty() && argument == Some(0)
        {
            Some(ReferenceRole::Event)
        } else if eq_ignore_case(&argument_name, "method")
            || argument_name.is_empty() && argument == Some(1)
        {
            Some(ReferenceRole::ListenerMethod)
        } else {
            None
        };
        if let Some(role) = role {
            return Some(Reference {
                role,
                origin: ReferenceOrigin::Listener,
                name,
                node: literal,
                container: attribute,
                class: class_name,
                method: String::new(),
            });
        }
    }

    if let Some(reference) = php_service_tag_reference(&literal, &resolver) {
        return Some(reference);
    }

    if let Some(call) = phpquery::call_at(&literal)
        && eq_ignore_case(&phpquery::call_method_name(&call), "dispatch")
    {
        let index = phpquery::argument_index(&call, &literal);
        let argument_name = index
            .and_then(|index| phpquery::argument(&call, index))
            .map(|argument| phpquery::argument_name(&argument))
            .unwrap_or_default();
        let first = phpquery::argument_expression(&call, 0);
        let is_legacy_first = index == Some(0) && first.as_ref() == Some(&literal);
        let is_modern_name = index == Some(1) || eq_ignore_case(&argument_name, "eventName");
        if (is_legacy_first || is_modern_name) && validate_dispatch_reference(context, &call) {
            return Some(Reference {
                role: ReferenceRole::Event,
                origin: ReferenceOrigin::Dispatch,
                name,
                method: phpquery::method_name(&literal),
                node: literal,
                container: call,
                class: class_name,
            });
        }
    }

    let method = phpquery::method_at(&literal)?;
    if !eq_ignore_case(&phpquery::method_name(&method), "getSubscribedEvents")
        || !validate_subscriber_class(context, phpquery::class_at(&method).as_ref(), &resolver)
    {
        return None;
    }
    subscriber_reference(&literal, &method, &class_name)
}

fn php_service_tag_reference(literal: &SyntaxNode, resolver: &NameResolver) -> Option<Reference> {
    let call = phpquery::call_at(literal)?;
    if !eq_ignore_case(&phpquery::call_method_name(&call), "tag")
        || php_string_value(phpquery::argument_expression(&call, 0).as_ref())
            != "kernel.event_listener"
    {
        return None;
    }
    let options = phpquery::argument_expression(&call, 1)
        .and_then(|argument| phpquery::array_at(&argument))?;
    if !node_contains(&options, literal) {
        return None;
    }
    let item = phpquery::array_item_at(literal)?;
    let key = phpquery::array_item_key(&item)?;
    let value = phpquery::array_item_value(&item)?;
    if !node_contains(&value, literal) {
        return None;
    }
    let role = match php_string_value(Some(&key)).as_str() {
        "event" => ReferenceRole::Event,
        "method" => ReferenceRole::ListenerMethod,
        _ => return None,
    };
    Some(Reference {
        role,
        origin: ReferenceOrigin::Listener,
        name: phpquery::string_value(literal),
        node: literal.clone(),
        class: service_tag_class(&call, resolver),
        container: call,
        method: String::new(),
    })
}

fn subscriber_reference(
    literal: &SyntaxNode,
    method: &SyntaxNode,
    class_name: &str,
) -> Option<Reference> {
    for statement in phpquery::nodes(method, &[PHP_RETURN_STATEMENT]) {
        if phpquery::function_like_at(&statement).as_ref() != Some(method) {
            continue;
        }
        let Some(array) = phpquery::direct_child(&statement, PHP_ARRAY) else {
            continue;
        };
        if !node_contains(&array, literal) {
            continue;
        }
        for item in phpquery::array_items(&array) {
            if !node_contains(&item, literal) {
                continue;
            }
            let role = if phpquery::array_item_key(&item)
                .is_some_and(|key| node_contains(&key, literal))
            {
                ReferenceRole::Event
            } else if phpquery::array_item_value(&item)
                .is_some_and(|value| node_contains(&value, literal))
            {
                ReferenceRole::ListenerMethod
            } else {
                continue;
            };
            return Some(Reference {
                role,
                origin: ReferenceOrigin::Listener,
                name: phpquery::string_value(literal),
                node: literal.clone(),
                container: item,
                class: class_name.to_owned(),
                method: phpquery::method_name(method),
            });
        }
    }
    None
}

pub fn yaml_reference_at(node: &SyntaxNode) -> Option<Reference> {
    if node.kind() != YAML_SCALAR {
        return None;
    }
    let pair = yamlquery::ancestor_pair(node)?;
    if yamlquery::pair_value(&pair).as_ref() != Some(node) {
        return None;
    }
    let role = match yamlquery::pair_key(&pair)
        .map(|key| yamlquery::scalar_value(&key))
        .unwrap_or_default()
        .as_str()
    {
        "event" => ReferenceRole::Event,
        "method" => ReferenceRole::ListenerMethod,
        _ => return None,
    };
    let tag = yaml_listener_tag_at(&pair)?;
    Some(Reference {
        role,
        origin: ReferenceOrigin::Listener,
        name: yamlquery::scalar_value(node),
        node: node.clone(),
        container: tag,
        class: String::new(),
        method: String::new(),
    })
}

pub fn xml_reference_at(node: &SyntaxNode) -> Option<Reference> {
    let attribute = xmlquery::attribute_at(node)?;
    let role = match xmlquery::attribute_name(&attribute)
        .unwrap_or_default()
        .as_str()
    {
        "event" => ReferenceRole::Event,
        "method" => ReferenceRole::ListenerMethod,
        _ => return None,
    };
    let tag = xmlquery::element_at(&attribute)?;
    if xmlquery::element_name(&tag).as_deref() != Some("tag")
        || xmlquery::attribute(&tag, "name").and_then(|name| xmlquery::attribute_value(&name))
            != Some("kernel.event_listener".to_owned())
    {
        return None;
    }
    Some(Reference {
        role,
        origin: ReferenceOrigin::Listener,
        name: xmlquery::attribute_value(&attribute).unwrap_or_default(),
        node: attribute,
        container: tag,
        class: String::new(),
        method: String::new(),
    })
}

/// Public listener-candidate methods of `class_name` (including inherited
/// ones), sorted by name case-insensitively. Magic methods other than
/// `__invoke`, setters and `getSubscribedEvents` are excluded.
pub fn public_methods(index: Option<&PhpIndex>, class_name: &str) -> Vec<Symbol> {
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
    .all(&Type::named(normalize_name(class_name), Vec::new()));
    let mut seen = HashSet::with_capacity(members.len());
    let mut result: Vec<Symbol> = Vec::new();
    for member in members {
        let symbol = member.symbol;
        let name: &str = &symbol.name;
        if symbol.kind != SymbolKind::Method
            || symbol.visibility != Visibility::Public
            || eq_ignore_case(name, "getSubscribedEvents")
            || name.starts_with("__") && name != "__invoke"
            || lowercase(name).starts_with("set")
        {
            continue;
        }
        if !seen.insert(lowercase(name)) {
            continue;
        }
        result.push(symbol);
    }
    result.sort_by_cached_key(|symbol| lowercase(&symbol.name));
    result
}

/// The listener a reference points at: the reference's own class and method
/// name, else the indexed listener declared at the reference node.
pub fn resolve_listener(
    index: Option<&Index>,
    path: &str,
    reference: &Reference,
) -> swlsp_indexer::Result<Option<Occurrence>> {
    if !reference.class.is_empty() {
        return Ok(Some(Occurrence {
            kind: OccurrenceKind::Listener,
            class: reference.class.clone(),
            method: reference.name.clone(),
            ..Occurrence::default()
        }));
    }
    let Some(index) = index else {
        return Ok(None);
    };
    index.listener_at(path, reference.node.range_trimmed_trivia().start())
}

fn validate_dispatch_reference(context: Option<&PhpContext>, call: &SyntaxNode) -> bool {
    let Some(context) = context else {
        return false;
    };
    let Some(receiver) = phpquery::call_receiver(call) else {
        return false;
    };
    let receiver_type = context.document.type_of(&receiver).ty;
    if receiver_type.is_unknown() {
        return false;
    }
    is_dispatcher_type(&receiver_type, Some(&context.snapshot))
}

fn validate_subscriber_class(
    context: Option<&PhpContext>,
    class: Option<&SyntaxNode>,
    resolver: &NameResolver,
) -> bool {
    let Some(class) = class else {
        return false;
    };
    let class_name = resolved_class_name(Some(class), resolver);
    if let Some(context) = context
        && !class_name.is_empty()
        && context.snapshot.relations().is_subtype(
            &Type::named(&class_name, Vec::new()),
            &Type::named(EVENT_SUBSCRIBER_INTERFACE, Vec::new()),
        )
    {
        return true;
    }
    is_subscriber_class(class, resolver)
}

fn yaml_listener_tag_at(node: &SyntaxNode) -> Option<SyntaxNode> {
    node.ancestors().find(|current| {
        yamlquery::is_mapping(current)
            && yamlquery::property(current, "name").map(|name| yamlquery::scalar_value(&name))
                == Some("kernel.event_listener".to_owned())
    })
}

fn node_contains(parent: &SyntaxNode, child: &SyntaxNode) -> bool {
    parent.text_range().contains_range(child.text_range())
}

/// The suffix of the last slash-separated element from its final `.`
/// (`.xml`); empty when there is none.
fn extension_with_dot(path: &str) -> &str {
    let name = &path[path.rfind('/').map_or(0, |index| index + 1)..];
    name.rfind('.').map_or("", |index| &name[index..])
}
