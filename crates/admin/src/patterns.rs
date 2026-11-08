//! Vue attribute and tag naming conventions (port of Go `patterns.go`).

use crate::*;

#[derive(Clone, Debug, Default, PartialEq, Eq)]
pub struct VueDirectiveReference {
    pub name: String,
    pub range: TextRange,
}

#[derive(Clone, Debug, Default, PartialEq, Eq)]
pub struct VueAttributeReference {
    pub name: String,
    pub range: TextRange,
}

fn text_range(start: u32, end: u32) -> TextRange {
    TextRange::new(TextSize::from(start), TextSize::from(end))
}

/// Recognizes DOM custom-element spelling and Vue's PascalCase spelling.
pub fn is_component_tag(tag_name: &str) -> bool {
    let tag_name = tag_name.trim();
    !tag_name.is_empty()
        && tag_name != "template"
        && (tag_name.contains('-') || tag_name.as_bytes()[0].is_ascii_uppercase())
}

/// Component names owned by Shopware's public registries (`sw-`/`mt-`).
pub fn is_shopware_component_tag(tag_name: &str) -> bool {
    let name = tag_name.trim().to_lowercase();
    name.starts_with("sw-") || name.starts_with("mt-")
}

/// Extracts a directive identity from `v-tooltip.bottom` or
/// `v-custom:argument.modifier`.
pub fn vue_directive_name(attribute_name: &str) -> String {
    let name = attribute_name.trim();
    let Some(name) = name.strip_prefix("v-") else {
        return String::new();
    };
    let name = match name.find([':', '.']) {
        Some(end) => &name[..end],
        None => name,
    };
    name.trim().to_owned()
}

/// The custom directive identity and its range within an attribute name.
pub fn vue_directive_reference_for_attribute(
    attribute_name: &str,
    name_range: TextRange,
) -> Option<VueDirectiveReference> {
    let name = vue_directive_name(attribute_name);
    if name.is_empty() || is_vue_builtin_directive(&name) {
        return None;
    }
    let start = u32::from(name_range.start()) + 2;
    Some(VueDirectiveReference {
        range: text_range(start, start + name.len() as u32),
        name,
    })
}

/// Vue's language-level directives.
pub fn is_vue_builtin_directive(name: &str) -> bool {
    matches!(
        name.trim().to_lowercase().as_str(),
        "bind"
            | "cloak"
            | "else"
            | "else-if"
            | "for"
            | "html"
            | "if"
            | "memo"
            | "model"
            | "on"
            | "once"
            | "pre"
            | "show"
            | "slot"
            | "text"
    )
}

/// Normalizes an attribute name to a prop name; empty for listeners, slot
/// shorthands and directives.
pub fn normalize_prop_name(attr_name: &str) -> String {
    let mut name = attr_name;
    if let Some(rest) = name.strip_prefix("v-bind:") {
        name = rest;
    } else if name.starts_with("v-on:") {
        return String::new();
    } else if let Some(rest) = name.strip_prefix(':') {
        name = rest;
    } else if name.starts_with('@') || name.starts_with('#') || name.starts_with("v-") {
        return String::new();
    }
    if let Some(modifier) = name.find('.') {
        name = &name[..modifier];
    }
    kebab_to_camel(name)
}

/// A static component prop attribute and its source range.
pub fn vue_prop_reference_for_attribute(
    attribute_name: &str,
    name_range: TextRange,
) -> Option<VueAttributeReference> {
    let name = normalize_prop_name(attribute_name);
    if name.is_empty() {
        return None;
    }
    let range = vue_attribute_argument_range(attribute_name, name_range)?;
    Some(VueAttributeReference { name, range })
}

/// The canonical event identity of a listener attribute.
pub fn normalize_event_name(attribute_name: &str) -> String {
    let name = attribute_name.trim();
    let name = if let Some(rest) = name.strip_prefix('@') {
        rest
    } else if let Some(rest) = name.strip_prefix("v-on:") {
        rest
    } else {
        return String::new();
    };
    let name = match name.find('.') {
        Some(modifier) => &name[..modifier],
        None => name,
    };
    canonical_event_name(name)
}

/// A static Vue listener and its public event-name range.
pub fn vue_event_reference_for_attribute(
    attribute_name: &str,
    name_range: TextRange,
) -> Option<VueAttributeReference> {
    let name = normalize_event_name(attribute_name);
    if name.is_empty() {
        return None;
    }
    let range = vue_attribute_argument_range(attribute_name, name_range)?;
    Some(VueAttributeReference { name, range })
}

pub(crate) fn vue_attribute_argument_range(
    attribute_name: &str,
    name_range: TextRange,
) -> Option<TextRange> {
    let prefix_length = if attribute_name.starts_with('@') || attribute_name.starts_with('#') {
        1
    } else if attribute_name.starts_with("v-on:") {
        "v-on:".len()
    } else if attribute_name.starts_with("v-slot:") {
        "v-slot:".len()
    } else if attribute_name.starts_with("v-model:") {
        "v-model:".len()
    } else if attribute_name.starts_with("v-bind:") {
        "v-bind:".len()
    } else if attribute_name.starts_with(':') {
        1
    } else {
        0
    };
    let name = &attribute_name.as_bytes()[prefix_length..];
    let name = match name.iter().position(|&byte| byte == b'.') {
        Some(modifier) => &name[..modifier],
        None => name,
    };
    if name.is_empty() {
        return None;
    }
    let start = u32::from(name_range.start()) + prefix_length as u32;
    let end = start + name.len() as u32;
    if end > u32::from(name_range.end()) {
        return None;
    }
    Some(text_range(start, end))
}

/// Recognizes static Vue model directives. The empty argument is the default
/// `v-model`; named arguments are returned in camelCase.
pub fn normalize_model_argument(attribute_name: &str) -> Option<String> {
    let name = attribute_name.trim();
    let name = match name.find('.') {
        Some(modifier) => &name[..modifier],
        None => name,
    };
    if name == "v-model" {
        return Some(String::new());
    }
    let argument = name.strip_prefix("v-model:")?.trim();
    if argument.is_empty() || argument.starts_with('[') {
        return None;
    }
    Some(kebab_to_camel(argument))
}

/// The argument of a named `v-model` directive.
pub fn vue_model_reference_for_attribute(
    attribute_name: &str,
    name_range: TextRange,
) -> Option<VueAttributeReference> {
    let argument = normalize_model_argument(attribute_name)?;
    if argument.is_empty() {
        return None;
    }
    let range = vue_attribute_argument_range(attribute_name, name_range)?;
    Some(VueAttributeReference {
        name: argument,
        range,
    })
}

/// Normalizes camelCase event declarations to kebab-case.
pub fn canonical_event_name(name: &str) -> String {
    camel_to_kebab(name.trim())
}

/// A static slot name from shorthand or long-form attribute spelling.
pub fn normalize_slot_name(attribute_name: &str) -> String {
    let name = attribute_name.trim();
    let name = if name == "v-slot" {
        return "default".to_owned();
    } else if let Some(rest) = name.strip_prefix('#') {
        rest
    } else if let Some(rest) = name.strip_prefix("v-slot:") {
        rest
    } else {
        return String::new();
    };
    let name = match name.find('.') {
        Some(modifier) => &name[..modifier],
        None => name,
    };
    if name.starts_with('[') {
        return String::new();
    }
    name.trim().to_owned()
}

/// An explicitly named slot consumer.
pub fn vue_slot_reference_for_attribute(
    attribute_name: &str,
    name_range: TextRange,
) -> Option<VueAttributeReference> {
    let name = normalize_slot_name(attribute_name);
    if name.is_empty() || attribute_name.trim() == "v-slot" {
        return None;
    }
    let range = vue_attribute_argument_range(attribute_name, name_range)?;
    Some(VueAttributeReference { name, range })
}

/// Converts kebab-case to camelCase.
pub fn kebab_to_camel(s: &str) -> String {
    let mut result = String::with_capacity(s.len());
    for (index, part) in s.split('-').enumerate() {
        if index == 0 || part.is_empty() {
            result.push_str(part);
            continue;
        }
        // Go upper-cases the first byte only.
        let first = part.as_bytes()[0];
        if first.is_ascii() {
            result.push(first.to_ascii_uppercase() as char);
            result.push_str(&part[1..]);
        } else {
            result.push_str(part);
        }
    }
    result
}

/// Converts camelCase to kebab-case.
pub fn camel_to_kebab(s: &str) -> String {
    let uppercase = s.bytes().filter(u8::is_ascii_uppercase).count();
    if uppercase == 0 {
        return s.to_owned();
    }
    let mut result = String::with_capacity(s.len() + uppercase);
    for (index, character) in s.char_indices() {
        if character.is_ascii_uppercase() {
            if index > 0 {
                result.push('-');
            }
            result.push(character.to_ascii_lowercase());
        } else {
            result.push(character);
        }
    }
    result
}
