//! Event declarations and legacy `@event` annotations.

use crate::*;

pub(crate) fn parse_string_array(node: Option<&SyntaxNode>) -> Vec<String> {
    let Some(node) = node.filter(|node| node.kind() == jssyntax::JS_ARRAY) else {
        return Vec::new();
    };
    jsquery::array_items(node)
        .iter()
        .filter(|item| item.kind() == jssyntax::JS_STRING)
        .map(jsquery::string_value)
        .filter(|value| !value.is_empty())
        .collect()
}

/// The 1-based line of a node's first non-trivia byte.
pub(crate) fn definition_node_line(line_index: &LineIndex, node: &SyntaxNode) -> u32 {
    line_index
        .position(u32::from(node.range_trimmed_trivia().start()))
        .0
        + 1
}

pub(crate) fn parse_event_declarations(
    node: &SyntaxNode,
    line_index: &LineIndex,
) -> Vec<VueComponentEvent> {
    let mut events = Vec::new();
    match node.kind() {
        jssyntax::JS_ARRAY => {
            for item in jsquery::array_items(node) {
                if item.kind() != jssyntax::JS_STRING {
                    continue;
                }
                let name = jsquery::string_value(&item);
                if name.is_empty() {
                    continue;
                }
                append_component_event(
                    &mut events,
                    VueComponentEvent {
                        name,
                        line: definition_node_line(line_index, &item),
                        documentation: javascript_documentation(&item),
                        name_range: component_member_name_range(Some(&item), line_index),
                        ..Default::default()
                    },
                );
            }
        }
        jssyntax::JS_OBJECT => {
            for property in jsquery::properties(node) {
                let name = jsquery::property_name(&property).unwrap_or_default();
                if name.is_empty() {
                    continue;
                }
                append_component_event(
                    &mut events,
                    VueComponentEvent {
                        name,
                        line: definition_node_line(line_index, &property),
                        documentation: javascript_documentation(&property),
                        name_range: component_member_name_range(
                            jsquery::property_name_node(&property).as_ref(),
                            line_index,
                        ),
                        ..Default::default()
                    },
                );
            }
        }
        _ => {}
    }
    events
}

pub(crate) fn parse_emitted_events(
    object: &SyntaxNode,
    line_index: &LineIndex,
) -> Vec<VueComponentEvent> {
    let mut events = Vec::new();
    for call in jsquery::iter_calls(object, &[]) {
        if !matches!(
            jsquery::call_name(&call).as_deref(),
            Some("this.$emit" | "$emit" | "emit" | "context.emit")
        ) {
            continue;
        }
        let Some(argument) = jsquery::argument_expression(&call, 0) else {
            continue;
        };
        if argument.kind() != jssyntax::JS_STRING {
            continue;
        }
        let name = jsquery::string_value(&argument);
        if name.is_empty() {
            continue;
        }
        append_component_event(
            &mut events,
            VueComponentEvent {
                name,
                line: definition_node_line(line_index, &argument),
                name_range: component_member_name_range(Some(&argument), line_index),
                ..Default::default()
            },
        );
    }
    events
}

/// Adds `event` or fills missing metadata of an existing event with the same
/// canonical name.
pub(crate) fn append_component_event(
    events: &mut Vec<VueComponentEvent>,
    event: VueComponentEvent,
) {
    let canonical = canonical_event_name(&event.name);
    if canonical.is_empty() {
        return;
    }
    if let Some(existing) = events
        .iter_mut()
        .find(|existing| canonical_event_name(&existing.name) == canonical)
    {
        if existing.file_path.is_empty() && !event.file_path.is_empty() {
            existing.file_path = event.file_path;
        }
        if existing.line == 0 && event.line != 0 {
            existing.line = event.line;
        }
        if existing.type_.is_empty() && !event.type_.is_empty() {
            existing.type_ = event.type_;
        }
        if existing.documentation.is_empty() && !event.documentation.is_empty() {
            existing.documentation = event.documentation;
        }
        let empty = AdminSourceRange::default();
        if existing.name_range == empty && event.name_range != empty {
            existing.name_range = event.name_range;
        }
        return;
    }
    events.push(event);
}

/// The leading trivia tokens of `node` (comments and whitespace before its
/// first syntax token).
pub(crate) fn leading_trivia_tokens(node: &SyntaxNode) -> impl Iterator<Item = SyntaxToken> {
    node.descendants_with_tokens()
        .filter_map(|element| element.into_token())
        .take_while(|token| token.kind().is_trivia())
}

/// Extracts the legacy Shopware `@event` contract attached to an export.
pub fn javascript_event_annotations(
    node: &SyntaxNode,
    line_index: &LineIndex,
) -> Vec<VueComponentEvent> {
    let mut result = Vec::new();
    for token in leading_trivia_tokens(node) {
        if token.kind() != jssyntax::TK_BLOCK_COMMENT || !token.text().trim().starts_with("/**") {
            continue;
        }
        for event in parse_javascript_event_annotations(
            token.text(),
            u32::from(token.text_range().start()),
            line_index,
        ) {
            append_component_event(&mut result, event);
        }
    }
    result
}

fn trim_left_blank(value: &str) -> &str {
    value.trim_start_matches([' ', '\t'])
}

pub(crate) fn parse_javascript_event_annotations(
    comment: &str,
    comment_start: u32,
    line_index: &LineIndex,
) -> Vec<VueComponentEvent> {
    let mut result = Vec::new();
    let mut line_start = 0usize;
    while line_start <= comment.len() {
        let line_end = comment[line_start..]
            .find('\n')
            .map_or(comment.len(), |end| end + line_start);
        let line = &comment[line_start..line_end];
        let bytes = line.as_bytes();
        let mut marker = line.len() - trim_left_blank(line).len();
        if marker < line.len() && bytes[marker] == b'*' {
            marker += 1;
            marker += line[marker..].len() - trim_left_blank(&line[marker..]).len();
        }
        if marker < line.len() && line[marker..].starts_with("@event") {
            let tail_start = marker + "@event".len();
            if tail_start == line.len() || bytes[tail_start] == b' ' || bytes[tail_start] == b'\t' {
                let tail = &line[tail_start..];
                let trimmed = trim_left_blank(tail);
                let leading = tail.len() - trimmed.len();
                let name_end = trimmed.find([' ', '\t', '\r']).unwrap_or(trimmed.len());
                let name = trimmed[..name_end].trim();
                if !name.is_empty() {
                    let absolute_start = comment_start + (line_start + tail_start + leading) as u32;
                    let (event_type, documentation) =
                        parse_javascript_event_annotation_contract(trimmed[name_end..].trim());
                    let line_number = line_index.position(absolute_start).0 + 1;
                    append_component_event(
                        &mut result,
                        VueComponentEvent {
                            name: name.to_owned(),
                            type_: event_type,
                            documentation,
                            line: line_number,
                            name_range: source_range_at(
                                line_index,
                                absolute_start,
                                absolute_start + name.len() as u32,
                                false,
                            ),
                            ..Default::default()
                        },
                    );
                }
            }
        }
        if line_end == comment.len() {
            break;
        }
        line_start = line_end + 1;
    }
    result
}

pub(crate) fn parse_javascript_event_annotation_contract(value: &str) -> (String, String) {
    let value = value.trim();
    if value.is_empty() {
        return (String::new(), String::new());
    }
    let bytes = value.as_bytes();
    if bytes[0] == b'{'
        && let Some(close) = value.find('}').filter(|close| *close > 0)
    {
        let mut contract = value[1..close].trim();
        if let Some(colon) = contract.find(':') {
            contract = contract[..colon].trim();
        }
        return (contract.to_owned(), value[close + 1..].trim().to_owned());
    }
    if bytes[0] == b'('
        && let Some(close) = value.find(')').filter(|close| *close > 0)
    {
        return (
            value[1..close].trim().to_owned(),
            value[close + 1..].trim().to_owned(),
        );
    }
    let fields: Vec<&str> = value.split_whitespace().collect();
    if fields.len() == 1 {
        return (fields[0].to_owned(), String::new());
    }
    if fields.len() == 2 && fields[0] == fields[1] {
        return (fields[0].to_owned(), String::new());
    }
    (String::new(), value.to_owned())
}

pub(crate) fn merge_javascript_event_annotations(
    definition: &mut ComponentDefinition,
    annotations: &[VueComponentEvent],
    has_explicit_emits: bool,
) {
    for annotation in annotations {
        let found = component_definition_event(&definition.events, &annotation.name).is_some();
        if has_explicit_emits && !found {
            // An explicit emits contract wins over possibly stale annotations.
            continue;
        }
        append_component_event(&mut definition.events, annotation.clone());
        append_unique(&mut definition.emits, &annotation.name);
    }
}

pub(crate) fn component_definition_event<'a>(
    events: &'a [VueComponentEvent],
    name: &str,
) -> Option<&'a VueComponentEvent> {
    let canonical = canonical_event_name(name);
    events
        .iter()
        .find(|event| canonical_event_name(&event.name) == canonical)
}

pub(crate) fn parse_method_names(node: Option<&SyntaxNode>) -> Vec<String> {
    let Some(node) = node.filter(|node| node.kind() == jssyntax::JS_OBJECT) else {
        return Vec::new();
    };
    jsquery::properties(node)
        .iter()
        .filter_map(jsquery::property_name)
        .filter(|name| !name.is_empty())
        .collect()
}

pub(crate) fn node_text(node: Option<&SyntaxNode>) -> String {
    node.map(|node| node.text().to_string()).unwrap_or_default()
}
