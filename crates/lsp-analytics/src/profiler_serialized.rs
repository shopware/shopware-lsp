//! A bounded reader for PHP `serialize()` data stored in Symfony profiles
//! (port of `profiler_serialized.go`).

use std::collections::HashMap;

use crate::profiler::{ProfilerRuntimeTwigComponent, go_lossy};
use crate::{lower, trim_space};

const MAX_PROFILER_SERIALIZED_DEPTH: usize = 64;

#[derive(Clone, Copy, Default)]
pub(crate) struct SerializedValue<'a> {
    pub raw: &'a [u8],
    pub text: Option<&'a [u8]>,
    pub integer: Option<i64>,
    pub body_start: usize,
    pub body_end: usize,
}

struct Pair<'a> {
    key: SerializedValue<'a>,
    value: SerializedValue<'a>,
}

pub(crate) fn read_value(
    content: &[u8],
    start: usize,
    depth: usize,
) -> Option<SerializedValue<'_>> {
    if start >= content.len() || depth > MAX_PROFILER_SERIALIZED_DEPTH {
        return None;
    }
    match content[start] {
        b'N' => {
            if content[start..].starts_with(b"N;") {
                return Some(SerializedValue {
                    raw: &content[start..start + 2],
                    ..SerializedValue::default()
                });
            }
            None
        }
        b's' => {
            if start + 2 > content.len() || content[start + 1] != b':' {
                return None;
            }
            let (length, cursor) = unsigned(content, start + 2, b':')?;
            if cursor + 1 >= content.len() || content[cursor + 1] != b'"' {
                return None;
            }
            let value_start = cursor + 2;
            let value_end = value_start.checked_add(length)?;
            if value_end.checked_add(2)? > content.len()
                || content[value_end] != b'"'
                || content[value_end + 1] != b';'
            {
                return None;
            }
            Some(SerializedValue {
                raw: &content[start..value_end + 2],
                text: Some(&content[value_start..value_end]),
                ..SerializedValue::default()
            })
        }
        b'i' | b'b' | b'd' | b'r' | b'R' => {
            if start + 2 > content.len() || content[start + 1] != b':' {
                return None;
            }
            let end = content[start + 2..].iter().position(|&byte| byte == b';')? + start + 2;
            let integer = if content[start] == b'i' {
                std::str::from_utf8(&content[start + 2..end])
                    .ok()
                    .and_then(|text| text.parse::<i64>().ok())
            } else {
                None
            };
            Some(SerializedValue {
                raw: &content[start..end + 1],
                integer,
                ..SerializedValue::default()
            })
        }
        b'a' => read_container(content, start, depth, false),
        b'O' => read_container(content, start, depth, true),
        _ => None,
    }
}

fn read_container(
    content: &[u8],
    start: usize,
    depth: usize,
    object: bool,
) -> Option<SerializedValue<'_>> {
    if start + 2 > content.len() || content[start + 1] != b':' {
        return None;
    }
    let mut count_start = start + 2;
    if object {
        let (class_length, cursor) = unsigned(content, start + 2, b':')?;
        if cursor + 1 >= content.len() || content[cursor + 1] != b'"' {
            return None;
        }
        let class_end = (cursor + 2).checked_add(class_length)?;
        if class_end >= content.len()
            || content[class_end] != b'"'
            || class_end + 1 >= content.len()
            || content[class_end + 1] != b':'
        {
            return None;
        }
        count_start = class_end + 2;
    }
    let (count, cursor) = unsigned(content, count_start, b':')?;
    if cursor + 1 >= content.len() || content[cursor + 1] != b'{' {
        return None;
    }
    if count > content.len() / 2 {
        return None;
    }
    let body_start = cursor + 2;
    let mut cursor = body_start;
    for _ in 0..count * 2 {
        let value = read_value(content, cursor, depth + 1)?;
        cursor += value.raw.len();
    }
    if cursor >= content.len() || content[cursor] != b'}' {
        return None;
    }
    Some(SerializedValue {
        raw: &content[start..cursor + 1],
        body_start: body_start - start,
        body_end: cursor - start,
        ..SerializedValue::default()
    })
}

/// Go `profilerSerializedUnsigned`: digits ending in `terminator`; returns
/// the value and the terminator offset.
fn unsigned(content: &[u8], start: usize, terminator: u8) -> Option<(usize, usize)> {
    let mut cursor = start;
    while cursor < content.len() && content[cursor].is_ascii_digit() {
        cursor += 1;
    }
    if cursor == start || cursor >= content.len() || content[cursor] != terminator {
        return None;
    }
    let value = std::str::from_utf8(&content[start..cursor])
        .ok()?
        .parse::<i64>()
        .ok()?;
    Some((usize::try_from(value).ok()?, cursor))
}

fn direct_pairs(content: &[u8]) -> Vec<Pair<'_>> {
    let Some(root) = read_value(content, 0, 0) else {
        return Vec::new();
    };
    if root.body_end <= root.body_start {
        return Vec::new();
    }
    let mut cursor = root.body_start;
    let mut result = Vec::new();
    while cursor < root.body_end {
        let Some(key) = read_value(root.raw, cursor, 1) else {
            break;
        };
        cursor += key.raw.len();
        let Some(value) = read_value(root.raw, cursor, 1) else {
            break;
        };
        cursor += value.raw.len();
        result.push(Pair { key, value });
    }
    result
}

fn property<'a>(content: &'a [u8], name: &str) -> Option<SerializedValue<'a>> {
    direct_pairs(content)
        .into_iter()
        .find(|pair| {
            pair.key
                .text
                .is_some_and(|text| property_name(text) == name.as_bytes())
        })
        .map(|pair| pair.value)
}

fn integer_key(content: &[u8], key: i64) -> Option<SerializedValue<'_>> {
    direct_pairs(content)
        .into_iter()
        .find(|pair| pair.key.integer == Some(key))
        .map(|pair| pair.value)
}

/// Strips the `\0Class\0` visibility prefix of a serialized property name.
fn property_name(name: &[u8]) -> &[u8] {
    match name.iter().rposition(|&byte| byte == 0) {
        Some(offset) => &name[offset + 1..],
        None => name,
    }
}

fn resolve_reference<'a>(value: SerializedValue<'a>, data: &'a [u8]) -> SerializedValue<'a> {
    let Some(reference) = integer_key(value.raw, 1) else {
        return value;
    };
    let Some(reference) = reference.integer else {
        return value;
    };
    integer_key(data, reference).unwrap_or(value)
}

/// The Twig components rendered by a request, grouped by name, most
/// rendered first.
pub(crate) fn profiler_twig_components(content: &[u8]) -> Vec<ProfilerRuntimeTwigComponent> {
    const MARKER: &[u8] = b"s:14:\"twig_component\";";
    let Some(offset) = content
        .windows(MARKER.len())
        .position(|window| window == MARKER)
    else {
        return Vec::new();
    };
    let Some(collector) = read_value(content, offset + MARKER.len(), 0) else {
        return Vec::new();
    };
    let Some(data_object) = property(collector.raw, "data") else {
        return Vec::new();
    };
    let Some(data) = property(data_object.raw, "data") else {
        return Vec::new();
    };
    let mut component_map = SerializedValue::default();
    for pair in direct_pairs(data.raw) {
        let Some(components) = property(pair.value.raw, "components") else {
            continue;
        };
        component_map = resolve_reference(components, data.raw);
        break;
    }
    if component_map.raw.is_empty() {
        return Vec::new();
    }

    let mut grouped: HashMap<String, ProfilerRuntimeTwigComponent> = HashMap::new();
    for pair in direct_pairs(component_map.raw) {
        let component = resolve_reference(pair.value, data.raw);
        let Some(current) = runtime_twig_component(component.raw) else {
            continue;
        };
        let key = lower(&current.name);
        if let Some(existing) = grouped.get_mut(&key) {
            if existing.class.is_empty() {
                existing.class = current.class;
            }
            if existing.template.is_empty() {
                existing.template = current.template;
            }
            existing.render_count = existing.render_count.wrapping_add(current.render_count);
            continue;
        }
        grouped.insert(key, current);
    }
    let mut result: Vec<ProfilerRuntimeTwigComponent> = grouped.into_values().collect();
    result.sort_by(|left, right| {
        right
            .render_count
            .cmp(&left.render_count)
            .then_with(|| lower(&left.name).cmp(&lower(&right.name)))
    });
    result
}

fn runtime_twig_component(content: &[u8]) -> Option<ProfilerRuntimeTwigComponent> {
    let mut result = ProfilerRuntimeTwigComponent::default();
    for pair in direct_pairs(content) {
        let Some(key) = pair.key.text else {
            continue;
        };
        let text = pair
            .value
            .text
            .map(|text| trim_space(&go_lossy(text)).to_owned());
        match property_name(key) {
            b"name" => {
                if let Some(text) = text {
                    result.name = text;
                }
            }
            b"class" => {
                if let Some(text) = text {
                    result.class = text.trim_matches('\\').to_owned();
                }
            }
            b"template" => {
                if let Some(text) = text {
                    result.template = text;
                }
            }
            b"render_count" => {
                if let Some(integer) = pair.value.integer {
                    result.render_count = integer;
                }
            }
            _ => {}
        }
    }
    let valid = !result.name.is_empty()
        && (!result.class.is_empty() || !result.template.is_empty() || result.render_count != 0);
    valid.then_some(result)
}
