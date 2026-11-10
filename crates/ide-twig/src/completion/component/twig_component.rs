//! Twig component names, props, blocks and Live actions.

use std::collections::{BTreeMap, HashSet};
use std::sync::{Arc, LazyLock};

use regex::Regex;
use swlsp_lsp::{CompletionProvider, CompletionRequest, Context};
use swlsp_lsp_protocol as protocol;
use swlsp_syntax::{LineIndex, TextRange, TextSize};
use swlsp_textutil::{eq_ignore_case, lowercase};
use swlsp_twigcomponent::Index;

use crate::completion::common::{
    component_prop_detail, protocol_range, sort_completion_items, text_edit,
};
use crate::completion::component_util::{
    ascii_regex, kind, last_index_whitespace, request_offset, request_path, snippet, source_before,
};

static COMPONENT_FUNCTION: LazyLock<Regex> =
    LazyLock::new(|| ascii_regex(r#"(?is)\bcomponent\s*\(\s*['"][A-Za-z0-9_:-]*$"#));
static COMPONENT_BLOCK: LazyLock<Regex> =
    LazyLock::new(|| ascii_regex(r#"(?is)\{%-?\s*component\s+(?:['"])?[A-Za-z0-9_:-]*$"#));
static COMPONENT_HTML: LazyLock<Regex> =
    LazyLock::new(|| ascii_regex(r"(?is)</?twig:[A-Za-z0-9_:-]*$"));
static COMPONENT_HTML_PROPS: LazyLock<Regex> =
    LazyLock::new(|| ascii_regex(r"(?is)<twig:([A-Za-z_][A-Za-z0-9_:-]*)\s+([^>]*)$"));
static COMPONENT_HTML_ATTRIBUTE: LazyLock<Regex> =
    LazyLock::new(|| ascii_regex(r"(?:^|\s):?([A-Za-z_][A-Za-z0-9_-]*)\s*="));
static COMPONENT_BLOCK_NAME: LazyLock<Regex> =
    LazyLock::new(|| ascii_regex(r#"(?is)<twig:block\s+[^>]*\bname\s*=\s*['"][A-Za-z0-9_-]*$"#));
static LIVE_ACTION_ATTRIBUTE: LazyLock<Regex> =
    LazyLock::new(|| ascii_regex(r#"(?is)\bdata-live-action-param\s*=\s*["']([^"']*)["']"#));
pub(super) static LIVE_ARGUMENT_ATTRIBUTE: LazyLock<Regex> =
    LazyLock::new(|| ascii_regex(r"(?i)\bdata-live-([a-z0-9_-]+)-param\s*="));

pub struct TwigComponentCompletionProvider {
    index: Option<Arc<Index>>,
}

impl TwigComponentCompletionProvider {
    pub fn new(index: Option<Arc<Index>>) -> Self {
        Self { index }
    }
}

impl CompletionProvider for TwigComponentCompletionProvider {
    fn get_completions(
        &self,
        _ctx: &Context,
        request: &CompletionRequest,
    ) -> Vec<protocol::CompletionItem> {
        let Some(index) = self.index.as_deref() else {
            return Vec::new();
        };
        let Some(line_index) = request.line_index() else {
            return Vec::new();
        };
        if !lowercase(&request.params.text_document.uri).ends_with(".twig") {
            return Vec::new();
        }
        let Some(offset) = request_offset(request) else {
            return Vec::new();
        };
        let path = request_path(request);
        let source = request.source();
        if let Some(argument) = live_action_argument_completion_context(source, offset) {
            return live_action_argument_completions(index, &path, &argument, line_index);
        }
        if let Some((action, present)) = request
            .node
            .as_ref()
            .and_then(swlsp_twigcomponent::live_action_argument_context)
        {
            return live_action_hash_argument_completions(index, &path, &action, &present);
        }
        if let Some(root) = request.root.as_ref()
            && swlsp_twigcomponent::live_action_reference_at(&path, root, offset).is_some()
        {
            return live_action_completions(index, &path);
        }
        let before = source_before(source, offset);
        if COMPONENT_BLOCK_NAME.is_match(before) {
            let component = request
                .node
                .as_ref()
                .map(swlsp_twigcomponent::enclosing_html_component)
                .unwrap_or_default();
            if !component.is_empty() {
                return block_completions(index, &component);
            }
        }
        if let Some(context) = html_prop_completion_context(before) {
            return prop_completions(index, &context);
        }
        if !component_name_completion_context(before) {
            return Vec::new();
        }
        let Ok(components) = index.components() else {
            return Vec::new();
        };
        let mut by_name: BTreeMap<String, swlsp_twigcomponent::Component> = BTreeMap::new();
        for component in components {
            match by_name.get(&component.name) {
                Some(existing) if !(existing.class.is_empty() && !component.class.is_empty()) => {}
                _ => {
                    by_name.insert(component.name.clone(), component);
                }
            }
        }
        let mut items: Vec<protocol::CompletionItem> = by_name
            .into_iter()
            .map(|(name, component)| {
                let mut detail = if component.class.is_empty() {
                    "anonymous Twig component".to_string()
                } else {
                    component.class.clone()
                };
                if component.live {
                    detail.push_str(" • live component");
                }
                protocol::CompletionItem {
                    label: name,
                    kind: kind(protocol::CompletionItemKind::CLASS),
                    detail,
                    ..Default::default()
                }
            })
            .collect();
        sort_completion_items(&mut items);
        items
    }

    fn trigger_characters(&self) -> Vec<String> {
        [":", "'", "\"", " "].map(String::from).to_vec()
    }
}

fn live_action_completions(index: &Index, path: &str) -> Vec<protocol::CompletionItem> {
    let Ok(actions) = index.live_actions_for_template(path) else {
        return Vec::new();
    };
    let mut items = Vec::with_capacity(actions.len());
    let mut seen = HashSet::new();
    for action in actions {
        if !seen.insert(lowercase(&action.name)) {
            continue;
        }
        let mut detail = format!("{}::{}()", action.class, action.method);
        if !action.parameters.is_empty() {
            let names: Vec<String> = action
                .parameters
                .iter()
                .map(|parameter| {
                    let php_name = if parameter.php_name.is_empty() {
                        &parameter.name
                    } else {
                        &parameter.php_name
                    };
                    let mut name = format!("${php_name}");
                    if !parameter.ty.is_empty() {
                        name = format!("{} {name}", parameter.ty);
                    }
                    if parameter.optional {
                        name.push('?');
                    }
                    name
                })
                .collect();
            detail = format!("{}::{}({})", action.class, action.method, names.join(", "));
        }
        items.push(protocol::CompletionItem {
            label: action.name,
            kind: kind(protocol::CompletionItemKind::METHOD),
            detail,
            ..Default::default()
        });
    }
    sort_completion_items(&mut items);
    items
}

fn live_action_hash_argument_completions(
    index: &Index,
    path: &str,
    action_name: &str,
    present: &[String],
) -> Vec<protocol::CompletionItem> {
    let Ok(actions) = index.live_actions_for_template(path) else {
        return Vec::new();
    };
    let configured: HashSet<String> = present.iter().map(|name| lowercase(name)).collect();
    let mut seen = HashSet::new();
    let mut items = Vec::new();
    for action in &actions {
        if !eq_ignore_case(&action.name, action_name) {
            continue;
        }
        for parameter in &action.parameters {
            let key = lowercase(&parameter.name);
            if configured.contains(&key) || !seen.insert(key) {
                continue;
            }
            let mut detail = format!("{} ${}", parameter.ty, parameter.php_name);
            if parameter.name != parameter.php_name {
                detail.push_str(&format!(" • LiveArg({})", parameter.name));
            }
            items.push(protocol::CompletionItem {
                label: parameter.name.clone(),
                kind: kind(protocol::CompletionItemKind::PROPERTY),
                detail: detail.trim().to_string(),
                insert_text: format!("{}: $0", parameter.name),
                insert_text_format: snippet(),
                ..Default::default()
            });
        }
    }
    sort_completion_items(&mut items);
    items
}

pub(super) struct LiveArgumentContext {
    pub name: String,
    pub present: HashSet<String>,
    pub start: u32,
    pub end: u32,
}

fn live_action_argument_completion_context(
    source: &str,
    offset: u32,
) -> Option<LiveArgumentContext> {
    let before = source_before(source, offset);
    let offset = before.len() as u32;
    let open = before.rfind('<')?;
    if before.rfind('>').is_some_and(|close| close > open) {
        return None;
    }
    let fragment = &before[open..];
    let current_start = last_index_whitespace(fragment)? + 1;
    let current = &fragment[current_start..];
    if !lowercase(current).starts_with("data-live-") || current.contains('=') {
        return None;
    }
    let action_match = LIVE_ACTION_ATTRIBUTE.captures(fragment)?;
    let mut action = action_match.get(1)?.as_str().trim();
    if let Some(separator) = action.rfind('|') {
        action = action[separator + 1..].trim();
    }
    if action.is_empty() {
        return None;
    }
    let mut present = HashSet::new();
    for captures in LIVE_ARGUMENT_ATTRIBUTE.captures_iter(fragment) {
        let Some(name) = captures.get(1) else {
            continue;
        };
        if eq_ignore_case(name.as_str(), "action") {
            continue;
        }
        present.insert(lowercase(&live_argument_name(name.as_str())));
    }
    Some(LiveArgumentContext {
        name: action.to_string(),
        present,
        start: (open + current_start) as u32,
        end: offset,
    })
}

pub(super) fn byte_range(start: u32, end: u32) -> TextRange {
    TextRange::new(TextSize::from(start), TextSize::from(end))
}

fn live_action_argument_completions(
    index: &Index,
    path: &str,
    context: &LiveArgumentContext,
    line_index: &LineIndex,
) -> Vec<protocol::CompletionItem> {
    let Ok(actions) = index.live_actions_for_template(path) else {
        return Vec::new();
    };
    let edit_range = protocol_range(byte_range(context.start, context.end), line_index);
    let mut seen = HashSet::new();
    let mut items = Vec::new();
    for action in &actions {
        if !eq_ignore_case(&action.name, &context.name) {
            continue;
        }
        for parameter in &action.parameters {
            let key = lowercase(&parameter.name);
            if seen.contains(&key) || context.present.contains(&key) {
                continue;
            }
            seen.insert(key);
            let attribute = format!(
                "data-live-{}-param",
                live_argument_attribute_segment(&parameter.name)
            );
            let mut detail = format!("{} ${}", parameter.ty, parameter.php_name);
            if parameter.name != parameter.php_name {
                detail.push_str(&format!(" • LiveArg({})", parameter.name));
            }
            items.push(protocol::CompletionItem {
                label: attribute.clone(),
                kind: kind(protocol::CompletionItemKind::PROPERTY),
                detail: detail.trim().to_string(),
                text_edit: Some(text_edit(edit_range, format!("{attribute}=\"$0\""))),
                insert_text_format: snippet(),
                ..Default::default()
            });
        }
    }
    sort_completion_items(&mut items);
    items
}

/// The Live action argument name of an attribute segment: `item-id`/`item_id` → `itemId`.
pub(super) fn live_argument_name(value: &str) -> String {
    let segments: Vec<&str> = value
        .split(['-', '_'])
        .filter(|segment| !segment.is_empty())
        .collect();
    let Some(first) = segments.first() else {
        return String::new();
    };
    let mut result = lowercase(first);
    for segment in &segments[1..] {
        let mut chars = segment.chars();
        if let Some(head) = chars.next() {
            result.push_str(&swlsp_textutil::uppercase(head.encode_utf8(&mut [0; 4])));
            result.push_str(&lowercase(chars.as_str()));
        }
    }
    result
}

/// The attribute segment of a Live action argument name: `itemId` → `item-id`.
pub(super) fn live_argument_attribute_segment(value: &str) -> String {
    let mut result = String::with_capacity(value.len() + 4);
    for (index, char) in value.char_indices() {
        if char.is_ascii_uppercase() {
            if index != 0 {
                result.push('-');
            }
            result.push(char.to_ascii_lowercase());
            continue;
        }
        if char == '_' {
            result.push('-');
            continue;
        }
        result.push(char);
    }
    result.trim_matches('-').to_string()
}

fn block_completions(index: &Index, component: &str) -> Vec<protocol::CompletionItem> {
    let Ok(blocks) = index.blocks(component) else {
        return Vec::new();
    };
    let mut items = Vec::with_capacity(blocks.len());
    let mut seen = HashSet::new();
    for block in blocks {
        if !seen.insert(block.name.clone()) {
            continue;
        }
        items.push(protocol::CompletionItem {
            label: block.name,
            kind: kind(protocol::CompletionItemKind::REFERENCE),
            detail: swlsp_uriutil::filepath::base(&block.file),
            ..Default::default()
        });
    }
    sort_completion_items(&mut items);
    items
}

struct HtmlPropContext {
    component: String,
    dynamic: bool,
    present: HashSet<String>,
}

fn html_prop_completion_context(before: &str) -> Option<HtmlPropContext> {
    let captures = COMPONENT_HTML_PROPS.captures(before)?;
    let attributes = captures.get(2)?.as_str();
    let mut current = attributes;
    if let Some(whitespace) = last_index_whitespace(current) {
        current = &current[whitespace + 1..];
    }
    let present = COMPONENT_HTML_ATTRIBUTE
        .captures_iter(attributes)
        .filter_map(|attribute| attribute.get(1))
        .map(|name| lowercase(name.as_str()))
        .collect();
    Some(HtmlPropContext {
        component: captures.get(1)?.as_str().to_string(),
        dynamic: current.starts_with(':'),
        present,
    })
}

fn component_name_completion_context(before: &str) -> bool {
    COMPONENT_FUNCTION.is_match(before)
        || COMPONENT_BLOCK.is_match(before)
        || COMPONENT_HTML.is_match(before)
}

fn prop_completions(index: &Index, context: &HtmlPropContext) -> Vec<protocol::CompletionItem> {
    let Ok(props) = index.props(&context.component) else {
        return Vec::new();
    };
    let mut items = Vec::new();
    for prop in &props {
        if context.present.contains(&lowercase(&prop.name)) {
            continue;
        }
        let label = if context.dynamic {
            format!(":{}", prop.name)
        } else {
            prop.name.clone()
        };
        let mut detail = component_prop_detail(prop);
        if detail.is_empty() {
            detail = "Twig component prop".to_string();
        }
        items.push(protocol::CompletionItem {
            insert_text: format!("{label}=\"$0\""),
            label,
            kind: kind(protocol::CompletionItemKind::PROPERTY),
            detail,
            insert_text_format: snippet(),
            ..Default::default()
        });
    }
    sort_completion_items(&mut items);
    items
}
