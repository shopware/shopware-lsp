//! Handler and dispatch extraction (port of Go `messenger/parser.go`).

use std::sync::Arc;

use swlsp_indexer::ParsedFile;
use swlsp_parser_php::query as phpquery;
use swlsp_parser_php::syntax::{
    PHP_ARRAY, PHP_EXPRESSION_STATEMENT, PHP_NAME, PHP_OBJECT_CREATION, PHP_RETURN_STATEMENT,
    PHP_STRING, PHP_VARIABLE, PHP_YIELD_EXPRESSION,
};
use swlsp_parser_xml::query as xmlquery;
use swlsp_parser_yaml::query as yamlquery;
use swlsp_parser_yaml::syntax::YAML_SCALAR;
use swlsp_php::{NameResolver, PhpIndex};
use swlsp_php_semantic::Document;
use swlsp_php_types::{Kind as TypeKind, Type, equal_fold, go_to_lower};
use swlsp_syntax::{SyntaxNode, SyntaxNodeExt, TextRange};

use crate::index::{normalize_name, object_type_names, unique_names, unique_occurrences};
use crate::model::{Occurrence, OccurrenceKind, SourceKind};

pub(crate) const AS_MESSAGE_HANDLER_ATTRIBUTE: &str =
    "Symfony\\Component\\Messenger\\Attribute\\AsMessageHandler";
const MESSAGE_BUS_INTERFACE: &str = "Symfony\\Component\\Messenger\\MessageBusInterface";

pub(crate) fn parse_php(file: &ParsedFile, php_index: Option<&PhpIndex>) -> Vec<Occurrence> {
    let Some(tree) = file.syntax_tree() else {
        return Vec::new();
    };
    let root = tree.root();
    let resolver = NameResolver::new(&root);
    let mut result = Vec::new();
    for class in phpquery::classes(&root) {
        let class_name = resolved_class_name(Some(&class), &resolver);
        if class_name.is_empty() {
            continue;
        }
        let class_range = node_name_range(Some(&class));
        for attribute in phpquery::attributes(&class) {
            if !is_as_message_handler(&attribute, &resolver) {
                continue;
            }
            let mut method_name =
                string_argument(attribute_argument(&attribute, "method", Some(3)).as_ref());
            if method_name.is_empty() {
                method_name = "__invoke".to_owned();
            }
            let handler_range = method_name_range(&class, &method_name);
            let inferred = method_parameter_message_names(&class, &method_name, &resolver);
            append_handler_attributes(
                &mut result,
                &attribute,
                &class_name,
                &method_name,
                class_range,
                handler_range,
                inferred,
                &resolver,
            );
        }
        for method in phpquery::methods(&class) {
            let method_name = phpquery::method_name(&method);
            for attribute in phpquery::attributes(&method) {
                if !is_as_message_handler(&attribute, &resolver) {
                    continue;
                }
                append_handler_attributes(
                    &mut result,
                    &attribute,
                    &class_name,
                    &method_name,
                    class_range,
                    node_name_range(Some(&method)),
                    parameter_message_names(&method, &resolver),
                    &resolver,
                );
            }
        }
        result.extend(parse_legacy_subscriber(&class, &class_name, &resolver));
    }
    result.extend(parse_php_service_tags(&root, &resolver));
    result.extend(parse_dispatches(file, &root, &resolver, php_index));
    unique_occurrences(result)
}

#[allow(clippy::too_many_arguments)]
fn append_handler_attributes(
    result: &mut Vec<Occurrence>,
    attribute: &SyntaxNode,
    class_name: &str,
    method_name: &str,
    class_range: TextRange,
    handler_range: TextRange,
    inferred: Vec<String>,
    resolver: &NameResolver,
) {
    let handles = attribute_argument(attribute, "handles", Some(2));
    let mut names = class_expression_names(handles.as_ref(), resolver);
    let message_range = node_range(handles.as_ref());
    if names.is_empty() {
        names = inferred;
    }
    if names.is_empty() {
        names = vec![String::new()];
    }
    let bus = string_argument(attribute_argument(attribute, "bus", Some(0)).as_ref());
    let transport =
        string_argument(attribute_argument(attribute, "fromTransport", Some(1)).as_ref());
    let priority = expression_text(attribute_argument(attribute, "priority", Some(4)).as_ref());
    for name in names {
        let range = if message_range.is_empty() {
            attribute.range_trimmed_trivia()
        } else {
            message_range
        };
        result.push(Occurrence {
            kind: OccurrenceKind::Handler,
            source: SourceKind::Attribute,
            message: name,
            range,
            message_range,
            handler_range,
            class_range,
            class: class_name.to_owned(),
            method: method_name.to_owned(),
            bus: bus.clone(),
            transport: transport.clone(),
            priority: priority.clone(),
            ..Occurrence::default()
        });
    }
}

fn parse_legacy_subscriber(
    class: &SyntaxNode,
    class_name: &str,
    resolver: &NameResolver,
) -> Vec<Occurrence> {
    let mut result = Vec::new();
    let class_range = node_name_range(Some(class));
    for method in phpquery::methods(class) {
        if !equal_fold(&phpquery::method_name(&method), "getHandledMessages") {
            continue;
        }
        for statement in phpquery::nodes(&method, &[PHP_RETURN_STATEMENT]) {
            if phpquery::function_like_at(&statement).as_ref() != Some(&method) {
                continue;
            }
            let Some(array) = phpquery::direct_child(&statement, PHP_ARRAY) else {
                continue;
            };
            for item in phpquery::array_items(&array) {
                let message_node = phpquery::array_item_key(&item);
                let value = phpquery::array_item_value(&item);
                let message_names = class_expression_names(message_node.as_ref(), resolver);
                let (handler_name, handler_node) = handler_method_value(value.as_ref());
                for message_name in message_names {
                    result.push(Occurrence {
                        kind: OccurrenceKind::Handler,
                        source: SourceKind::Subscriber,
                        message: message_name,
                        range: node_range(message_node.as_ref()),
                        message_range: node_range(message_node.as_ref()),
                        handler_range: node_range(handler_node.as_ref()),
                        class_range,
                        class: class_name.to_owned(),
                        method: handler_name.clone(),
                        ..Occurrence::default()
                    });
                }
            }
        }
        for yield_node in phpquery::nodes(&method, &[PHP_YIELD_EXPRESSION]) {
            if phpquery::function_like_at(&yield_node).as_ref() != Some(&method) {
                continue;
            }
            let children: Vec<SyntaxNode> = yield_node.children().collect();
            if children.len() < 2 {
                continue;
            }
            let (handler_name, handler_node) = handler_method_value(Some(&children[1]));
            for message_name in class_expression_names(Some(&children[0]), resolver) {
                result.push(Occurrence {
                    kind: OccurrenceKind::Handler,
                    source: SourceKind::Subscriber,
                    message: message_name,
                    range: node_range(Some(&children[0])),
                    message_range: node_range(Some(&children[0])),
                    handler_range: node_range(handler_node.as_ref()),
                    class_range,
                    class: class_name.to_owned(),
                    method: handler_name.clone(),
                    ..Occurrence::default()
                });
            }
        }
    }
    result
}

fn handler_method_value(value: Option<&SyntaxNode>) -> (String, Option<SyntaxNode>) {
    let Some(value) = value else {
        return (String::new(), None);
    };
    if value.kind() == PHP_STRING {
        return (phpquery::string_value(value), Some(value.clone()));
    }
    let Some(array) = phpquery::array_at(value) else {
        return (String::new(), None);
    };
    if !node_contains(value, &array) {
        return (String::new(), None);
    }
    for item in phpquery::array_items(&array) {
        let key = phpquery::array_item_key(&item);
        let item_value = phpquery::array_item_value(&item);
        if equal_fold(&string_value_of(key.as_ref()), "method")
            && let Some(item_value) = item_value
            && item_value.kind() == PHP_STRING
        {
            return (phpquery::string_value(&item_value), Some(item_value));
        }
    }
    (String::new(), None)
}

fn parse_php_service_tags(root: &SyntaxNode, resolver: &NameResolver) -> Vec<Occurrence> {
    let mut result = Vec::new();
    for call in phpquery::calls(root, &[]) {
        if !equal_fold(&phpquery::call_method_name(&call), "tag")
            || string_argument(phpquery::argument_expression(&call, 0).as_ref())
                != "messenger.message_handler"
        {
            continue;
        }
        let class_name = service_tag_class(&call, resolver);
        let options = phpquery::argument_expression(&call, 1)
            .and_then(|argument| phpquery::array_at(&argument));
        let handles = php_array_property(options.as_ref(), "handles");
        let method_node = php_array_property(options.as_ref(), "method");
        let mut method_name = string_argument(method_node.as_ref());
        if method_name.is_empty() {
            method_name = "__invoke".to_owned();
        }
        let mut names = class_expression_names(handles.as_ref(), resolver);
        if names.is_empty() {
            names = vec![String::new()];
        }
        for name in names {
            let mut range = node_range(handles.as_ref());
            if range.is_empty() {
                range = call.range_trimmed_trivia();
            }
            result.push(Occurrence {
                kind: OccurrenceKind::Handler,
                source: SourceKind::ServiceTag,
                message: name,
                range,
                message_range: node_range(handles.as_ref()),
                handler_range: node_range(method_node.as_ref()),
                class: class_name.clone(),
                method: method_name.clone(),
                bus: string_argument(php_array_property(options.as_ref(), "bus").as_ref()),
                transport: string_argument(
                    php_array_property(options.as_ref(), "from_transport").as_ref(),
                ),
                priority: expression_text(
                    php_array_property(options.as_ref(), "priority").as_ref(),
                ),
                ..Occurrence::default()
            });
        }
    }
    result
}

fn parse_dispatches(
    file: &ParsedFile,
    root: &SyntaxNode,
    resolver: &NameResolver,
    php_index: Option<&PhpIndex>,
) -> Vec<Occurrence> {
    let mut document: Option<Arc<Document>> = None;
    let analyze = |document: &mut Option<Arc<Document>>| {
        if document.is_none()
            && let Some(php_index) = php_index
        {
            *document = Some(php_index.analyze_parsed_file(file));
        }
    };
    let mut result = Vec::new();
    for call in phpquery::calls(root, &[]) {
        if !equal_fold(&phpquery::call_method_name(&call), "dispatch") {
            continue;
        }
        let receiver = phpquery::call_receiver(&call);
        let (mut is_bus, known) = classify_message_bus_receiver(&call, receiver.as_ref(), resolver);
        if !known {
            analyze(&mut document);
            if let (Some(current), Some(receiver)) = (document.as_ref(), receiver.as_ref()) {
                is_bus = is_message_bus_type(&current.type_of(receiver).ty, php_index);
            }
        }
        if !is_bus {
            continue;
        }
        let message_node = phpquery::argument_expression(&call, 0);
        let object = first_object_creation(message_node.as_ref());
        if object.is_none() {
            analyze(&mut document);
        }
        let names = expression_message_names(message_node.as_ref(), resolver, document.as_deref());
        for name in names {
            let range = match &object {
                Some(object) => object.range_trimmed_trivia(),
                None => node_range(message_node.as_ref()),
            };
            result.push(Occurrence {
                kind: OccurrenceKind::Dispatch,
                source: SourceKind::Dispatch,
                message: name,
                range,
                message_range: range,
                class: resolved_class_name(phpquery::class_at(&call).as_ref(), resolver),
                method: phpquery::method_name(&call),
                ..Occurrence::default()
            });
        }
    }
    result
}

/// Whether the receiver is a message bus, and whether that is known without
/// semantic inference.
fn classify_message_bus_receiver(
    call: &SyntaxNode,
    receiver: Option<&SyntaxNode>,
    resolver: &NameResolver,
) -> (bool, bool) {
    let Some(receiver) = receiver else {
        return (false, true);
    };
    let declared = declared_receiver_type(call, receiver, resolver);
    if !declared.is_empty() {
        return (is_message_bus_name(&declared), true);
    }
    let text = go_to_lower(&receiver.text().to_string().replace('_', ""));
    if text.contains("messagebus") {
        (true, true)
    } else if text.contains("eventdispatcher") || text.contains("eventbus") {
        (false, true)
    } else {
        (false, false)
    }
}

fn is_message_bus_type(value: &Type, php_index: Option<&PhpIndex>) -> bool {
    if matches!(value.kind(), TypeKind::Union | TypeKind::Intersection) {
        return value
            .arguments()
            .iter()
            .any(|part| is_message_bus_type(part, php_index));
    }
    let name = normalize_name(value.name());
    if is_message_bus_name(name) {
        return true;
    }
    match php_index {
        Some(php_index) if !name.is_empty() => php_index
            .semantic_snapshot()
            .relations()
            .is_subtype(value, &Type::named(MESSAGE_BUS_INTERFACE, Vec::new())),
        _ => false,
    }
}

fn is_message_bus_name(value: &str) -> bool {
    let value = go_to_lower(normalize_name(value));
    value == go_to_lower(MESSAGE_BUS_INTERFACE) || value.ends_with("\\messagebusinterface")
}

fn expression_message_names(
    node: Option<&SyntaxNode>,
    resolver: &NameResolver,
    document: Option<&Document>,
) -> Vec<String> {
    if let Some(object) = first_object_creation(node) {
        return vec![
            normalize_name(&resolver.resolve(&phpquery::object_class_name(&object))).to_owned(),
        ];
    }
    let (Some(document), Some(node)) = (document, node) else {
        return Vec::new();
    };
    unique_names(object_type_names(&document.type_of(node).ty))
}

fn class_expression_names(node: Option<&SyntaxNode>, resolver: &NameResolver) -> Vec<String> {
    let Some(node) = node else {
        return Vec::new();
    };
    let class_name = phpquery::class_constant_name(node);
    if !class_name.is_empty() {
        return vec![normalize_name(&resolver.resolve(&class_name)).to_owned()];
    }
    if node.kind() == PHP_STRING {
        let value = phpquery::string_value(node);
        let value = normalize_name(&value);
        if value.contains('\\') {
            return vec![value.to_owned()];
        }
    }
    Vec::new()
}

fn method_parameter_message_names(
    class: &SyntaxNode,
    method_name: &str,
    resolver: &NameResolver,
) -> Vec<String> {
    phpquery::methods(class)
        .into_iter()
        .find(|method| equal_fold(&phpquery::method_name(method), method_name))
        .map(|method| parameter_message_names(&method, resolver))
        .unwrap_or_default()
}

fn parameter_message_names(method: &SyntaxNode, resolver: &NameResolver) -> Vec<String> {
    let Some(first) = phpquery::parameters(method).next() else {
        return Vec::new();
    };
    let mut result = Vec::new();
    for part in type_parts(&phpquery::parameter_type(&first)) {
        let part = part.trim();
        let part = part.strip_prefix('?').unwrap_or(part);
        let bare = part.strip_prefix('\\').unwrap_or(part);
        if matches!(
            go_to_lower(bare).as_str(),
            "" | "array"
                | "bool"
                | "callable"
                | "false"
                | "float"
                | "int"
                | "iterable"
                | "mixed"
                | "never"
                | "null"
                | "object"
                | "string"
                | "true"
                | "void"
        ) {
            continue;
        }
        result.push(normalize_name(&resolver.resolve(part)).to_owned());
    }
    unique_names(result)
}

/// Go `strings.FieldsFunc` on `|` and `&`.
fn type_parts(value: &str) -> impl Iterator<Item = &str> {
    value.split(['|', '&']).filter(|part| !part.is_empty())
}

pub(crate) fn parse_yaml(file: &ParsedFile) -> Vec<Occurrence> {
    let Some(tree) = file.syntax_tree() else {
        return Vec::new();
    };
    let root = tree.root();
    let Some(services) = yamlquery::root_value(&root)
        .and_then(|root| yamlquery::property(&root, "services"))
        .filter(yamlquery::is_mapping)
    else {
        return Vec::new();
    };
    let mut result = Vec::new();
    for service_pair in yamlquery::pairs(&services) {
        let service_id = yamlquery::pair_key(&service_pair)
            .map(|key| yamlquery::scalar_value(&key))
            .unwrap_or_default();
        let Some(definition) = yamlquery::pair_value(&service_pair).filter(yamlquery::is_mapping)
        else {
            continue;
        };
        let mut class_name =
            normalize_name(&yaml_scalar(yamlquery::property(&definition, "class"))).to_owned();
        if class_name.is_empty() && service_id.contains('\\') {
            class_name = normalize_name(&service_id).to_owned();
        }
        let tags = yamlquery::property(&definition, "tags");
        match tags {
            Some(tags) if yamlquery::is_sequence(&tags) => {
                for item in yamlquery::items(&tags) {
                    result.extend(parse_yaml_handler_tag(
                        yamlquery::item_value(&item).as_ref(),
                        &service_id,
                        &class_name,
                    ));
                }
            }
            tags => result.extend(parse_yaml_handler_tag(
                tags.as_ref(),
                &service_id,
                &class_name,
            )),
        }
    }
    unique_occurrences(result)
}

fn yaml_scalar(node: Option<SyntaxNode>) -> String {
    node.map(|node| yamlquery::scalar_value(&node))
        .unwrap_or_default()
}

fn parse_yaml_handler_tag(
    tag: Option<&SyntaxNode>,
    service: &str,
    class_name: &str,
) -> Vec<Occurrence> {
    let Some(tag) = tag else {
        return Vec::new();
    };
    if tag.kind() == YAML_SCALAR {
        if yamlquery::scalar_value(tag) != "messenger.message_handler" {
            return Vec::new();
        }
        return vec![Occurrence {
            kind: OccurrenceKind::Handler,
            source: SourceKind::ServiceTag,
            range: tag.range_trimmed_trivia(),
            class: class_name.to_owned(),
            method: "__invoke".to_owned(),
            service: service.to_owned(),
            ..Occurrence::default()
        }];
    }
    if !yamlquery::is_mapping(tag)
        || yaml_scalar(yamlquery::property(tag, "name")) != "messenger.message_handler"
    {
        return Vec::new();
    }
    let handles = yamlquery::property(tag, "handles");
    let method_node = yamlquery::property(tag, "method");
    let message = normalize_name(&yaml_scalar(handles.clone())).to_owned();
    let mut method_name = yaml_scalar(method_node.clone());
    if method_name.is_empty() {
        method_name = "__invoke".to_owned();
    }
    let mut range = node_range(handles.as_ref());
    if range.is_empty() {
        range = tag.range_trimmed_trivia();
    }
    vec![Occurrence {
        kind: OccurrenceKind::Handler,
        source: SourceKind::ServiceTag,
        message,
        range,
        message_range: node_range(handles.as_ref()),
        handler_range: node_range(method_node.as_ref()),
        class: class_name.to_owned(),
        method: method_name,
        service: service.to_owned(),
        bus: yaml_scalar(yamlquery::property(tag, "bus")),
        transport: yaml_scalar(yamlquery::property(tag, "from_transport")),
        priority: yaml_scalar(yamlquery::property(tag, "priority")),
        ..Occurrence::default()
    }]
}

pub(crate) fn parse_xml(file: &ParsedFile) -> Vec<Occurrence> {
    let Some(tree) = file.syntax_tree() else {
        return Vec::new();
    };
    let root = tree.root();
    let mut result = Vec::new();
    for service in xmlquery::elements(&root, &["service"]) {
        let attributes = xmlquery::attribute_values(&service);
        let value = |values: &std::collections::BTreeMap<String, String>, name: &str| {
            values.get(name).cloned().unwrap_or_default()
        };
        let service_id = value(&attributes, "id");
        let mut class_name = normalize_name(&value(&attributes, "class")).to_owned();
        if class_name.is_empty() && service_id.contains('\\') {
            class_name = normalize_name(&service_id).to_owned();
        }
        for tag in xmlquery::child_elements(&service, &["tag"]) {
            let values = xmlquery::attribute_values(&tag);
            if value(&values, "name") != "messenger.message_handler" {
                continue;
            }
            let handles = xmlquery::attribute(&tag, "handles");
            let method_node = xmlquery::attribute(&tag, "method");
            let mut method_name = value(&values, "method");
            if method_name.is_empty() {
                method_name = "__invoke".to_owned();
            }
            let mut range = node_range(handles.as_ref());
            if range.is_empty() {
                range = tag.range_trimmed_trivia();
            }
            result.push(Occurrence {
                kind: OccurrenceKind::Handler,
                source: SourceKind::ServiceTag,
                message: normalize_name(&value(&values, "handles")).to_owned(),
                range,
                message_range: node_range(handles.as_ref()),
                handler_range: node_range(method_node.as_ref()),
                class: class_name.clone(),
                method: method_name,
                service: service_id.clone(),
                bus: value(&values, "bus"),
                transport: value(&values, "from-transport"),
                priority: value(&values, "priority"),
                ..Occurrence::default()
            });
        }
    }
    unique_occurrences(result)
}

pub(crate) fn is_as_message_handler(attribute: &SyntaxNode, resolver: &NameResolver) -> bool {
    equal_fold(
        normalize_name(&resolver.resolve(&phpquery::attribute_name(attribute))),
        AS_MESSAGE_HANDLER_ATTRIBUTE,
    )
}

/// The named argument `name`, else the positional argument `fallback` when
/// it is not itself named.
fn attribute_argument(
    attribute: &SyntaxNode,
    name: &str,
    fallback: Option<usize>,
) -> Option<SyntaxNode> {
    for (index, argument) in phpquery::arguments(attribute).enumerate() {
        if equal_fold(&phpquery::argument_name(&argument), name) {
            return phpquery::argument_expression(attribute, index);
        }
    }
    let fallback = fallback?;
    let argument = phpquery::argument(attribute, fallback)?;
    if !phpquery::argument_name(&argument).is_empty() {
        return None;
    }
    phpquery::argument_expression(attribute, fallback)
}

pub(crate) fn string_argument(node: Option<&SyntaxNode>) -> String {
    match node {
        Some(node) if node.kind() == PHP_STRING => phpquery::string_value(node),
        _ => String::new(),
    }
}

fn string_value_of(node: Option<&SyntaxNode>) -> String {
    node.map(phpquery::string_value).unwrap_or_default()
}

fn expression_text(node: Option<&SyntaxNode>) -> String {
    node.map(|node| node.text().to_string().trim().to_owned())
        .unwrap_or_default()
}

pub(crate) fn node_range(node: Option<&SyntaxNode>) -> TextRange {
    node.map(SyntaxNodeExt::range_trimmed_trivia)
        .unwrap_or_default()
}

fn node_name_range(node: Option<&SyntaxNode>) -> TextRange {
    node_range(
        node.and_then(|node| phpquery::direct_child(node, PHP_NAME))
            .as_ref(),
    )
}

fn method_name_range(class: &SyntaxNode, name: &str) -> TextRange {
    phpquery::methods(class)
        .into_iter()
        .find(|method| equal_fold(&phpquery::method_name(method), name))
        .map(|method| node_name_range(Some(&method)))
        .unwrap_or_default()
}

fn php_array_property(array: Option<&SyntaxNode>, name: &str) -> Option<SyntaxNode> {
    let array = array?;
    phpquery::array_items(array)
        .into_iter()
        .find(|item| {
            equal_fold(
                &string_value_of(phpquery::array_item_key(item).as_ref()),
                name,
            )
        })
        .and_then(|item| phpquery::array_item_value(&item))
}

pub(crate) fn service_tag_class(tag_call: &SyntaxNode, resolver: &NameResolver) -> String {
    let statement = tag_call
        .ancestors()
        .find(|node| node.kind() == PHP_EXPRESSION_STATEMENT)
        .or_else(|| phpquery::function_like_at(tag_call));
    let Some(statement) = statement else {
        return String::new();
    };
    for call in phpquery::calls(&statement, &[]) {
        match go_to_lower(&phpquery::call_method_name(&call)).as_str() {
            "set" => {
                let class_name =
                    class_argument(phpquery::argument_expression(&call, 1).as_ref(), resolver);
                if !class_name.is_empty() {
                    return class_name;
                }
                let class_name =
                    class_argument(phpquery::argument_expression(&call, 0).as_ref(), resolver);
                if !class_name.is_empty() {
                    return class_name;
                }
            }
            "get" => {
                let class_name =
                    class_argument(phpquery::argument_expression(&call, 0).as_ref(), resolver);
                if !class_name.is_empty() {
                    return class_name;
                }
            }
            _ => {}
        }
    }
    String::new()
}

fn class_argument(node: Option<&SyntaxNode>, resolver: &NameResolver) -> String {
    let Some(node) = node else {
        return String::new();
    };
    let class_name = phpquery::class_constant_name(node);
    if !class_name.is_empty() {
        return normalize_name(&resolver.resolve(&class_name)).to_owned();
    }
    if node.kind() == PHP_STRING {
        let value = phpquery::string_value(node);
        if value.contains('\\') {
            return normalize_name(&value).to_owned();
        }
    }
    String::new()
}

fn declared_receiver_type(
    call: &SyntaxNode,
    receiver: &SyntaxNode,
    resolver: &NameResolver,
) -> String {
    let text = compact_expression(&receiver.text().to_string());
    if text.starts_with("$this->") {
        let member = &text[text.rfind("->").map_or(0, |index| index + 2)..];
        let Some(class) = phpquery::class_at(call) else {
            return String::new();
        };
        for property in phpquery::properties(&class) {
            for variable in phpquery::property_variables(&property) {
                if equal_fold(&phpquery::variable_name(&variable), member) {
                    return resolve_first_type(&phpquery::property_type(&property), resolver);
                }
            }
        }
        for method in phpquery::methods(&class) {
            if equal_fold(&phpquery::method_name(&method), "__construct") {
                let value = parameter_type_by_name(Some(&method), member, resolver);
                if !value.is_empty() {
                    return value;
                }
            }
        }
        return String::new();
    }
    if receiver.kind() != PHP_VARIABLE {
        return String::new();
    }
    parameter_type_by_name(
        phpquery::function_like_at(call).as_ref(),
        &phpquery::variable_name(receiver),
        resolver,
    )
}

fn parameter_type_by_name(
    function: Option<&SyntaxNode>,
    name: &str,
    resolver: &NameResolver,
) -> String {
    let Some(function) = function else {
        return String::new();
    };
    let name = name.strip_prefix('$').unwrap_or(name);
    for parameter in phpquery::parameters(function) {
        let parameter_name = phpquery::parameter_name(&parameter);
        if equal_fold(
            parameter_name.strip_prefix('$').unwrap_or(&parameter_name),
            name,
        ) {
            return resolve_first_type(&phpquery::parameter_type(&parameter), resolver);
        }
    }
    String::new()
}

fn resolve_first_type(value: &str, resolver: &NameResolver) -> String {
    resolve_type_names(value, resolver)
        .into_iter()
        .next()
        .unwrap_or_default()
}

fn resolve_type_names(value: &str, resolver: &NameResolver) -> Vec<String> {
    let mut result = Vec::new();
    for candidate in type_parts(value) {
        let candidate = candidate.trim();
        let candidate = candidate.strip_prefix('?').unwrap_or(candidate);
        let bare = candidate.strip_prefix('\\').unwrap_or(candidate);
        if matches!(
            go_to_lower(bare).as_str(),
            "" | "array"
                | "bool"
                | "callable"
                | "false"
                | "float"
                | "int"
                | "iterable"
                | "mixed"
                | "never"
                | "null"
                | "object"
                | "self"
                | "static"
                | "string"
                | "true"
                | "void"
        ) {
            continue;
        }
        result.push(normalize_name(&resolver.resolve(candidate)).to_owned());
    }
    unique_names(result)
}

fn first_object_creation(node: Option<&SyntaxNode>) -> Option<SyntaxNode> {
    let node = node?;
    if node.kind() == PHP_OBJECT_CREATION {
        return Some(node.clone());
    }
    phpquery::object_creations(node, &[]).into_iter().next()
}

fn compact_expression(value: &str) -> String {
    value
        .chars()
        .filter(|character| !matches!(character, ' ' | '\t' | '\r' | '\n'))
        .collect()
}

pub(crate) fn resolved_class_name(class: Option<&SyntaxNode>, resolver: &NameResolver) -> String {
    let Some(class) = class else {
        return String::new();
    };
    let resolved = resolver.resolve(&phpquery::class_name(class));
    resolved.strip_prefix('\\').unwrap_or(&resolved).to_owned()
}

pub(crate) fn node_contains(parent: &SyntaxNode, child: &SyntaxNode) -> bool {
    parent.text_range().contains_range(child.text_range())
}
