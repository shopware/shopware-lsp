//! Symfony service option, YAML tag and keyword completion in service YAML
//! files.

use std::collections::HashSet;
use std::sync::Arc;

use swlsp_lsp::{CompletionProvider, CompletionRequest, Context};
use swlsp_lsp_protocol as protocol;
use swlsp_php_project::{Model, Version};
use swlsp_syntax::{LineIndex, TextRange, TextSize};

use super::{lower_ext, request_offset};
use crate::common::{leading_yaml_indent, protocol_range, text_edit};
use swlsp_textutil::lowercase;

use protocol::CompletionItemKind as Kind;

struct YamlServiceOption {
    name: &'static str,
    detail: &'static str,
    deprecated: bool,
}

const fn option(name: &'static str, detail: &'static str) -> YamlServiceOption {
    YamlServiceOption {
        name,
        detail,
        deprecated: false,
    }
}

const fn deprecated(name: &'static str, detail: &'static str) -> YamlServiceOption {
    YamlServiceOption {
        name,
        detail,
        deprecated: true,
    }
}

const YAML_SERVICE_OPTIONS: &[YamlServiceOption] = &[
    option("abstract", "bool"),
    option("alias", "service ID"),
    option("arguments", "constructor arguments"),
    option("autoconfigure", "bool · Symfony 3.3+"),
    option("autowire", "bool · Symfony 2.8+"),
    deprecated("autowiring_types", "Symfony 2.8–3.x"),
    option("bind", "named argument defaults · Symfony 2.8+"),
    option("calls", "post-construction calls"),
    option("class", "PHP class"),
    option("configurator", "callable · Symfony 2.8+"),
    option("decorates", "decorated service ID"),
    option("decoration_inner_name", "inner service ID"),
    option(
        "decoration_on_invalid",
        "exception | ignore | null · Symfony 4.4+",
    ),
    option("decoration_priority", "int · Symfony 2.8+"),
    option("deprecated", "bool | message · Symfony 2.8+"),
    option("exclude", "resource exclusions · Symfony 3.3+"),
    option("factory", "factory callable · Symfony 2.6+"),
    deprecated("factory_class", "legacy factory class"),
    deprecated("factory_method", "legacy factory method"),
    deprecated("factory_service", "legacy factory service"),
    option("file", "required file · Symfony 2.8+"),
    option("lazy", "bool"),
    option("parent", "parent service ID"),
    option("properties", "property injections · Symfony 5.1+"),
    option("public", "bool"),
    option("resource", "PSR-4 resource · Symfony 3.3+"),
    deprecated("scope", "request | prototype · Symfony ≤3"),
    option("shared", "bool · Symfony 3.0+"),
    deprecated("synchronized", "legacy synchronized service"),
    option("synthetic", "bool"),
    option("tags", "service tags"),
];

/// Completes Symfony service options, YAML value tags and keywords.
pub struct YamlServiceAuthoringCompletionProvider {
    project: Option<Arc<Model>>,
}

impl YamlServiceAuthoringCompletionProvider {
    pub fn new(project: Option<Arc<Model>>) -> Self {
        Self { project }
    }
}

impl CompletionProvider for YamlServiceAuthoringCompletionProvider {
    fn get_completions(
        &self,
        _ctx: &Context,
        request: &CompletionRequest,
    ) -> Vec<protocol::CompletionItem> {
        let Some(line_index) = request.line_index() else {
            return Vec::new();
        };
        let extension = lower_ext(&request.params.text_document.uri);
        if extension != ".yaml" && extension != ".yml" {
            return Vec::new();
        }
        let source = request.source();
        let Some(offset) = request_offset(request) else {
            return Vec::new();
        };
        if let Some(context) = yaml_service_option_context(source, offset) {
            return yaml_service_option_items(&context, line_index);
        }
        let Some(value) = yaml_value_completion_context(source, offset) else {
            return Vec::new();
        };
        let path = swlsp_uriutil::path(&request.params.text_document.uri).unwrap_or_default();
        self.yaml_value_items(
            &value,
            line_index,
            yaml_configuration_file(&path),
            yaml_inside_service_arguments(source, offset as usize),
        )
    }

    fn trigger_characters(&self) -> Vec<String> {
        ["!", "_"].map(String::from).to_vec()
    }
}

pub(super) fn text_range(start: usize, end: usize) -> TextRange {
    TextRange::new(TextSize::from(start as u32), TextSize::from(end as u32))
}

struct ServiceOptionContext {
    range: TextRange,
    complete: bool,
    existing: HashSet<String>,
    current: String,
}

/// The cursor as a usable byte index: inside the source and on a character
/// boundary.
pub(super) fn cursor_in(source: &str, offset: u32) -> Option<usize> {
    let cursor = offset as usize;
    (cursor <= source.len() && source.is_char_boundary(cursor)).then_some(cursor)
}

fn yaml_service_option_context(source: &str, offset: u32) -> Option<ServiceOptionContext> {
    let cursor = cursor_in(source, offset)?;
    if let Some(context) = yaml_flow_service_option_context(source, cursor) {
        return Some(context);
    }
    let (line_start, line_end) = yaml_line_bounds(source, cursor);
    let line = &source[line_start..line_end];
    let line = line.strip_suffix('\r').unwrap_or(line);
    let indent = leading_yaml_indent(line);
    let key_start = line_start + indent;
    if cursor < key_start {
        return None;
    }
    let key_end = yaml_identifier_end(source, key_start, line_end);
    if cursor > key_end || !yaml_option_prefix(&source[key_start..cursor]) {
        return None;
    }
    let complete = yaml_key_has_colon(source, key_end, line_end);
    let parent = yaml_parent_mapping_line(source, line_start, indent)?;
    if !parent.empty_value {
        return None;
    }
    let grandparent = yaml_parent_mapping_line(source, parent.start, parent.indent)?;
    if grandparent.key != "services" {
        if grandparent.key != "_instanceof" {
            return None;
        }
        let services = yaml_parent_mapping_line(source, grandparent.start, grandparent.indent)?;
        if services.key != "services" {
            return None;
        }
    }
    Some(ServiceOptionContext {
        range: text_range(key_start, key_end),
        complete,
        existing: yaml_block_mapping_keys(source, parent.start, parent.indent, indent),
        current: source[key_start..key_end].to_string(),
    })
}

fn yaml_flow_service_option_context(source: &str, cursor: usize) -> Option<ServiceOptionContext> {
    let bytes = source.as_bytes();
    let (line_start, line_end) = yaml_line_bounds(source, cursor);
    let before = &source[line_start..cursor];
    let open = before.rfind('{')?;
    if before.rfind('}').is_some_and(|close| close > open) {
        return None;
    }
    let open = open + line_start;
    let container_prefix = &source[line_start..open];
    let colon = container_prefix.find(':')?;
    if !container_prefix[colon + 1..].trim().is_empty() {
        return None;
    }
    let indent = leading_yaml_indent(&source[line_start..line_end]);
    let parent = yaml_parent_mapping_line(source, line_start, indent)?;
    if parent.key != "services" {
        return None;
    }
    let mut segment_start = open + 1;
    if let Some(comma) = source[open + 1..cursor].rfind(',') {
        segment_start = open + 1 + comma + 1;
    }
    while segment_start < cursor && matches!(bytes[segment_start], b' ' | b'\t') {
        segment_start += 1;
    }
    let key_end = yaml_identifier_end(source, segment_start, line_end);
    if cursor > key_end || !yaml_option_prefix(&source[segment_start..cursor]) {
        return None;
    }
    let close = source[cursor..line_end]
        .find('}')
        .map_or(line_end, |end| cursor + end);
    Some(ServiceOptionContext {
        range: text_range(segment_start, key_end),
        complete: yaml_key_has_colon(source, key_end, close),
        existing: yaml_flow_mapping_keys(&source[open + 1..close]),
        current: source[segment_start..key_end].to_string(),
    })
}

pub(super) struct YamlMappingLine {
    pub start: usize,
    pub indent: usize,
    pub key: String,
    pub empty_value: bool,
}

pub(super) fn yaml_parent_mapping_line(
    source: &str,
    before: usize,
    child_indent: usize,
) -> Option<YamlMappingLine> {
    let bytes = source.as_bytes();
    let mut search_end = before;
    while search_end > 0 {
        let mut previous_end = search_end;
        if previous_end > 0 && bytes[previous_end - 1] == b'\n' {
            previous_end -= 1;
        }
        if previous_end > 0 && bytes[previous_end - 1] == b'\r' {
            previous_end -= 1;
        }
        let previous_start = source[..previous_end]
            .rfind('\n')
            .map_or(0, |index| index + 1);
        let line = &source[previous_start..previous_end];
        search_end = previous_start;
        let trimmed = line.trim();
        if trimmed.is_empty() || trimmed.starts_with('#') {
            continue;
        }
        let indent = leading_yaml_indent(line);
        if indent >= child_indent {
            continue;
        }
        let (key, empty_value) = yaml_mapping_key_line(line)?;
        return Some(YamlMappingLine {
            start: previous_start,
            indent,
            key,
            empty_value,
        });
    }
    None
}

/// The key of a YAML mapping line and whether its value is empty.
pub(super) fn yaml_mapping_key_line(line: &str) -> Option<(String, bool)> {
    let trimmed = line.trim();
    if trimmed.is_empty() || trimmed.starts_with('-') {
        return None;
    }
    let colon = trimmed.find(':')?;
    if colon == 0 {
        return None;
    }
    let key = trim_quotes(trimmed[..colon].trim());
    if key.is_empty() {
        return None;
    }
    let rest = trimmed[colon + 1..].trim();
    Some((key.to_string(), rest.is_empty() || rest.starts_with('#')))
}

/// `value` without surrounding single and double quotes.
pub(super) fn trim_quotes(value: &str) -> &str {
    value.trim_matches(|c| c == '\'' || c == '"')
}

pub(super) fn yaml_block_mapping_keys(
    source: &str,
    parent_start: usize,
    parent_indent: usize,
    child_indent: usize,
) -> HashSet<String> {
    let bytes = source.as_bytes();
    let mut result = HashSet::new();
    let mut line_start = yaml_next_line_start(source, parent_start);
    while line_start < bytes.len() {
        let mut line_end = line_start;
        while line_end < bytes.len() && bytes[line_end] != b'\n' && bytes[line_end] != b'\r' {
            line_end += 1;
        }
        let line = &source[line_start..line_end];
        if !line.trim().is_empty() {
            let indent = leading_yaml_indent(line);
            if indent <= parent_indent {
                break;
            }
            if indent == child_indent
                && let Some((key, _)) = yaml_mapping_key_line(line)
            {
                result.insert(key);
            }
        }
        line_start = yaml_next_line_start(source, line_end);
    }
    result
}

pub(super) fn yaml_flow_mapping_keys(source: &str) -> HashSet<String> {
    source
        .split(',')
        .filter_map(|segment| yaml_mapping_key_line(segment.trim()))
        .map(|(key, _)| key)
        .collect()
}

fn yaml_service_option_items(
    context: &ServiceOptionContext,
    line_index: &LineIndex,
) -> Vec<protocol::CompletionItem> {
    let edit_range = protocol_range(context.range, line_index);
    YAML_SERVICE_OPTIONS
        .iter()
        .filter(|option| !context.existing.contains(option.name) || option.name == context.current)
        .map(|option| {
            let mut new_text = option.name.to_string();
            if !context.complete {
                new_text.push_str(": ");
            }
            protocol::CompletionItem {
                label: option.name.to_string(),
                kind: Some(Kind::PROPERTY),
                detail: format!("Symfony service option · {}", option.detail),
                deprecated: option.deprecated,
                text_edit: Some(text_edit(edit_range, new_text)),
                ..Default::default()
            }
        })
        .collect()
}

struct ValueContext {
    range: TextRange,
    prefix: String,
}

fn yaml_value_completion_context(source: &str, offset: u32) -> Option<ValueContext> {
    let cursor = cursor_in(source, offset)?;
    let bytes = source.as_bytes();
    let (line_start, line_end) = yaml_line_bounds(source, cursor);
    let delimiter = yaml_value_delimiter(source, line_start, cursor)?;
    let mut start = delimiter + 1;
    while start < cursor && matches!(bytes[start], b' ' | b'\t') {
        start += 1;
    }
    let prefix = &source[start..cursor];
    if prefix.contains([' ', '\t']) || prefix.starts_with('\'') || prefix.starts_with('"') {
        return None;
    }
    let mut end = cursor;
    while end < line_end && yaml_value_token_character(bytes[end]) {
        end += 1;
    }
    let mut next = end;
    while next < line_end && matches!(bytes[next], b' ' | b'\t') {
        next += 1;
    }
    if next < line_end && bytes[next] == b':' {
        return None;
    }
    Some(ValueContext {
        range: text_range(start, end),
        prefix: prefix.to_string(),
    })
}

fn yaml_value_delimiter(source: &str, line_start: usize, cursor: usize) -> Option<usize> {
    let bytes = source.as_bytes();
    let mut in_single = false;
    let mut in_double = false;
    let mut delimiter = None;
    for position in line_start..cursor {
        match bytes[position] {
            b'\'' => {
                if !in_double {
                    in_single = !in_single;
                }
            }
            b'"' => {
                if !in_single {
                    in_double = !in_double;
                }
            }
            b':' | b',' | b'[' => {
                if !in_single && !in_double {
                    delimiter = Some(position);
                }
            }
            b'-' if !in_single && !in_double && source[line_start..position].trim().is_empty() => {
                delimiter = Some(position);
            }
            _ => {}
        }
    }
    if in_single || in_double {
        return None;
    }
    delimiter
}

fn yaml_value_token_character(value: u8) -> bool {
    value.is_ascii_alphanumeric() || matches!(value, b'!' | b'/' | b'_' | b'.' | b'~' | b'-')
}

impl YamlServiceAuthoringCompletionProvider {
    fn yaml_value_items(
        &self,
        context: &ValueContext,
        line_index: &LineIndex,
        configuration_file: bool,
        service_arguments: bool,
    ) -> Vec<protocol::CompletionItem> {
        let edit_range = protocol_range(context.range, line_index);
        let mut items = Vec::new();
        let mut add = |label: &str, new_text: String, detail: String, kind: Kind| {
            items.push(protocol::CompletionItem {
                label: label.to_string(),
                kind: Some(kind),
                detail,
                text_edit: Some(text_edit(edit_range, new_text)),
                ..Default::default()
            });
        };
        if context.prefix.is_empty() || context.prefix.starts_with('!') {
            for tag in self.yaml_tags(configuration_file, service_arguments) {
                add(
                    tag,
                    format!("{tag} "),
                    "YAML/Symfony value tag".to_string(),
                    Kind::KEYWORD,
                );
            }
        }
        if context.prefix.is_empty() || !context.prefix.starts_with('!') {
            for (value, detail) in [
                ("~", "null"),
                ("null", "null"),
                ("true", "bool"),
                ("false", "bool"),
                (".inf", "float"),
            ] {
                add(
                    value,
                    value.to_string(),
                    format!("YAML {detail}"),
                    Kind::VALUE,
                );
            }
        }
        // Labels are unique, so the order is total.
        items.sort_by(|left, right| left.label.cmp(&right.label));
        items
    }

    fn yaml_tags(&self, configuration_file: bool, service_arguments: bool) -> Vec<&'static str> {
        let mut tags = vec!["!!binary", "!!float", "!!str"];
        if configuration_file {
            tags.push("!php/const");
            if swlsp_symfony::supports_yaml_container_enum(self.project.as_deref()) {
                tags.push("!php/enum");
            }
        }
        if !service_arguments {
            return tags;
        }
        let version = self
            .project
            .as_deref()
            .and_then(|project| {
                project.dependency_version(&[
                    "symfony/dependency-injection",
                    "symfony/framework-bundle",
                    "symfony/symfony",
                ])
            })
            .unwrap_or(Version {
                major: 7,
                minor: 0,
                patch: 0,
            });
        if version.at_least(4, 4) {
            tags.push("!tagged_iterator");
        } else if version.at_least(3, 4) {
            tags.push("!tagged");
        }
        if version.at_least(4, 3) {
            tags.push("!tagged_locator");
        }
        if version.at_least(4, 2) {
            tags.push("!service_locator");
        }
        if version.at_least(3, 3) {
            tags.push("!iterator");
            tags.push("!service");
        }
        tags
    }
}

fn yaml_inside_service_arguments(source: &str, cursor: usize) -> bool {
    let (line_start, _) = yaml_line_bounds(source, cursor);
    if source[line_start..cursor].contains("arguments:") {
        return true;
    }
    let indent = leading_yaml_indent(&source[line_start..]);
    yaml_parent_mapping_line(source, line_start, indent)
        .is_some_and(|parent| parent.key == "arguments")
}

fn yaml_configuration_file(path: &str) -> bool {
    let slash_path = swlsp_uriutil::filepath::to_slash(path);
    let base = lowercase(&swlsp_uriutil::filepath::base(path));
    slash_path.contains("/config/") || base.starts_with("config.") || base.starts_with("services.")
}

pub(super) fn yaml_line_bounds(source: &str, cursor: usize) -> (usize, usize) {
    let start = source[..cursor].rfind('\n').map_or(0, |index| index + 1);
    let end = source[cursor..]
        .find('\n')
        .map_or(source.len(), |position| cursor + position);
    (start, end)
}

fn is_identifier_byte(value: u8) -> bool {
    value.is_ascii_alphanumeric() || value == b'_' || value == b'-'
}

pub(super) fn yaml_identifier_end(source: &str, start: usize, limit: usize) -> usize {
    let bytes = source.as_bytes();
    let mut end = start;
    while end < limit && is_identifier_byte(bytes[end]) {
        end += 1;
    }
    end
}

pub(super) fn yaml_option_prefix(value: &str) -> bool {
    value.bytes().all(is_identifier_byte)
}

pub(super) fn yaml_key_has_colon(source: &str, key_end: usize, limit: usize) -> bool {
    let bytes = source.as_bytes();
    let mut position = key_end;
    while position < limit && matches!(bytes[position], b' ' | b'\t') {
        position += 1;
    }
    position < limit && bytes[position] == b':'
}

pub(super) fn yaml_next_line_start(source: &str, position: usize) -> usize {
    source.as_bytes()[position.min(source.len())..]
        .iter()
        .position(|byte| *byte == b'\n')
        .map_or(source.len(), |index| position + index + 1)
}
