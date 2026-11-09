//! Listener and dispatch extraction.

use std::collections::{BTreeMap, HashMap};
use std::sync::LazyLock;

use regex::Regex;
use swlsp_indexer::ParsedFile;
use swlsp_parser_php::query as phpquery;
use swlsp_parser_php::syntax::{
    PHP_ARRAY, PHP_CLASS_CONST_DECLARATION, PHP_EXPRESSION_STATEMENT, PHP_OBJECT_CREATION,
    PHP_RETURN_STATEMENT, PHP_STRING, PHP_VARIABLE,
};
use swlsp_parser_xml::query as xmlquery;
use swlsp_parser_yaml::query as yamlquery;
use swlsp_parser_yaml::syntax::YAML_SCALAR;
use swlsp_php::NameResolver;
use swlsp_php_semantic::Snapshot;
use swlsp_php_types::{Kind as TypeKind, Type};
use swlsp_syntax::{SyntaxNode, SyntaxNodeExt, TextRange};
use swlsp_textutil::{eq_ignore_case, lowercase};

use crate::index::{looks_like_class_name, normalize_name, record_key, unique_occurrences};
use crate::model::{Constant, Occurrence, OccurrenceKind, Record, SourceKind};

pub(crate) const EVENT_SUBSCRIBER_INTERFACE: &str =
    "Symfony\\Component\\EventDispatcher\\EventSubscriberInterface";
const AS_EVENT_LISTENER_ATTRIBUTE: &str =
    "Symfony\\Component\\EventDispatcher\\Attribute\\AsEventListener";

const DISPATCHER_INTERFACES: [&str; 3] = [
    "Symfony\\Contracts\\EventDispatcher\\EventDispatcherInterface",
    "Symfony\\Component\\EventDispatcher\\EventDispatcherInterface",
    "Psr\\EventDispatcher\\EventDispatcherInterface",
];

// PHP identifiers allow bytes 0x80-0xff, matched here as the code points
// U+0080-U+00FF; whitespace is ASCII only.
static CLASS_CONSTANT_NAME_PATTERN: LazyLock<Regex> = LazyLock::new(|| {
    Regex::new(
        r"(?i)([A-Za-z_\x{80}-\x{ff}][A-Za-z0-9_\x{80}-\x{ff}]*)[\t\n\x0C\r ]*=[\t\n\x0C\r ]*$",
    )
    .expect("valid class constant pattern")
});

#[derive(Clone, Debug, Default)]
struct EventSpec {
    name: String,
    expression: String,
    event_type: String,
    range: TextRange,
}

struct RecordCollector {
    path: String,
    records: HashMap<String, Record>,
}

impl RecordCollector {
    fn new(path: &str) -> Self {
        Self {
            path: path.to_owned(),
            records: HashMap::new(),
        }
    }

    fn add(&mut self, spec: &EventSpec, mut occurrence: Occurrence) {
        occurrence.file = self.path.clone();
        if occurrence.range.is_empty() {
            occurrence.range = spec.range;
        }
        if occurrence.event_type.is_empty() {
            occurrence.event_type = spec.event_type.clone();
        }
        let key = if !spec.name.is_empty() {
            format!("name:{}", lowercase(&spec.name))
        } else if !spec.expression.is_empty() {
            format!("expression:{}", lowercase(&spec.expression))
        } else {
            let target = if occurrence.class.is_empty() {
                &occurrence.service
            } else {
                &occurrence.class
            };
            if target.is_empty() && occurrence.method.is_empty() {
                return;
            }
            format!(
                "inferred:{}",
                lowercase(&format!("{target}::{}", occurrence.method))
            )
        };
        let record = self.records.entry(key).or_insert_with(|| Record {
            name: spec.name.clone(),
            expression: spec.expression.clone(),
            event_type: spec.event_type.clone(),
            file: self.path.clone(),
            occurrences: Vec::new(),
        });
        if record.event_type.is_empty() {
            record.event_type = occurrence.event_type.clone();
        }
        record.occurrences.push(occurrence);
    }

    fn values(self) -> Vec<Record> {
        let mut result: Vec<(String, Record)> = self
            .records
            .into_values()
            .map(|mut record| {
                record.occurrences = unique_occurrences(std::mem::take(&mut record.occurrences));
                (record_key(&record), record)
            })
            .collect();
        result.sort_by(|left, right| left.0.cmp(&right.0));
        result.into_iter().map(|(_, record)| record).collect()
    }
}

pub(crate) fn is_php_event_candidate(content: &str) -> bool {
    content.contains("getSubscribedEvents")
        || content.contains("AsEventListener")
        || content.contains("kernel.event_listener")
        || content.contains("->dispatch")
}

pub(crate) fn parse_php(file: &ParsedFile) -> Vec<Record> {
    let Some(tree) = file.syntax_tree() else {
        return Vec::new();
    };
    let root = tree.root();
    let resolver = NameResolver::new(&root);
    let mut collector = RecordCollector::new(file.path());
    for class in phpquery::classes(&root) {
        let class_name = resolved_class_name(Some(&class), &resolver);
        if class_name.is_empty() {
            continue;
        }
        let methods = class_method_map(&class);
        if is_subscriber_class(&class, &resolver) || has_subscriber_method(&class) {
            parse_subscriber(&mut collector, &class, &class_name, &methods, &resolver);
        }
        parse_listener_attributes(&mut collector, &class, &class_name, &methods, &resolver);
    }
    parse_php_service_tags(&mut collector, &root, &resolver);
    parse_dispatches(&mut collector, &root, &resolver);
    collector.values()
}

fn has_subscriber_method(class: &SyntaxNode) -> bool {
    phpquery::methods(class)
        .iter()
        .any(|method| eq_ignore_case(&phpquery::method_name(method), "getSubscribedEvents"))
}

/// The string-valued class constants of `class` (`NAME = 'value'`).
pub(crate) fn parse_class_constants(
    class: &SyntaxNode,
    class_name: &str,
    path: &str,
) -> Vec<Constant> {
    let Some(body) = phpquery::class_body(class) else {
        return Vec::new();
    };
    let mut result = Vec::new();
    for child in body.children() {
        if child.kind() != PHP_CLASS_CONST_DECLARATION {
            continue;
        }
        let text = child.text().to_string();
        let child_start = u32::from(child.text_range().start());
        for literal in phpquery::nodes(&child, &[PHP_STRING]) {
            let relative = (u32::from(literal.text_range().start()) - child_start) as usize;
            let Some(prefix) = text.get(..relative) else {
                continue;
            };
            let Some(captures) = CLASS_CONSTANT_NAME_PATTERN.captures(prefix) else {
                continue;
            };
            let value = phpquery::string_value(&literal);
            if value.is_empty() {
                continue;
            }
            result.push(Constant {
                expression: format!("{class_name}::{}", &captures[1]),
                value,
                file: path.to_owned(),
                range: literal.range_trimmed_trivia(),
            });
        }
    }
    result
}

fn parse_subscriber(
    collector: &mut RecordCollector,
    class: &SyntaxNode,
    class_name: &str,
    methods: &HashMap<String, SyntaxNode>,
    resolver: &NameResolver,
) {
    for method in phpquery::methods(class) {
        if !eq_ignore_case(&phpquery::method_name(&method), "getSubscribedEvents") {
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
                let Some(key) = phpquery::array_item_key(&item) else {
                    continue;
                };
                let spec = php_event_spec(Some(&key), resolver, class_name);
                if spec.name.is_empty() && spec.expression.is_empty() {
                    continue;
                }
                for listener in subscriber_methods(phpquery::array_item_value(&item).as_ref()) {
                    let event_type =
                        listener_event_type(methods.get(&lowercase(&listener.name)), resolver);
                    collector.add(
                        &spec,
                        Occurrence {
                            kind: OccurrenceKind::Listener,
                            source: SourceKind::Subscriber,
                            range: spec.range,
                            method_range: listener.range,
                            class: class_name.to_owned(),
                            method: listener.name,
                            priority: listener.priority,
                            event_type,
                            ..Occurrence::default()
                        },
                    );
                }
            }
        }
    }
}

struct SubscriberMethod {
    name: String,
    priority: String,
    range: TextRange,
}

fn subscriber_methods(node: Option<&SyntaxNode>) -> Vec<SubscriberMethod> {
    let Some(node) = node else {
        return Vec::new();
    };
    if node.kind() == PHP_STRING {
        return vec![SubscriberMethod {
            name: phpquery::string_value(node),
            priority: String::new(),
            range: node.range_trimmed_trivia(),
        }];
    }
    if node.kind() != PHP_ARRAY {
        return Vec::new();
    }
    let items = phpquery::array_items(node);
    let Some(first_item) = items.first() else {
        return Vec::new();
    };
    if let Some(first) = phpquery::array_item_value(first_item)
        && first.kind() == PHP_STRING
    {
        return vec![SubscriberMethod {
            name: phpquery::string_value(&first),
            priority: array_item_text(&items, 1),
            range: first.range_trimmed_trivia(),
        }];
    }
    let mut result = Vec::new();
    for item in &items {
        let Some(value) = phpquery::array_item_value(item) else {
            continue;
        };
        if value.kind() == PHP_STRING {
            result.push(SubscriberMethod {
                name: phpquery::string_value(&value),
                priority: String::new(),
                range: value.range_trimmed_trivia(),
            });
            continue;
        }
        if value.kind() != PHP_ARRAY {
            continue;
        }
        let nested = phpquery::array_items(&value);
        let Some(method) = nested.first().and_then(phpquery::array_item_value) else {
            continue;
        };
        if method.kind() != PHP_STRING {
            continue;
        }
        result.push(SubscriberMethod {
            name: phpquery::string_value(&method),
            priority: array_item_text(&nested, 1),
            range: method.range_trimmed_trivia(),
        });
    }
    result
}

fn array_item_text(items: &[SyntaxNode], index: usize) -> String {
    items
        .get(index)
        .and_then(phpquery::array_item_value)
        .map(|value| expression_text(Some(&value)))
        .unwrap_or_default()
}

fn parse_listener_attributes(
    collector: &mut RecordCollector,
    class: &SyntaxNode,
    class_name: &str,
    methods: &HashMap<String, SyntaxNode>,
    resolver: &NameResolver,
) {
    for attribute in phpquery::attributes(class) {
        if !is_as_event_listener(&attribute, resolver) {
            continue;
        }
        let method_node = call_argument(&attribute, "method", None);
        let mut method_name = php_string_value(method_node.as_ref());
        if method_name.is_empty() {
            method_name = "__invoke".to_owned();
        }
        let event_type = listener_event_type(methods.get(&lowercase(&method_name)), resolver);
        add_attribute_listener(
            collector,
            &attribute,
            class_name,
            method_name,
            method_node.as_ref(),
            event_type,
            resolver,
        );
    }
    for method in phpquery::methods(class) {
        for attribute in phpquery::attributes(&method) {
            if !is_as_event_listener(&attribute, resolver) {
                continue;
            }
            let method_node = call_argument(&attribute, "method", None);
            let mut method_name = php_string_value(method_node.as_ref());
            if method_name.is_empty() {
                method_name = phpquery::method_name(&method);
            }
            let event_type = listener_event_type(Some(&method), resolver);
            add_attribute_listener(
                collector,
                &attribute,
                class_name,
                method_name,
                method_node.as_ref(),
                event_type,
                resolver,
            );
        }
    }
}

fn add_attribute_listener(
    collector: &mut RecordCollector,
    attribute: &SyntaxNode,
    class_name: &str,
    method_name: String,
    method_node: Option<&SyntaxNode>,
    event_type: String,
    resolver: &NameResolver,
) {
    let mut spec = php_event_spec(
        call_argument(attribute, "event", Some(0)).as_ref(),
        resolver,
        class_name,
    );
    if spec.name.is_empty() && spec.expression.is_empty() && !event_type.is_empty() {
        spec.name = event_type.clone();
        spec.event_type = event_type.clone();
        spec.range = attribute.range_trimmed_trivia();
    }
    collector.add(
        &spec,
        Occurrence {
            kind: OccurrenceKind::Listener,
            source: SourceKind::Attribute,
            range: spec.range,
            method_range: node_range(method_node),
            class: class_name.to_owned(),
            method: method_name,
            priority: expression_text(call_argument(attribute, "priority", None).as_ref()),
            event_type,
            ..Occurrence::default()
        },
    );
}

fn parse_php_service_tags(
    collector: &mut RecordCollector,
    root: &SyntaxNode,
    resolver: &NameResolver,
) {
    for call in phpquery::calls(root, &[]) {
        if !eq_ignore_case(&phpquery::call_method_name(&call), "tag")
            || php_string_value(phpquery::argument_expression(&call, 0).as_ref())
                != "kernel.event_listener"
        {
            continue;
        }
        let class_name = service_tag_class(&call, resolver);
        let options = phpquery::argument_expression(&call, 1)
            .and_then(|argument| phpquery::array_at(&argument));
        let event_node = php_array_property(options.as_ref(), "event");
        let method_node = php_array_property(options.as_ref(), "method");
        let mut method_name = php_string_value(method_node.as_ref());
        if method_name.is_empty() {
            method_name = "__invoke".to_owned();
        }
        let mut spec = php_event_spec(event_node.as_ref(), resolver, &class_name);
        if spec.range.is_empty() {
            spec.range = call.range_trimmed_trivia();
        }
        collector.add(
            &spec,
            Occurrence {
                kind: OccurrenceKind::Listener,
                source: SourceKind::ServiceTag,
                range: spec.range,
                method_range: node_range(method_node.as_ref()),
                class: class_name,
                method: method_name,
                priority: expression_text(
                    php_array_property(options.as_ref(), "priority").as_ref(),
                ),
                ..Occurrence::default()
            },
        );
    }
}

fn parse_dispatches(collector: &mut RecordCollector, root: &SyntaxNode, resolver: &NameResolver) {
    for call in phpquery::calls(root, &[]) {
        if !eq_ignore_case(&phpquery::call_method_name(&call), "dispatch")
            || !is_event_dispatcher_call(&call, resolver)
        {
            continue;
        }
        let class_name = resolved_class_name(phpquery::class_at(&call).as_ref(), resolver);
        let first = phpquery::argument_expression(&call, 0);
        let second = phpquery::argument_expression(&call, 1);
        let mut spec = EventSpec::default();
        if first
            .as_ref()
            .is_some_and(|first| first.kind() == PHP_STRING)
        {
            spec = php_event_spec(first.as_ref(), resolver, &class_name);
            spec.event_type = expression_event_type(second.as_ref(), resolver);
        } else if phpquery::arguments(&call).count() > 1 {
            spec = php_event_spec(second.as_ref(), resolver, &class_name);
        }
        if spec.name.is_empty() && spec.expression.is_empty() {
            spec = php_event_spec(first.as_ref(), resolver, &class_name);
        }
        if spec.name.is_empty() && spec.expression.is_empty() {
            continue;
        }
        if spec.event_type.is_empty() {
            spec.event_type = expression_event_type(first.as_ref(), resolver);
        }
        let occurrence = Occurrence {
            kind: OccurrenceKind::Dispatch,
            source: SourceKind::Dispatch,
            range: spec.range,
            class: class_name,
            method: phpquery::method_name(&call),
            event_type: spec.event_type.clone(),
            ..Occurrence::default()
        };
        collector.add(&spec, occurrence);
    }
}

fn is_event_dispatcher_call(call: &SyntaxNode, resolver: &NameResolver) -> bool {
    let Some(receiver) = phpquery::call_receiver(call) else {
        return false;
    };
    let declared = declared_receiver_type(call, &receiver, resolver);
    if !declared.is_empty() {
        return lowercase(&declared).contains("eventdispatcher");
    }
    let text = lowercase(&receiver.text().to_string().replace('_', ""));
    text.contains("eventdispatcher")
        || (text.contains("dispatcher")
            && !text.contains("message")
            && !text.contains("bus")
            && !text.contains("queue"))
}

fn declared_receiver_type(
    call: &SyntaxNode,
    receiver: &SyntaxNode,
    resolver: &NameResolver,
) -> String {
    let text = compact_php_expression(&receiver.text().to_string());
    if text.starts_with("$this->") {
        let member = &text[text.rfind("->").map_or(0, |index| index + 2)..];
        let Some(class) = phpquery::class_at(call) else {
            return String::new();
        };
        for property in phpquery::properties(&class) {
            for variable in phpquery::property_variables(&property) {
                if eq_ignore_case(&phpquery::variable_name(&variable), member) {
                    return resolve_php_type(&phpquery::property_type(&property), resolver);
                }
            }
        }
        for method in phpquery::methods(&class) {
            if !eq_ignore_case(&phpquery::method_name(&method), "__construct") {
                continue;
            }
            let value = parameter_type_by_name(Some(&method), member, resolver);
            if !value.is_empty() {
                return value;
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
        if eq_ignore_case(
            parameter_name.strip_prefix('$').unwrap_or(&parameter_name),
            name,
        ) {
            return resolve_php_type(&phpquery::parameter_type(&parameter), resolver);
        }
    }
    String::new()
}

/// Whether `value` is (a subtype of) one of the EventDispatcher interfaces.
pub(crate) fn is_dispatcher_type(value: &Type, snapshot: Option<&Snapshot>) -> bool {
    if matches!(value.kind(), TypeKind::Union | TypeKind::Intersection) {
        return value
            .arguments()
            .iter()
            .any(|part| is_dispatcher_type(part, snapshot));
    }
    let name = value.name();
    let name = name.strip_prefix('\\').unwrap_or(name);
    for target in DISPATCHER_INTERFACES {
        if eq_ignore_case(name, target) {
            return true;
        }
        if let Some(snapshot) = snapshot
            && snapshot
                .relations()
                .is_subtype(value, &Type::named(target, Vec::new()))
        {
            return true;
        }
    }
    false
}

fn expression_event_type(node: Option<&SyntaxNode>, resolver: &NameResolver) -> String {
    match first_object_creation(node) {
        Some(object) => {
            normalize_name(&resolver.resolve(&phpquery::object_class_name(&object))).to_owned()
        }
        None => String::new(),
    }
}

fn first_object_creation(node: Option<&SyntaxNode>) -> Option<SyntaxNode> {
    let node = node?;
    if node.kind() == PHP_OBJECT_CREATION {
        return Some(node.clone());
    }
    phpquery::object_creations(node, &[]).into_iter().next()
}

fn php_event_spec(
    node: Option<&SyntaxNode>,
    resolver: &NameResolver,
    current_class: &str,
) -> EventSpec {
    let Some(node) = node else {
        return EventSpec::default();
    };
    if node.kind() == PHP_STRING {
        return EventSpec {
            name: phpquery::string_value(node),
            range: node.range_trimmed_trivia(),
            ..EventSpec::default()
        };
    }
    if let Some(object) = first_object_creation(Some(node)) {
        let name =
            normalize_name(&resolver.resolve(&phpquery::object_class_name(&object))).to_owned();
        return EventSpec {
            event_type: name.clone(),
            name,
            range: object.range_trimmed_trivia(),
            ..EventSpec::default()
        };
    }
    let text = compact_php_expression(&node.text().to_string());
    let Some(separator) = text.rfind("::") else {
        return EventSpec::default();
    };
    let member = &text[separator + 2..];
    let class_name = match lowercase(&text[..separator]).as_str() {
        "self" | "static" => current_class.to_owned(),
        "parent" => String::new(),
        _ => normalize_name(&resolver.resolve(&text[..separator])).to_owned(),
    };
    if class_name.is_empty() || member.is_empty() {
        return EventSpec::default();
    }
    if eq_ignore_case(member, "class") {
        return EventSpec {
            event_type: class_name.clone(),
            name: class_name,
            range: node.range_trimmed_trivia(),
            ..EventSpec::default()
        };
    }
    EventSpec {
        expression: format!("{class_name}::{member}"),
        range: node.range_trimmed_trivia(),
        ..EventSpec::default()
    }
}

fn compact_php_expression(value: &str) -> String {
    value
        .chars()
        .filter(|character| !matches!(character, ' ' | '\t' | '\r' | '\n'))
        .collect()
}

pub(crate) fn resolved_class_name(class: Option<&SyntaxNode>, resolver: &NameResolver) -> String {
    let Some(class) = class else {
        return String::new();
    };
    let name = phpquery::class_name(class);
    if name.is_empty() {
        return String::new();
    }
    normalize_name(&resolver.resolve(&name)).to_owned()
}

pub(crate) fn is_subscriber_class(class: &SyntaxNode, resolver: &NameResolver) -> bool {
    phpquery::class_implements(class).iter().any(|implemented| {
        eq_ignore_case(
            normalize_name(&resolver.resolve(implemented)),
            EVENT_SUBSCRIBER_INTERFACE,
        )
    })
}

pub(crate) fn is_as_event_listener(attribute: &SyntaxNode, resolver: &NameResolver) -> bool {
    eq_ignore_case(
        normalize_name(&resolver.resolve(&phpquery::attribute_name(attribute))),
        AS_EVENT_LISTENER_ATTRIBUTE,
    )
}

fn class_method_map(class: &SyntaxNode) -> HashMap<String, SyntaxNode> {
    phpquery::methods(class)
        .into_iter()
        .map(|method| (lowercase(&phpquery::method_name(&method)), method))
        .collect()
}

fn listener_event_type(method: Option<&SyntaxNode>, resolver: &NameResolver) -> String {
    let Some(first) = method.and_then(|method| phpquery::parameters(method).next()) else {
        return String::new();
    };
    resolve_php_type(&phpquery::parameter_type(&first), resolver)
}

/// The first class-like part of a type declaration, resolved.
fn resolve_php_type(value: &str, resolver: &NameResolver) -> String {
    for candidate in value.split(['|', '&']).filter(|part| !part.is_empty()) {
        let candidate = candidate.trim();
        let candidate = candidate.strip_prefix('?').unwrap_or(candidate);
        if matches!(
            lowercase(candidate.strip_prefix('\\').unwrap_or(candidate)).as_str(),
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
        return normalize_name(&resolver.resolve(candidate)).to_owned();
    }
    String::new()
}

/// The named argument `name`, else the positional argument `fallback` when
/// it is not itself named.
fn call_argument(call: &SyntaxNode, name: &str, fallback: Option<usize>) -> Option<SyntaxNode> {
    for (index, argument) in phpquery::arguments(call).enumerate() {
        if eq_ignore_case(&phpquery::argument_name(&argument), name) {
            return phpquery::argument_expression(call, index);
        }
    }
    let fallback = fallback?;
    let argument = phpquery::argument(call, fallback)?;
    if !phpquery::argument_name(&argument).is_empty() {
        return None;
    }
    phpquery::argument_expression(call, fallback)
}

pub(crate) fn php_string_value(node: Option<&SyntaxNode>) -> String {
    match node {
        Some(node) if node.kind() == PHP_STRING => phpquery::string_value(node),
        _ => String::new(),
    }
}

fn expression_text(node: Option<&SyntaxNode>) -> String {
    node.map(|node| node.text().to_string().trim().to_owned())
        .unwrap_or_default()
}

fn node_range(node: Option<&SyntaxNode>) -> TextRange {
    node.map(SyntaxNodeExt::range_trimmed_trivia)
        .unwrap_or_default()
}

fn php_array_property(array: Option<&SyntaxNode>, name: &str) -> Option<SyntaxNode> {
    let array = array?;
    phpquery::array_items(array)
        .into_iter()
        .find(|item| {
            phpquery::array_item_key(item).is_some_and(|key| {
                key.kind() == PHP_STRING && eq_ignore_case(&phpquery::string_value(&key), name)
            })
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
        let candidates: &[usize] = match lowercase(&phpquery::call_method_name(&call)).as_str() {
            "set" => &[1, 0],
            "get" => &[0],
            _ => &[],
        };
        for &index in candidates {
            let class_name = php_class_argument(
                phpquery::argument_expression(&call, index).as_ref(),
                resolver,
            );
            if !class_name.is_empty() {
                return class_name;
            }
        }
    }
    String::new()
}

fn php_class_argument(node: Option<&SyntaxNode>, resolver: &NameResolver) -> String {
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

fn yaml_scalar(node: Option<&SyntaxNode>) -> String {
    node.map(yamlquery::scalar_value).unwrap_or_default()
}

pub(crate) fn parse_yaml(file: &ParsedFile) -> Vec<Record> {
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
    let mut collector = RecordCollector::new(file.path());
    for service_pair in yamlquery::pairs(&services) {
        let service_id = yaml_scalar(yamlquery::pair_key(&service_pair).as_ref());
        let Some(definition) = yamlquery::pair_value(&service_pair).filter(yamlquery::is_mapping)
        else {
            continue;
        };
        let mut class_name = normalize_name(&yaml_scalar(
            yamlquery::property(&definition, "class").as_ref(),
        ))
        .to_owned();
        if class_name.is_empty() && service_id.contains('\\') {
            class_name = normalize_name(&service_id).to_owned();
        }
        match yamlquery::property(&definition, "tags") {
            Some(tags) if yamlquery::is_sequence(&tags) => {
                for item in yamlquery::items(&tags) {
                    parse_yaml_listener_tag(
                        &mut collector,
                        yamlquery::item_value(&item).as_ref(),
                        &service_id,
                        &class_name,
                    );
                }
            }
            tags => {
                parse_yaml_listener_tag(&mut collector, tags.as_ref(), &service_id, &class_name)
            }
        }
    }
    collector.values()
}

fn parse_yaml_listener_tag(
    collector: &mut RecordCollector,
    tag: Option<&SyntaxNode>,
    service: &str,
    class_name: &str,
) {
    let Some(tag) = tag else {
        return;
    };
    if tag.kind() == YAML_SCALAR {
        if yamlquery::scalar_value(tag) != "kernel.event_listener" {
            return;
        }
        let range = tag.range_trimmed_trivia();
        collector.add(
            &EventSpec {
                range,
                ..EventSpec::default()
            },
            Occurrence {
                kind: OccurrenceKind::Listener,
                source: SourceKind::ServiceTag,
                range,
                class: class_name.to_owned(),
                method: "__invoke".to_owned(),
                service: service.to_owned(),
                ..Occurrence::default()
            },
        );
        return;
    }
    if !yamlquery::is_mapping(tag)
        || yaml_scalar(yamlquery::property(tag, "name").as_ref()) != "kernel.event_listener"
    {
        return;
    }
    let event_node = yamlquery::property(tag, "event");
    let method_node = yamlquery::property(tag, "method");
    let mut method_name = yaml_scalar(method_node.as_ref());
    if method_name.is_empty() {
        method_name = "__invoke".to_owned();
    }
    let event_name = yaml_scalar(event_node.as_ref());
    let event_type = if looks_like_class_name(&event_name) {
        normalize_name(&event_name).to_owned()
    } else {
        String::new()
    };
    let mut range = node_range(event_node.as_ref());
    if range.is_empty() {
        range = tag.range_trimmed_trivia();
    }
    collector.add(
        &EventSpec {
            name: event_name,
            event_type: event_type.clone(),
            range,
            ..EventSpec::default()
        },
        Occurrence {
            kind: OccurrenceKind::Listener,
            source: SourceKind::ServiceTag,
            range,
            method_range: node_range(method_node.as_ref()),
            class: class_name.to_owned(),
            method: method_name,
            service: service.to_owned(),
            priority: yaml_scalar(yamlquery::property(tag, "priority").as_ref()),
            event_type,
            ..Occurrence::default()
        },
    );
}

pub(crate) fn parse_xml(file: &ParsedFile) -> Vec<Record> {
    let Some(tree) = file.syntax_tree() else {
        return Vec::new();
    };
    let root = tree.root();
    let value = |values: &BTreeMap<String, String>, name: &str| {
        values.get(name).cloned().unwrap_or_default()
    };
    let mut collector = RecordCollector::new(file.path());
    for service in xmlquery::elements(&root, &["service"]) {
        let attributes = xmlquery::attribute_values(&service);
        let service_id = value(&attributes, "id");
        let mut class_name = normalize_name(&value(&attributes, "class")).to_owned();
        if class_name.is_empty() && service_id.contains('\\') {
            class_name = normalize_name(&service_id).to_owned();
        }
        for tag in xmlquery::child_elements(&service, &["tag"]) {
            let tag_attributes = xmlquery::attribute_values(&tag);
            if value(&tag_attributes, "name") != "kernel.event_listener" {
                continue;
            }
            let event_attribute = xmlquery::attribute(&tag, "event");
            let method_attribute = xmlquery::attribute(&tag, "method");
            let mut method_name = value(&tag_attributes, "method");
            if method_name.is_empty() {
                method_name = "__invoke".to_owned();
            }
            let event_name = value(&tag_attributes, "event");
            let event_type = if looks_like_class_name(&event_name) {
                normalize_name(&event_name).to_owned()
            } else {
                String::new()
            };
            let mut range = node_range(event_attribute.as_ref());
            if range.is_empty() {
                range = tag.range_trimmed_trivia();
            }
            collector.add(
                &EventSpec {
                    name: event_name,
                    event_type: event_type.clone(),
                    range,
                    ..EventSpec::default()
                },
                Occurrence {
                    kind: OccurrenceKind::Listener,
                    source: SourceKind::ServiceTag,
                    range,
                    method_range: node_range(method_attribute.as_ref()),
                    class: class_name.clone(),
                    method: method_name,
                    service: service_id.clone(),
                    priority: value(&tag_attributes, "priority"),
                    event_type,
                    ..Occurrence::default()
                },
            );
        }
    }
    collector.values()
}
