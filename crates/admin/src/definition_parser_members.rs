//! Source-aware component member declarations.

use crate::*;

/// A value which `member_definitions` turns into a component member (a plain
/// name or a [`VueComponentProp`]).
pub(crate) trait MemberDefinitionValue {
    /// Name, type and deprecation of the value.
    fn member_parts(&self) -> (&str, &str, &str);
}

impl MemberDefinitionValue for String {
    fn member_parts(&self) -> (&str, &str, &str) {
        (self, "", "")
    }
}

impl MemberDefinitionValue for VueComponentProp {
    fn member_parts(&self) -> (&str, &str, &str) {
        (&self.name, &self.type_, &self.deprecated)
    }
}

pub(crate) fn member_definitions<T: MemberDefinitionValue>(
    property: Option<&SyntaxNode>,
    values: &[T],
    kind: VueComponentMemberKind,
    line_index: &LineIndex,
) -> Vec<VueComponentMember> {
    struct Declaration {
        line: u32,
        name_range: AdminSourceRange,
        binding_name: String,
        shorthand: bool,
        deprecated: String,
    }
    let mut locations: HashMap<String, Declaration> = HashMap::new();
    let default_line = property.map_or(0, |property| definition_node_line(line_index, property));
    if let Some(object) = property.and_then(first_object) {
        for child in jsquery::properties(&object) {
            let name = jsquery::property_name(&child).unwrap_or_default();
            if name.is_empty() {
                continue;
            }
            let value = jsquery::property_value(&child);
            let mut binding_name = name.clone();
            if let Some(value) = value
                .as_ref()
                .filter(|value| value.kind() == jssyntax::JS_IDENTIFIER)
            {
                binding_name = jsquery::identifier_text(value)
                    .unwrap_or_default()
                    .trim()
                    .to_owned();
            }
            locations.insert(
                name,
                Declaration {
                    line: definition_node_line(line_index, &child),
                    name_range: component_member_name_range(
                        jsquery::property_name_node(&child).as_ref(),
                        line_index,
                    ),
                    binding_name,
                    shorthand: value.is_none() && child.kind() == jssyntax::JS_PROPERTY,
                    deprecated: javascript_deprecation(&child),
                },
            );
        }
    }
    let mut members = Vec::with_capacity(values.len());
    for value in values {
        let (name, member_type, deprecated) = value.member_parts();
        if name.is_empty() {
            continue;
        }
        let location = locations.get(name);
        let mut deprecated = deprecated.to_owned();
        if deprecated.is_empty() {
            deprecated = location
                .map(|location| location.deprecated.clone())
                .unwrap_or_default();
        }
        let mut line = location.map_or(0, |location| location.line);
        if line == 0 {
            line = default_line;
        }
        members.push(VueComponentMember {
            name: name.to_owned(),
            kind,
            type_: member_type.to_owned(),
            line,
            name_range: location
                .map(|location| location.name_range.clone())
                .unwrap_or_default(),
            binding_name: location
                .map(|location| location.binding_name.clone())
                .unwrap_or_default(),
            shorthand: location.is_some_and(|location| location.shorthand),
            deprecated,
            ..Default::default()
        });
    }
    members
}

pub(crate) fn component_member_name_range(
    node: Option<&SyntaxNode>,
    line_index: &LineIndex,
) -> AdminSourceRange {
    let Some(node) = node else {
        return AdminSourceRange::default();
    };
    let mut range = node.range_trimmed_trivia();
    let identifier = node.kind() != jssyntax::JS_STRING;
    if !identifier && let Some(inner) = js_string_content_range(node) {
        range = inner;
    }
    let (start_line, start_character) = line_index.position_utf16(u32::from(range.start()));
    let (end_line, end_character) = line_index.position_utf16(u32::from(range.end()));
    AdminSourceRange {
        start_line,
        start_character,
        end_line,
        end_character,
        declaration: true,
        identifier,
        ..Default::default()
    }
}

pub(crate) fn first_object(node: &SyntaxNode) -> Option<SyntaxNode> {
    if let Some(value) =
        jsquery::property_value(node).filter(|value| value.kind() == jssyntax::JS_OBJECT)
    {
        return Some(value);
    }
    node.descendants()
        .find(|node| node.kind() == jssyntax::JS_OBJECT)
}

pub(crate) fn parse_data_names(property: &SyntaxNode) -> Vec<String> {
    first_object(property)
        .map(|object| parse_method_names(Some(&object)))
        .unwrap_or_default()
}

pub(crate) fn parse_inject_names(node: Option<&SyntaxNode>) -> Vec<String> {
    match node {
        Some(node) if node.kind() == jssyntax::JS_ARRAY => parse_string_array(Some(node)),
        Some(node) if node.kind() == jssyntax::JS_OBJECT => parse_method_names(Some(node)),
        _ => Vec::new(),
    }
}

pub(crate) fn parse_mixin_names(node: Option<&SyntaxNode>) -> Vec<String> {
    let Some(node) = node else {
        return Vec::new();
    };
    jsquery::iter_calls(node, &["Mixin.getByName", "Shopware.Mixin.getByName"])
        .filter_map(|call| jsquery::string_argument(&call, 0))
        .map(|argument| jsquery::string_value(&argument))
        .filter(|name| !name.is_empty())
        .collect()
}

pub(crate) fn set_definition_file_path(definition: &mut ComponentDefinition, file_path: &str) {
    definition.file_path = file_path.to_owned();
    for prop in &mut definition.props {
        prop.file_path = file_path.to_owned();
    }
    for member in &mut definition.members {
        member.file_path = file_path.to_owned();
        for element in &mut member.element_members {
            element.file_path = file_path.to_owned();
        }
        if !member.type_.is_empty() && member.type_context_path.is_empty() {
            member.type_context_path = file_path.to_owned();
        }
    }
    for assignment in &mut definition.assignments {
        assignment.file_path = file_path.to_owned();
    }
    for event in &mut definition.events {
        event.file_path = file_path.to_owned();
    }
    for local in &mut definition.local_components {
        local.file_path = file_path.to_owned();
    }
    for local in &mut definition.local_directives {
        local.file_path = file_path.to_owned();
    }
}
