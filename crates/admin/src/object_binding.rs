//! `v-bind="{ ... }"` object bindings.

use swlsp_parser_twig::ast::AstNode;

use crate::*;

#[derive(Clone, Debug, Default, PartialEq, Eq)]
pub struct VueObjectBindingField {
    pub name: String,
    pub expression: String,
    pub name_range: TextRange,
    pub expression_range: TextRange,
    pub shorthand: bool,
}

/// Extracts source-aware fields from an object literal. `complete` is false
/// for spreads, computed keys, methods, malformed entries or a trailing
/// runtime expression; statically named siblings are still returned.
pub fn vue_object_binding_fields(value: &str, base: u32) -> (Vec<VueObjectBindingField>, bool) {
    let bytes = value.as_bytes();
    let open = skip_object_binding_space(value, 0);
    if open >= bytes.len() || bytes[open] != b'{' {
        return (Vec::new(), false);
    }
    let Some(close) = matching_slot_delimiter(value, open, b'{', b'}') else {
        return (Vec::new(), false);
    };
    let mut complete = value[close + 1..].trim().is_empty();
    let segments = vue_object_binding_segments(value, open + 1, close);
    let mut result = Vec::with_capacity(segments.len());
    for segment in segments {
        match vue_object_binding_field(value, base, segment) {
            Some(field) => result.push(field),
            None => {
                if !value[segment.start..segment.end].trim().is_empty() {
                    complete = false;
                }
            }
        }
    }
    (result, complete)
}

#[derive(Clone, Copy, Debug)]
pub(crate) struct VueObjectBindingSegment {
    pub start: usize,
    pub end: usize,
}

pub(crate) fn vue_object_binding_segments(
    value: &str,
    start: usize,
    end: usize,
) -> Vec<VueObjectBindingSegment> {
    let bytes = value.as_bytes();
    let mut result = Vec::new();
    let mut segment_start = start;
    let mut state = SlotScanState::default();
    for (index, &byte) in bytes.iter().enumerate().take(end).skip(start) {
        if byte == b',' && state.top_level() {
            result.push(VueObjectBindingSegment {
                start: segment_start,
                end: index,
            });
            segment_start = index + 1;
            continue;
        }
        state.consume(byte);
    }
    result.push(VueObjectBindingSegment {
        start: segment_start,
        end,
    });
    result
}

pub(crate) fn vue_object_binding_field(
    value: &str,
    base: u32,
    segment: VueObjectBindingSegment,
) -> Option<VueObjectBindingField> {
    let bytes = value.as_bytes();
    let start = skip_object_binding_space(value, segment.start);
    let end = trim_object_binding_space(value, start, segment.end);
    if start >= end || bytes[start..end].starts_with(b"...") {
        return None;
    }
    let colon = object_binding_top_level_colon(value, start, end);
    let mut key_end = end;
    let mut expression_start = start;
    let shorthand = colon.is_none();
    if let Some(colon) = colon {
        key_end = trim_object_binding_space(value, start, colon);
        expression_start = skip_object_binding_space(value, colon + 1);
        if expression_start >= end {
            return None;
        }
    }
    let mut key_start = start;
    key_end = trim_object_binding_space(value, key_start, key_end);
    if key_start >= key_end {
        return None;
    }
    let mut name = &bytes[key_start..key_end];
    if name.len() >= 2 && (name[0] == b'\'' || name[0] == b'"') && name[name.len() - 1] == name[0] {
        key_start += 1;
        key_end -= 1;
        name = &bytes[key_start..key_end];
    }
    let name = std::str::from_utf8(name).ok()?;
    if !is_slot_property_name(name) {
        return None;
    }
    if shorthand && !is_slot_identifier(name) {
        return None;
    }
    let mut expression_end = end;
    if shorthand {
        expression_start = key_start;
        expression_end = key_end;
    }
    Some(VueObjectBindingField {
        name: name.to_owned(),
        expression: String::from_utf8_lossy(&bytes[expression_start..expression_end])
            .trim()
            .to_owned(),
        name_range: slot_text_range(base + key_start as u32, base + key_end as u32),
        expression_range: slot_text_range(
            base + expression_start as u32,
            base + expression_end as u32,
        ),
        shorthand,
    })
}

pub(crate) fn object_binding_top_level_colon(
    value: &str,
    start: usize,
    end: usize,
) -> Option<usize> {
    let bytes = value.as_bytes();
    let mut state = SlotScanState::default();
    for (index, &byte) in bytes.iter().enumerate().take(end).skip(start) {
        if byte == b':' && state.top_level() {
            return Some(index);
        }
        state.consume(byte);
    }
    None
}

pub(crate) fn skip_object_binding_space(value: &str, mut start: usize) -> usize {
    let bytes = value.as_bytes();
    while start < bytes.len() && is_slot_space(bytes[start]) {
        start += 1;
    }
    start
}

pub(crate) fn trim_object_binding_space(value: &str, start: usize, mut end: usize) -> usize {
    let bytes = value.as_bytes();
    while end > start && is_slot_space(bytes[end - 1]) {
        end -= 1;
    }
    end
}

/// `v-bind` attribute value inners of a starting tag, with their text.
fn v_bind_inners(start_tag: &SyntaxNode) -> Vec<SyntaxNode> {
    let Some(tag) = twigast::HtmlStartingTag::cast(start_tag.clone()) else {
        return Vec::new();
    };
    tag.attributes()
        .filter(|attribute| {
            twigquery::html_attribute_name(attribute.syntax()).as_deref() == Some("v-bind")
        })
        .filter_map(|attribute| attribute.value()?.inner())
        .map(|inner| inner.syntax().clone())
        .collect()
}

fn range_contains_offset(range: TextRange, offset: u32) -> bool {
    offset >= u32::from(range.start()) && offset <= u32::from(range.end())
}

/// Resolves a public prop key inside a component-level `v-bind` object.
pub fn twig_component_object_binding_field_at_offset(
    root: &SyntaxNode,
    offset: u32,
) -> Option<(SyntaxNode, VueObjectBindingField)> {
    for start_tag in twigquery::iterate_nodes(root, &[twigsyntax::HTML_STARTING_TAG]) {
        if !range_contains_offset(start_tag.text_range(), offset) {
            continue;
        }
        for inner in v_bind_inners(&start_tag) {
            if !range_contains_offset(inner.text_range(), offset) {
                continue;
            }
            let (fields, _) = vue_object_binding_fields(
                &inner.text_string(),
                u32::from(inner.text_range().start()),
            );
            for field in fields {
                if range_contains_offset(field.name_range, offset) {
                    return Some((start_tag, field));
                }
            }
        }
    }
    None
}

/// Resolves a direct string-literal expression inside a component-level
/// `v-bind` object; the range covers only the literal content.
pub fn twig_component_object_binding_value_at_offset(
    root: &SyntaxNode,
    offset: u32,
) -> Option<(SyntaxNode, VueObjectBindingField, TextRange)> {
    for start_tag in twigquery::iterate_nodes(root, &[twigsyntax::HTML_STARTING_TAG]) {
        if !range_contains_offset(start_tag.text_range(), offset) {
            continue;
        }
        for inner in v_bind_inners(&start_tag) {
            if !range_contains_offset(inner.text_range(), offset) {
                continue;
            }
            let (fields, _) = vue_object_binding_fields(
                &inner.text_string(),
                u32::from(inner.text_range().start()),
            );
            for field in fields {
                if field.shorthand {
                    continue;
                }
                let Some((_, content_start, content_end)) =
                    vue_static_string_literal(&field.expression)
                else {
                    continue;
                };
                let start = u32::from(field.expression_range.start());
                let value_range = slot_text_range(start + content_start, start + content_end);
                if range_contains_offset(value_range, offset) {
                    return Some((start_tag, field, value_range));
                }
            }
        }
    }
    None
}

/// Reports a top-level key-editing position inside a component `v-bind`
/// object, also for incomplete literals.
pub fn twig_component_object_binding_key_context_at_offset(
    root: &SyntaxNode,
    offset: u32,
) -> Option<(SyntaxNode, Vec<VueObjectBindingField>)> {
    for start_tag in twigquery::iterate_nodes(root, &[twigsyntax::HTML_STARTING_TAG]) {
        if !range_contains_offset(start_tag.text_range(), offset) {
            continue;
        }
        if let Some(tag) = twigast::HtmlStartingTag::cast(start_tag.clone()) {
            for attribute in tag.attributes() {
                if twigquery::html_attribute_name(attribute.syntax()).as_deref() != Some("v-bind") {
                    continue;
                }
                let Some(inner) = attribute.value().and_then(|value| value.inner()) else {
                    continue;
                };
                let inner = inner.syntax().clone();
                let text = inner.text_string();
                let base = u32::from(inner.text_range().start());
                if offset < base || offset > base + text.len() as u32 {
                    continue;
                }
                if !vue_object_binding_key_position(&text, (offset - base) as usize) {
                    continue;
                }
                let (fields, _) = vue_object_binding_fields(&text, base);
                return Some((start_tag, fields));
            }
        }
    }
    None
}

pub(crate) fn vue_object_binding_key_position(value: &str, cursor: usize) -> bool {
    let bytes = value.as_bytes();
    let open = skip_object_binding_space(value, 0);
    if open >= bytes.len() || bytes[open] != b'{' || cursor <= open {
        return false;
    }
    let close = matching_slot_delimiter(value, open, b'{', b'}');
    let mut end = bytes.len();
    if let Some(close) = close {
        end = close;
        if cursor > close {
            return false;
        }
    }
    if cursor > end {
        return false;
    }
    for segment in vue_object_binding_segments(value, open + 1, end) {
        if cursor < segment.start || cursor > segment.end {
            continue;
        }
        let start = skip_object_binding_space(value, segment.start);
        if start < segment.end && bytes[start..segment.end].starts_with(b"...") {
            return false;
        }
        let colon = object_binding_top_level_colon(value, start, segment.end);
        return colon.is_none_or(|colon| cursor <= colon);
    }
    false
}
