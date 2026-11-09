//! Static DoctrineBundle custom DBAL type registrations in PHP, YAML and XML
//! configuration.

use std::collections::HashSet;

use serde::{Deserialize, Serialize};
use swlsp_parser_php::query as phpquery;
use swlsp_parser_php::syntax::{
    PHP_ARRAY, PHP_FUNCTION_CALL, PHP_MEMBER_CALL, PHP_NAME, PHP_OBJECT_CREATION, PHP_SCOPED_CALL,
};
use swlsp_parser_xml::query as xmlquery;
use swlsp_parser_xml::syntax::XML_TEXT;
use swlsp_parser_yaml::query as yamlquery;
use swlsp_parser_yaml::syntax::{YAML_FLOW_MAPPING, YAML_MAPPING};
use swlsp_php::NameResolver;
use swlsp_syntax::{SyntaxNode, SyntaxNodeExt, TextRange, TextSize};
use swlsp_textutil::{eq_ignore_case, lowercase};

use crate::helpers::{
    has_range, normalize_class, range, range_contains_cursor, xml_value_range, yaml_scalar_range,
};

/// A static DoctrineBundle custom DBAL type registration. `name_range` and
/// `class_range` retain both sides of the configuration mapping.
#[derive(Clone, Debug, Default, PartialEq, Eq, Serialize, Deserialize)]
#[serde(default)]
pub struct TypeRegistration {
    pub name: String,
    pub class: String,
    pub file: String,
    pub name_range: TextRange,
    pub class_range: TextRange,
}

#[derive(Clone, Copy, Debug, Default, PartialEq, Eq)]
pub enum TypeRegistrationReferenceRole {
    #[default]
    Name,
    Class,
}

#[derive(Clone, Debug, Default, PartialEq, Eq)]
pub struct TypeRegistrationReference {
    pub role: TypeRegistrationReferenceRole,
    pub name: String,
    pub class: String,
    pub range: TextRange,
    pub class_constant: bool,
    pub object_creation: bool,
    pub object_creation_started: bool,
}

fn lower_extension(path: &str) -> String {
    let base = path.rsplit('/').next().unwrap_or(path);
    match base.rfind('.') {
        Some(dot) => lowercase(&base[dot..]),
        None => String::new(),
    }
}

fn text(node: &SyntaxNode) -> String {
    node.text().to_string()
}

fn empty_at(offset: TextSize) -> TextRange {
    TextRange::empty(offset)
}

/// Every static registration in the document, in source order.
pub fn type_registrations_in_document(path: &str, root: &SyntaxNode) -> Vec<TypeRegistration> {
    match lower_extension(path).as_str() {
        ".php" => php_type_registrations(path, root),
        ".yaml" | ".yml" => yaml_type_registrations(path, root),
        ".xml" => xml_type_registrations(path, root),
        _ => Vec::new(),
    }
}

/// The registration name or class under the cursor, including incomplete
/// values while typing.
pub fn type_registration_reference_at(
    path: &str,
    root: &SyntaxNode,
    offset: TextSize,
) -> Option<TypeRegistrationReference> {
    let extension = lower_extension(path);
    // PHP registrations and every PHP match lie inside a call node, so only
    // the calls whose range touches the cursor are inspected.
    let php_calls = (extension == ".php").then(|| php_calls_at(root, offset));
    if php_calls.as_ref().is_some_and(Vec::is_empty) {
        return None;
    }
    let php_resolver = php_calls.as_ref().map(|_| NameResolver::new(root));
    let registrations = match (&php_calls, &php_resolver) {
        (Some(calls), Some(resolver)) => php_type_registrations_in_calls(path, calls, resolver),
        _ => type_registrations_in_document(path, root),
    };
    for registration in registrations {
        if range_contains_cursor(registration.name_range, offset) {
            return Some(TypeRegistrationReference {
                role: TypeRegistrationReferenceRole::Name,
                range: registration.name_range,
                name: registration.name,
                class: registration.class,
                ..TypeRegistrationReference::default()
            });
        }
        if range_contains_cursor(registration.class_range, offset) {
            let mut class_constant = false;
            let mut object_creation = false;
            if extension == ".php" {
                let node = root.node_at(offset);
                class_constant = node
                    .as_ref()
                    .is_some_and(|node| !phpquery::class_constant_name(node).is_empty());
                object_creation = node
                    .as_ref()
                    .is_some_and(|node| php_type_object_creation_at(node).is_some());
            }
            return Some(TypeRegistrationReference {
                role: TypeRegistrationReferenceRole::Class,
                range: registration.class_range,
                name: registration.name,
                class: registration.class,
                class_constant,
                object_creation,
                object_creation_started: false,
            });
        }
    }
    if let (Some(calls), Some(resolver)) = (&php_calls, &php_resolver) {
        return php_type_registration_reference_at(calls, resolver, offset);
    }
    match extension.as_str() {
        ".yaml" | ".yml" => yaml_type_registration_reference_at(root, offset),
        ".xml" => xml_type_registration_reference_at(root, offset),
        _ => None,
    }
}

fn registration_key(name: &str, class: &str, name_range: TextRange) -> String {
    format!(
        "{}|{}|{}:{}",
        lowercase(name),
        lowercase(class),
        u32::from(name_range.start()),
        u32::from(name_range.end())
    )
}

fn string_content_range(node: Option<&SyntaxNode>) -> TextRange {
    node.map(phpquery::string_content_range).unwrap_or_default()
}

fn is_php_call(node: &SyntaxNode) -> bool {
    matches!(
        node.kind(),
        PHP_MEMBER_CALL | PHP_SCOPED_CALL | PHP_FUNCTION_CALL
    )
}

/// The calls (`phpquery::calls` kinds, document order) that can yield a
/// registration or match at `offset`. Everything a call derives lies inside
/// the call or inside an array enclosing it (`array_at` climbs ancestors), so
/// only calls containing `offset` and calls inside the outermost array
/// containing `offset` can match.
fn php_calls_at(root: &SyntaxNode, offset: TextSize) -> Vec<SyntaxNode> {
    let root_range = root.text_range();
    if offset < root_range.start() || offset > root_range.end() {
        return Vec::new();
    }
    let mut candidates: Vec<SyntaxNode> = Vec::new();
    let mut outermost_arrays: Vec<SyntaxNode> = Vec::new();
    for token in root.token_at_offset(offset) {
        let mut outermost = None;
        for node in token.parent_ancestors() {
            if is_php_call(&node) {
                candidates.push(node.clone());
            }
            if node.kind() == PHP_ARRAY {
                outermost = Some(node);
            }
        }
        if let Some(array) = outermost
            && !outermost_arrays.contains(&array)
        {
            outermost_arrays.push(array);
        }
    }
    for array in &outermost_arrays {
        candidates.extend(array.descendants().filter(is_php_call));
    }
    // Preorder: earlier start first, then the enclosing (longer) node, then
    // the ancestor of equal-range nodes.
    let mut keyed: Vec<(TextSize, std::cmp::Reverse<TextSize>, usize, SyntaxNode)> = candidates
        .into_iter()
        .map(|node| {
            let range = node.text_range();
            let depth = node.ancestors().count();
            (range.start(), std::cmp::Reverse(range.end()), depth, node)
        })
        .collect();
    keyed.sort_by_key(|left| (left.0, left.1, left.2));
    keyed.dedup_by(|left, right| left.3 == right.3);
    keyed.into_iter().map(|(_, _, _, node)| node).collect()
}

fn php_type_registrations(path: &str, root: &SyntaxNode) -> Vec<TypeRegistration> {
    let resolver = NameResolver::new(root);
    php_type_registrations_in_calls(path, &phpquery::calls(root, &[]), &resolver)
}

fn php_type_registrations_in_calls(
    path: &str,
    calls: &[SyntaxNode],
    resolver: &NameResolver,
) -> Vec<TypeRegistration> {
    let mut result = Vec::new();
    let mut seen = HashSet::new();
    for types in php_doctrine_type_arrays(calls) {
        for item in phpquery::array_items(&types) {
            let name_node = phpquery::array_item_key(&item);
            let Some(name) = php_type_config_string(name_node.as_ref()) else {
                continue;
            };
            if name.is_empty() {
                continue;
            }
            let (class, class_range, _) =
                php_type_registration_class(phpquery::array_item_value(&item).as_ref(), resolver);
            if class.is_empty() {
                continue;
            }
            let name_range = string_content_range(name_node.as_ref());
            if !seen.insert(registration_key(&name, &class, name_range)) {
                continue;
            }
            result.push(TypeRegistration {
                name,
                class,
                file: path.to_owned(),
                name_range,
                class_range,
            });
        }
    }
    for call in php_runtime_type_registration_calls(calls, resolver) {
        let name_node = phpquery::argument_expression(&call.node, 0);
        let Some(name) = php_type_config_string(name_node.as_ref()) else {
            continue;
        };
        if name.is_empty() {
            continue;
        }
        let (class, class_range, _, _) = php_runtime_type_registration_class(
            phpquery::argument_expression(&call.node, 1).as_ref(),
            resolver,
            call.object_creation,
        );
        if class.is_empty() {
            continue;
        }
        let name_range = string_content_range(name_node.as_ref());
        if !seen.insert(registration_key(&name, &class, name_range)) {
            continue;
        }
        result.push(TypeRegistration {
            name,
            class,
            file: path.to_owned(),
            name_range,
            class_range,
        });
    }
    result
}

struct RuntimeCall {
    node: SyntaxNode,
    object_creation: bool,
}

fn php_runtime_type_registration_calls(
    calls: &[SyntaxNode],
    resolver: &NameResolver,
) -> Vec<RuntimeCall> {
    let mut result = Vec::new();
    for call in calls.iter().cloned() {
        match lowercase(&phpquery::call_method_name(&call)).as_str() {
            "addtype" | "overridetype" => {
                if php_dbal_type_static_call(&call, resolver) {
                    result.push(RuntimeCall {
                        node: call,
                        object_creation: false,
                    });
                }
            }
            "register" => {
                if let Some(receiver) = phpquery::call_receiver(&call)
                    && eq_ignore_case(&phpquery::call_method_name(&receiver), "getTypeRegistry")
                    && php_dbal_type_static_call(&receiver, resolver)
                {
                    result.push(RuntimeCall {
                        node: call,
                        object_creation: true,
                    });
                }
            }
            _ => {}
        }
    }
    result
}

fn php_dbal_type_static_call(call: &SyntaxNode, resolver: &NameResolver) -> bool {
    let Some(receiver) = phpquery::call_receiver(call) else {
        return false;
    };
    let name = phpquery::name_value(&receiver);
    !name.is_empty()
        && eq_ignore_case(
            &normalize_class(&resolver.resolve(&name)),
            "Doctrine\\DBAL\\Types\\Type",
        )
}

fn php_doctrine_type_arrays(calls: &[SyntaxNode]) -> Vec<SyntaxNode> {
    let mut result = Vec::new();
    let mut seen = HashSet::new();
    for call in calls {
        if !eq_ignore_case(&phpquery::call_method_name(call), "extension") {
            continue;
        }
        let extension = php_type_config_string(phpquery::argument_expression(call, 0).as_ref());
        if !extension.is_some_and(|extension| eq_ignore_case(&extension, "doctrine")) {
            continue;
        }
        let config =
            phpquery::argument_expression(call, 1).and_then(|node| phpquery::array_at(&node));
        let dbal = php_type_config_array_property(config.as_ref(), "dbal");
        let dbal_array = dbal.and_then(|node| phpquery::array_at(&node));
        let types = php_type_config_array_property(dbal_array.as_ref(), "types");
        let Some(array) = types.and_then(|node| phpquery::array_at(&node)) else {
            continue;
        };
        if !seen.insert(array.text_range()) {
            continue;
        }
        result.push(array);
    }
    result
}

fn php_type_config_array_property(array: Option<&SyntaxNode>, name: &str) -> Option<SyntaxNode> {
    let array = array?;
    for item in phpquery::array_items(array) {
        let key = php_type_config_string(phpquery::array_item_key(&item).as_ref());
        if key.is_some_and(|key| eq_ignore_case(&key, name)) {
            return phpquery::array_item_value(&item);
        }
    }
    None
}

/// The value of a plain single- or double-quoted string literal that spans
/// exactly `node`; interpolated strings are rejected.
fn php_type_config_string(node: Option<&SyntaxNode>) -> Option<String> {
    let node = node?;
    let literal = phpquery::string_at(node)?;
    if literal.range_trimmed_trivia() != node.range_trimmed_trivia() {
        return None;
    }
    let raw = text(&literal);
    let trimmed = raw.trim().as_bytes();
    if trimmed.len() < 2 {
        return None;
    }
    let quote = trimmed[0];
    if (quote != b'\'' && quote != b'"') || trimmed[trimmed.len() - 1] != quote {
        return None;
    }
    let mut value = phpquery::string_value(&literal);
    if quote == b'"' && value.contains('$') {
        return None;
    }
    value = value.replace("\\\\", "\\");
    value = if quote == b'\'' {
        value.replace("\\'", "'")
    } else {
        value.replace("\\\"", "\"")
    };
    Some(value)
}

/// The class of a registration value: `Foo::class`, a class string, or an
/// expanded `['class' => ...]` option. The flag reports `::class` syntax.
fn php_type_registration_class(
    value: Option<&SyntaxNode>,
    resolver: &NameResolver,
) -> (String, TextRange, bool) {
    let Some(value) = value else {
        return (String::new(), TextRange::default(), false);
    };
    if let Some(array) = phpquery::array_at(value)
        && array.range_trimmed_trivia() == value.range_trimmed_trivia()
    {
        return php_type_registration_class(
            php_type_config_array_property(Some(&array), "class").as_ref(),
            resolver,
        );
    }
    let name = phpquery::class_constant_name(value);
    if !name.is_empty() {
        let range = phpquery::direct_child(value, PHP_NAME).map_or_else(
            || value.range_trimmed_trivia(),
            |node| node.range_trimmed_trivia(),
        );
        return (normalize_class(&resolver.resolve(&name)), range, true);
    }
    if let Some(class) = php_type_config_string(Some(value)) {
        return (
            normalize_class(&class),
            phpquery::string_content_range(value),
            false,
        );
    }
    (String::new(), value.range_trimmed_trivia(), false)
}

fn php_type_registration_reference_at(
    calls: &[SyntaxNode],
    resolver: &NameResolver,
    offset: TextSize,
) -> Option<TypeRegistrationReference> {
    for types in php_doctrine_type_arrays(calls) {
        let types_range = types.text_range();
        if offset < types_range.start() || offset > types_range.end() {
            continue;
        }
        let items = phpquery::array_items(&types);
        for (index, item) in items.iter().enumerate() {
            let item_range = item.text_range();
            let item_end = items
                .get(index + 1)
                .map_or(types_range.end(), |next| next.text_range().start());
            if offset < item_range.start() || offset > item_end {
                continue;
            }
            let name_node = phpquery::array_item_key(item);
            let name = php_type_config_string(name_node.as_ref()).unwrap_or_default();
            let name_range = string_content_range(name_node.as_ref());
            let mut value = phpquery::array_item_value(item);
            if let (Some(current), Some(key)) = (&value, &name_node)
                && current.text_range() == key.text_range()
                && php_array_item_has_arrow(item)
            {
                value = None;
            }
            let (class, mut class_range, mut class_constant) =
                php_type_registration_class(value.as_ref(), resolver);
            if range_contains_cursor(name_range, offset) {
                return Some(TypeRegistrationReference {
                    role: TypeRegistrationReferenceRole::Name,
                    name,
                    class,
                    range: name_range,
                    class_constant,
                    ..TypeRegistrationReference::default()
                });
            }
            if let Some(current) = &value
                && let Some(array) = phpquery::array_at(current)
                && array.range_trimmed_trivia() == current.range_trimmed_trivia()
            {
                if let Some(reference) =
                    php_type_class_option_reference_at(&array, &name, resolver, offset)
                {
                    return Some(reference);
                }
                continue;
            }
            if range_contains_cursor(class_range, offset)
                || (value.is_none() && php_cursor_after_array_arrow(item, offset))
            {
                if value.is_none() {
                    class_range = empty_at(offset);
                    class_constant = true;
                }
                return Some(TypeRegistrationReference {
                    role: TypeRegistrationReferenceRole::Class,
                    name,
                    class,
                    range: class_range,
                    class_constant,
                    ..TypeRegistrationReference::default()
                });
            }
        }
    }
    php_runtime_type_registration_reference_at(calls, resolver, offset)
}

fn php_runtime_type_registration_reference_at(
    calls: &[SyntaxNode],
    resolver: &NameResolver,
    offset: TextSize,
) -> Option<TypeRegistrationReference> {
    for runtime_call in php_runtime_type_registration_calls(calls, resolver) {
        let call = &runtime_call.node;
        let name_node = phpquery::argument_expression(call, 0);
        let name = php_type_config_string(name_node.as_ref()).unwrap_or_default();
        let name_range = string_content_range(name_node.as_ref());
        let value = phpquery::argument_expression(call, 1);
        let (class, mut class_range, mut class_constant, mut object_creation) =
            php_runtime_type_registration_class(
                value.as_ref(),
                resolver,
                runtime_call.object_creation,
            );
        if range_contains_cursor(name_range, offset) {
            return Some(TypeRegistrationReference {
                role: TypeRegistrationReferenceRole::Name,
                name,
                class,
                range: name_range,
                class_constant,
                object_creation,
                object_creation_started: false,
            });
        }
        let value_range = value
            .as_ref()
            .map(SyntaxNodeExt::range_trimmed_trivia)
            .unwrap_or_default();
        let missing_value =
            value.is_none() && php_cursor_after_call_argument_separator(call, offset);
        let incomplete_value = class.is_empty()
            && value
                .as_ref()
                .is_some_and(|value| php_cursor_in_incomplete_call_value(call, value, offset));
        if range_contains_cursor(class_range, offset)
            || range_contains_cursor(value_range, offset)
            || missing_value
            || incomplete_value
        {
            if !has_range(class_range) {
                class_range = empty_at(offset);
            }
            if missing_value {
                class_constant = !runtime_call.object_creation;
                object_creation = runtime_call.object_creation;
            }
            if incomplete_value && runtime_call.object_creation {
                object_creation = true;
            }
            return Some(TypeRegistrationReference {
                role: TypeRegistrationReferenceRole::Class,
                name,
                class,
                range: class_range,
                class_constant,
                object_creation,
                object_creation_started: object_creation && value.is_some(),
            });
        }
    }
    None
}

/// The offset of the last `)` in the call, or the call end.
fn call_closing(call: &SyntaxNode) -> TextSize {
    let call_range = call.text_range();
    match text(call).rfind(')') {
        Some(closing) => call_range.start() + TextSize::from(closing as u32),
        None => call_range.end(),
    }
}

fn php_cursor_in_incomplete_call_value(
    call: &SyntaxNode,
    value: &SyntaxNode,
    offset: TextSize,
) -> bool {
    if offset < value.text_range().start() {
        return false;
    }
    offset <= call_closing(call)
}

fn php_runtime_type_registration_class(
    value: Option<&SyntaxNode>,
    resolver: &NameResolver,
    object_expected: bool,
) -> (String, TextRange, bool, bool) {
    if object_expected {
        let Some(object) = value.filter(|value| value.kind() == PHP_OBJECT_CREATION) else {
            let range = value
                .map(SyntaxNodeExt::range_trimmed_trivia)
                .unwrap_or_default();
            return (String::new(), range, false, true);
        };
        let name = phpquery::object_class_name(object);
        let range = phpquery::direct_child(object, PHP_NAME).map_or_else(
            || object.range_trimmed_trivia(),
            |node| node.range_trimmed_trivia(),
        );
        return (
            normalize_class(&resolver.resolve(&name)),
            range,
            false,
            true,
        );
    }
    let (class, range, class_constant) = php_type_registration_class(value, resolver);
    (class, range, class_constant, false)
}

fn php_type_object_creation_at(node: &SyntaxNode) -> Option<SyntaxNode> {
    node.ancestors()
        .find(|current| current.kind() == PHP_OBJECT_CREATION)
}

fn php_cursor_after_call_argument_separator(call: &SyntaxNode, offset: TextSize) -> bool {
    let Some(first) = phpquery::argument(call, 0) else {
        return false;
    };
    let call_range = call.text_range();
    let first_range = first.text_range();
    if first_range.end() < call_range.start() || first_range.end() > call_range.end() {
        return false;
    }
    let call_text = text(call);
    let from = u32::from(first_range.end() - call_range.start()) as usize;
    let Some(rest) = call_text.get(from..) else {
        return false;
    };
    let Some(separator) = rest.find(',') else {
        return false;
    };
    let start = first_range.end() + TextSize::from(separator as u32 + 1);
    offset >= start && offset <= call_closing(call)
}

fn php_type_class_option_reference_at(
    array: &SyntaxNode,
    name: &str,
    resolver: &NameResolver,
    offset: TextSize,
) -> Option<TypeRegistrationReference> {
    let items = phpquery::array_items(array);
    for (index, option) in items.iter().enumerate() {
        let key_node = phpquery::array_item_key(option);
        let key = php_type_config_string(key_node.as_ref());
        if !key.is_some_and(|key| eq_ignore_case(&key, "class")) {
            continue;
        }
        let option_range = option.text_range();
        let option_end = items
            .get(index + 1)
            .map_or(array.text_range().end(), |next| next.text_range().start());
        if offset < option_range.start() || offset > option_end {
            continue;
        }
        let mut value = phpquery::array_item_value(option);
        if let (Some(current), Some(key)) = (&value, &key_node)
            && current.text_range() == key.text_range()
            && php_array_item_has_arrow(option)
        {
            value = None;
        }
        let (class, mut range, mut class_constant) =
            php_type_registration_class(value.as_ref(), resolver);
        if value.is_none() && php_cursor_after_array_arrow(option, offset) {
            range = empty_at(offset);
            class_constant = true;
        } else if !range_contains_cursor(range, offset) {
            return None;
        }
        return Some(TypeRegistrationReference {
            role: TypeRegistrationReferenceRole::Class,
            name: name.to_owned(),
            class,
            range,
            class_constant,
            ..TypeRegistrationReference::default()
        });
    }
    None
}

fn php_array_item_has_arrow(item: &SyntaxNode) -> bool {
    text(item).contains("=>")
}

fn php_cursor_after_array_arrow(item: &SyntaxNode, offset: TextSize) -> bool {
    let Some(index) = text(item).find("=>") else {
        return false;
    };
    offset >= item.text_range().start() + TextSize::from(index as u32 + 2)
}

fn yaml_type_registrations(path: &str, root: &SyntaxNode) -> Vec<TypeRegistration> {
    let mut result = Vec::new();
    let mut seen = HashSet::new();
    for mapping in yamlquery::nodes(root, &[YAML_MAPPING, YAML_FLOW_MAPPING]) {
        let types = yamlquery::property(&mapping, "doctrine")
            .and_then(|doctrine| yamlquery::property(&doctrine, "dbal"))
            .and_then(|dbal| yamlquery::property(&dbal, "types"));
        let Some(types) = types.filter(yamlquery::is_mapping) else {
            continue;
        };
        for pair in yamlquery::pairs(&types) {
            let name_node = yamlquery::pair_key(&pair);
            let value = yamlquery::pair_value(&pair);
            let class_node = match &value {
                Some(value) if yamlquery::is_mapping(value) => yamlquery::property(value, "class"),
                _ => value,
            };
            let name = scalar(name_node.as_ref()).trim().to_owned();
            let class = normalize_class(&scalar(class_node.as_ref()));
            if name.is_empty() || class.is_empty() {
                continue;
            }
            let name_range = yaml_scalar_range(name_node.as_ref());
            if !seen.insert(registration_key(&name, &class, name_range)) {
                continue;
            }
            result.push(TypeRegistration {
                name,
                class,
                file: path.to_owned(),
                name_range,
                class_range: yaml_scalar_range(class_node.as_ref()),
            });
        }
    }
    result
}

fn scalar(node: Option<&SyntaxNode>) -> String {
    node.map(yamlquery::scalar_value).unwrap_or_default()
}

fn yaml_type_registration_reference_at(
    root: &SyntaxNode,
    offset: TextSize,
) -> Option<TypeRegistrationReference> {
    let node = root.node_at(offset)?;
    let pair = yamlquery::ancestor_pair(&node)?;
    let path = yamlquery::pair_path(&pair);
    let types_at = type_registration_path_index(&path)?;
    if types_at + 1 >= path.len() {
        return None;
    }
    let name = path[types_at + 1].clone();
    let mut role = TypeRegistrationReferenceRole::Class;
    if path.len() > types_at + 2 && !eq_ignore_case(&path[types_at + 2], "class") {
        return None;
    }
    let key = yamlquery::pair_key(&pair);
    let value = yamlquery::pair_value(&pair);
    if path.len() == types_at + 2 && range_contains_cursor(yaml_scalar_range(key.as_ref()), offset)
    {
        role = TypeRegistrationReferenceRole::Name;
    }
    let class_node = if path.len() == types_at + 3 {
        value
    } else {
        match &value {
            Some(value) if yamlquery::is_mapping(value) => yamlquery::property(value, "class"),
            _ => value,
        }
    };
    let class = normalize_class(&scalar(class_node.as_ref()));
    let mut range = yaml_scalar_range(class_node.as_ref());
    if role == TypeRegistrationReferenceRole::Name {
        range = yaml_scalar_range(key.as_ref());
    }
    if !has_range(range) {
        range = empty_at(offset);
    }
    Some(TypeRegistrationReference {
        role,
        name,
        class,
        range,
        ..TypeRegistrationReference::default()
    })
}

fn type_registration_path_index(path: &[String]) -> Option<usize> {
    (0..path.len().saturating_sub(2)).find_map(|index| {
        (eq_ignore_case(&path[index], "doctrine")
            && eq_ignore_case(&path[index + 1], "dbal")
            && eq_ignore_case(&path[index + 2], "types"))
        .then_some(index + 2)
    })
}

fn attribute_value(node: Option<&SyntaxNode>) -> String {
    node.and_then(xmlquery::attribute_value).unwrap_or_default()
}

fn is_dbal_type_element(element: &SyntaxNode) -> bool {
    eq_ignore_case(&xml_local_name(element), "type")
        && xml_has_ancestor(element, "dbal")
        && xml_has_ancestor(element, "config")
}

fn xml_type_registrations(path: &str, root: &SyntaxNode) -> Vec<TypeRegistration> {
    let mut result = Vec::new();
    for element in xmlquery::elements(root, &[]) {
        if !is_dbal_type_element(&element) {
            continue;
        }
        let name_attribute = xmlquery::attribute(&element, "name");
        let class_attribute = xmlquery::attribute(&element, "class");
        let name = attribute_value(name_attribute.as_ref()).trim().to_owned();
        let mut class = normalize_class(&attribute_value(class_attribute.as_ref()));
        let mut class_range = xml_value_range(class_attribute.as_ref());
        if class.is_empty() {
            let (value, value_range) = xml_registration_text(&element);
            class = normalize_class(&value);
            class_range = value_range;
        }
        if name.is_empty() || class.is_empty() {
            continue;
        }
        result.push(TypeRegistration {
            name,
            class,
            file: path.to_owned(),
            name_range: xml_value_range(name_attribute.as_ref()),
            class_range,
        });
    }
    result
}

fn xml_type_registration_reference_at(
    root: &SyntaxNode,
    offset: TextSize,
) -> Option<TypeRegistrationReference> {
    let node = root.node_at(offset)?;
    let element = xmlquery::element_at(&node)?;
    if !is_dbal_type_element(&element) {
        return None;
    }
    let name_attribute = xmlquery::attribute(&element, "name");
    let class_attribute = xmlquery::attribute(&element, "class");
    let name = attribute_value(name_attribute.as_ref()).trim().to_owned();
    let class = normalize_class(&attribute_value(class_attribute.as_ref()));
    if let Some(attribute) = xmlquery::attribute_at(&node) {
        match lowercase(&xmlquery::attribute_name(&attribute).unwrap_or_default()).as_str() {
            "name" => {
                return Some(TypeRegistrationReference {
                    role: TypeRegistrationReferenceRole::Name,
                    name,
                    class,
                    range: xml_value_range(Some(&attribute)),
                    ..TypeRegistrationReference::default()
                });
            }
            "class" => {
                let mut range = xml_value_range(Some(&attribute));
                if !has_range(range) {
                    range = empty_at(offset);
                }
                return Some(TypeRegistrationReference {
                    role: TypeRegistrationReferenceRole::Class,
                    name,
                    class,
                    range,
                    ..TypeRegistrationReference::default()
                });
            }
            _ => {}
        }
    }
    if class_attribute.is_none() {
        let (value, mut range) = xml_registration_text(&element);
        if !has_range(range) {
            range = empty_at(offset);
        }
        return Some(TypeRegistrationReference {
            role: TypeRegistrationReferenceRole::Class,
            name,
            class: normalize_class(&value),
            range,
            ..TypeRegistrationReference::default()
        });
    }
    None
}

fn xml_local_name(element: &SyntaxNode) -> String {
    let name = xmlquery::element_name(element).unwrap_or_default();
    match name.rfind(':') {
        Some(separator) => name[separator + 1..].to_owned(),
        None => name,
    }
}

fn xml_has_ancestor(element: &SyntaxNode, name: &str) -> bool {
    let mut parent = xmlquery::parent_element(element);
    while let Some(current) = parent {
        if eq_ignore_case(&xml_local_name(&current), name) {
            return true;
        }
        parent = xmlquery::parent_element(&current);
    }
    false
}

fn xml_registration_text(element: &SyntaxNode) -> (String, TextRange) {
    for node in xmlquery::nodes(element, &[XML_TEXT]) {
        let raw = text(&node);
        let value = raw.trim();
        if value.is_empty() {
            continue;
        }
        let is_space = |character: char| matches!(character, ' ' | '\t' | '\r' | '\n');
        let leading = raw.len() - raw.trim_start_matches(is_space).len();
        let trailing = raw.len() - raw.trim_end_matches(is_space).len();
        let full = node.text_range();
        let start = u32::from(full.start()) + leading as u32;
        let end = u32::from(full.end()) - trailing as u32;
        return (value.to_owned(), range(start, end));
    }
    (String::new(), TextRange::default())
}
