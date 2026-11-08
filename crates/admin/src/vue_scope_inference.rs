//! `v-for` parsing and iterable/event payload type inference (port of Go
//! `vue_scope_inference.go`).
#![allow(clippy::needless_range_loop)] // byte loops mirror the Go scanners

use crate::*;

pub(crate) fn parse_twig_vue_for_bindings(
    value: &str,
    base: u32,
    scope_range: TextRange,
    expression_range: TextRange,
) -> Vec<TwigVueBinding> {
    let bytes = value.as_bytes();
    let Some((separator_start, separator_end)) = twig_vue_for_separator(value) else {
        return Vec::new();
    };
    let (mut left_start, mut left_end) = trim_byte_range(value, 0, separator_start);
    if left_start >= left_end {
        return Vec::new();
    }
    if bytes[left_start] == b'('
        && bytes[left_end - 1] == b')'
        && matching_slot_delimiter(value, left_start, b'(', b')') == Some(left_end - 1)
    {
        (left_start, left_end) = trim_byte_range(value, left_start + 1, left_end - 1);
    }
    let (iterable_start, iterable_end) = trim_byte_range(value, separator_end, bytes.len());
    let iterable = if iterable_start < iterable_end {
        value[iterable_start..iterable_end].to_owned()
    } else {
        String::new()
    };
    let mut result = Vec::new();
    for (ordinal, (part_start, part_end)) in
        split_twig_vue_top_level_ranges(value, left_start, left_end, b',')
            .into_iter()
            .enumerate()
    {
        let (start, end) = trim_byte_range(value, part_start, part_end);
        if start >= end || !is_slot_identifier(&value[start..end]) {
            continue;
        }
        result.push(TwigVueBinding {
            name: value[start..end].to_owned(),
            kind: TwigVueBindingKind::For,
            ordinal,
            declaration_range: slot_text_range(base + start as u32, base + end as u32),
            scope_range,
            expression_range,
            iterable: iterable.clone(),
            ..Default::default()
        });
    }
    result
}

/// The byte range of the top-level ` in ` / ` of ` separator.
pub(crate) fn twig_vue_for_separator(value: &str) -> Option<(usize, usize)> {
    let bytes = value.as_bytes();
    let mut state = SlotScanState::default();
    for index in 0..bytes.len() {
        if state.top_level() {
            for word in [b"in", b"of"] {
                let end = index + word.len();
                if end > bytes.len()
                    || &bytes[index..end] != word
                    || index == 0
                    || !is_slot_space(bytes[index - 1])
                    || end == bytes.len()
                    || !is_slot_space(bytes[end])
                {
                    continue;
                }
                return Some((index, end));
            }
        }
        state.consume(bytes[index]);
    }
    None
}

pub(crate) fn trim_byte_range(value: &str, mut start: usize, mut end: usize) -> (usize, usize) {
    let bytes = value.as_bytes();
    while start < end && is_slot_space(bytes[start]) {
        start += 1;
    }
    while end > start && is_slot_space(bytes[end - 1]) {
        end -= 1;
    }
    (start, end)
}

pub(crate) fn split_twig_vue_top_level_ranges(
    value: &str,
    start: usize,
    end: usize,
    separator: u8,
) -> Vec<(usize, usize)> {
    let bytes = value.as_bytes();
    let mut result = Vec::new();
    let mut part_start = start;
    let mut state = SlotScanState::default();
    for index in start..end {
        if bytes[index] == separator && state.top_level() {
            result.push((part_start, index));
            part_start = index + 1;
            continue;
        }
        state.consume(bytes[index]);
    }
    result.push((part_start, end));
    result
}

pub(crate) fn event_payload_type(value: &str) -> String {
    let value = value.trim();
    if value.is_empty() {
        return String::new();
    }
    if value.starts_with('[') {
        let Some(close) = matching_slot_delimiter(value, 0, b'[', b']') else {
            return String::new();
        };
        let parameters = split_slot_top_level(&value[1..close], b',');
        return parameters
            .first()
            .map_or_else(String::new, |first| vue_event_parameter_type(first));
    }
    if value.starts_with('(') {
        let Some(close) = matching_slot_delimiter(value, 0, b'(', b')') else {
            return String::new();
        };
        let parameters = split_slot_top_level(&value[1..close], b',');
        if parameters.is_empty() {
            return String::new();
        }
        let mut parameter_index = 0;
        if parameters.len() > 1 && vue_event_discriminator_parameter(parameters[0]) {
            parameter_index = 1;
        }
        return vue_event_parameter_type(parameters[parameter_index]);
    }
    if matches!(
        trim_admin_type_parentheses(value),
        "void" | "undefined" | "never"
    ) {
        return String::new();
    }
    // Legacy @event annotations describe the payload directly.
    value.to_owned()
}

pub(crate) fn vue_event_parameter_type(parameter: &str) -> String {
    let mut parameter = parameter.trim();
    let rest = parameter.starts_with("...");
    if let Some(colon) = index_slot_top_level(parameter, b':') {
        parameter = parameter[colon + 1..].trim();
    }
    if rest && let Some(stripped) = parameter.strip_suffix("[]") {
        parameter = stripped.trim();
    }
    parameter.to_owned()
}

pub(crate) fn vue_event_discriminator_parameter(parameter: &str) -> bool {
    let parameter = parameter.trim();
    let Some(colon) = index_slot_top_level(parameter, b':') else {
        return false;
    };
    let name = &parameter[..colon];
    let name = name.strip_suffix('?').unwrap_or(name).trim();
    if !matches!(name, "event" | "evt" | "e") {
        return false;
    }
    let event_type = parameter[colon + 1..].trim().as_bytes();
    event_type.len() >= 2
        && (event_type[0] == b'\'' || event_type[0] == b'"')
        && event_type[event_type.len() - 1] == event_type[0]
}

pub(crate) fn native_event_payload_type(name: &str) -> String {
    match canonical_event_name(name).as_str() {
        "click" | "dblclick" | "mousedown" | "mouseup" | "mousemove" | "mouseenter"
        | "mouseleave" | "contextmenu" => "MouseEvent",
        "keydown" | "keyup" | "keypress" => "KeyboardEvent",
        "focus" | "blur" | "focusin" | "focusout" => "FocusEvent",
        "drag" | "dragstart" | "dragend" | "dragenter" | "dragleave" | "dragover" | "drop" => {
            "DragEvent"
        }
        "input" | "change" | "submit" | "reset" => "Event",
        _ => "",
    }
    .to_owned()
}

fn short_type_name(name: &str) -> &str {
    match name.rfind('.') {
        Some(separator) => &name[separator + 1..],
        None => name,
    }
}

/// The value type exposed by a Vue `v-for` over `value`. Plain `Array` has
/// no element information and returns unknown (empty).
pub fn vue_iterable_element_type(value: &str) -> String {
    let value = value.trim();
    let union = split_admin_type_top_level(value, b'|');
    if union.len() > 1 {
        let mut element_type = String::new();
        for branch in &union {
            let branch = branch.trim();
            if matches!(branch, "null" | "undefined" | "never") {
                continue;
            }
            let candidate = vue_iterable_element_type(branch);
            if candidate.is_empty() {
                return String::new();
            }
            if !element_type.is_empty() && element_type != candidate {
                return String::new();
            }
            element_type = candidate;
        }
        return element_type;
    }
    let value = normalize_vue_iterable_type(value);
    if value == "number" {
        return "number".to_owned();
    }
    if value == "string" {
        return "string".to_owned();
    }
    if let Some(element) = value.strip_suffix("[]") {
        return element.trim().to_owned();
    }
    let (name, arguments) = parse_admin_named_type(&value);
    if arguments.len() == 2 {
        match short_type_name(&name) {
            "Record" => return arguments[1].trim().to_owned(),
            "Map" | "ReadonlyMap" => {
                return format!("[{}, {}]", arguments[0].trim(), arguments[1].trim());
            }
            _ => {}
        }
    }
    for constructor in [
        "Array",
        "ReadonlyArray",
        "Iterable",
        "Set",
        "EntityCollection",
        "EntitySchema.EntityCollection",
    ] {
        if !value.starts_with(constructor) || !value[constructor.len()..].starts_with('<') {
            continue;
        }
        let open = constructor.len();
        if matching_slot_delimiter(&value, open, b'<', b'>') == Some(value.len() - 1) {
            let element = value[open + 1..value.len() - 1].trim();
            if constructor.ends_with("EntityCollection") {
                return format!("Entity<{element}>");
            }
            return element.to_owned();
        }
    }
    let bytes = value.as_bytes();
    if bytes.len() >= 2
        && bytes[0] == b'['
        && matching_slot_delimiter(&value, 0, b'[', b']') == Some(bytes.len() - 1)
    {
        let mut types: Vec<&str> = Vec::new();
        let mut seen = HashSet::new();
        for item in split_slot_top_level(&value[1..value.len() - 1], b',') {
            let item = item.trim();
            if item.is_empty() || !seen.insert(item) {
                continue;
            }
            types.push(item);
        }
        return types.join(" | ");
    }
    if value.starts_with('{') {
        let mut element_type = String::new();
        for member in vue_type_members(&value) {
            element_type = merge_vue_types(&[&element_type, &member.type_]);
        }
        return element_type;
    }
    String::new()
}

/// Vue's `v-for` binding order: arrays, strings, ranges and iterables expose
/// `(value, index)`, objects and Records `(value, key, index)`.
pub fn vue_iterable_binding_type(value: &str, ordinal: usize) -> String {
    if ordinal == 0 {
        return vue_iterable_element_type(value);
    }
    if ordinal >= 2 {
        return "number".to_owned();
    }
    let key_type = vue_iterable_key_type(value);
    if !key_type.is_empty() {
        return key_type;
    }
    if !vue_iterable_element_type(value).is_empty() {
        return "number".to_owned();
    }
    String::new()
}

pub(crate) fn vue_iterable_key_type(value: &str) -> String {
    let value = value.trim();
    let union = split_admin_type_top_level(value, b'|');
    if union.len() > 1 {
        let mut key_type = String::new();
        for branch in &union {
            let branch = branch.trim();
            if matches!(branch, "null" | "undefined" | "never") {
                continue;
            }
            let candidate = vue_iterable_key_type(branch);
            if candidate.is_empty() {
                return String::new();
            }
            key_type = merge_vue_types(&[&key_type, &candidate]);
        }
        return key_type;
    }
    let value = normalize_vue_iterable_type(value);
    let (name, arguments) = parse_admin_named_type(&value);
    if arguments.len() == 2 && short_type_name(&name) == "Record" {
        return arguments[0].trim().to_owned();
    }
    if value.starts_with('{') {
        return "string".to_owned();
    }
    String::new()
}

pub(crate) fn normalize_vue_iterable_type(value: &str) -> String {
    let mut value = value.trim();
    if let Some(arrow) = value.rfind("=>") {
        value = value[arrow + 2..].trim();
    }
    while let Some(open) = value.rfind("PropType<") {
        let angle = open + "PropType".len();
        let Some(close) = matching_slot_delimiter(value, angle, b'<', b'>') else {
            break;
        };
        value = value[angle + 1..close].trim();
    }
    value = value.strip_prefix("readonly ").unwrap_or(value).trim();
    while value.len() >= 2
        && value.as_bytes()[0] == b'('
        && matching_slot_delimiter(value, 0, b'(', b')') == Some(value.len() - 1)
    {
        value = value[1..value.len() - 1].trim();
    }
    value.to_owned()
}
