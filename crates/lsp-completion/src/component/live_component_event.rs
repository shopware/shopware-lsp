//! Live Component event names and listener arguments.

use std::collections::{BTreeMap, HashSet};
use std::sync::{Arc, LazyLock};

use regex::Regex;
use swlsp_lsp::{CompletionProvider, CompletionRequest, Context};
use swlsp_lsp_protocol as protocol;
use swlsp_parser_php::query as phpquery;
use swlsp_parser_php::syntax::PHP_MEMBER_CALL;
use swlsp_syntax::{LineIndex, SyntaxNode};
use swlsp_textutil::eq_ignore_case;
use swlsp_twigcomponent::{Index, LiveActionParameter, LiveListener};

use super::twig_component::{
    LIVE_ARGUMENT_ATTRIBUTE, LiveArgumentContext, byte_range, live_argument_attribute_segment,
    live_argument_name,
};
use super::util::{
    ascii_regex, kind, last_index_whitespace, request_offset, request_path, snippet, source_before,
};
use crate::common::{protocol_range, sort_completion_items, text_edit};
use swlsp_textutil::lowercase;

static LIVE_EVENT_VALUE_COMPLETION: LazyLock<Regex> =
    LazyLock::new(|| ascii_regex(r#"(?is)\bdata-live-event-param\s*=\s*["']([^"']*)$"#));
static LIVE_EVENT_VALUE: LazyLock<Regex> =
    LazyLock::new(|| ascii_regex(r#"(?is)\bdata-live-event-param\s*=\s*["']([^"']*)["']"#));
static LIVE_EVENT_ACTION: LazyLock<Regex> = LazyLock::new(|| {
    ascii_regex(r#"(?is)\bdata-action\s*=\s*["'][^"']*live#emit(?:self|up)?[^"']*["']"#)
});

pub struct LiveComponentEventCompletionProvider {
    index: Option<Arc<Index>>,
}

impl LiveComponentEventCompletionProvider {
    pub fn new(index: Option<Arc<Index>>) -> Self {
        Self { index }
    }
}

impl CompletionProvider for LiveComponentEventCompletionProvider {
    fn get_completions(
        &self,
        _ctx: &Context,
        request: &CompletionRequest,
    ) -> Vec<protocol::CompletionItem> {
        let Some(index) = self.index.as_deref() else {
            return Vec::new();
        };
        let (Some(root), Some(line_index)) = (request.root.as_ref(), request.line_index()) else {
            return Vec::new();
        };
        let Some(offset) = request_offset(request) else {
            return Vec::new();
        };
        let path = request_path(request);
        match lowercase(swlsp_uriutil::filepath::ext(&path)).as_str() {
            ".php" => {
                let Some(node) = request.node.as_ref() else {
                    return Vec::new();
                };
                if let Some((event, present)) =
                    swlsp_twigcomponent::live_event_argument_context_php(node)
                {
                    return php_argument_completions(index, &event, &present);
                }
                if live_event_name_context_php(node) {
                    return event_completions(index);
                }
            }
            ".twig" => {
                let source = request.source();
                if let Some(argument) = live_event_argument_completion_context(source, offset) {
                    return twig_argument_completions(index, &argument, line_index);
                }
                if swlsp_twigcomponent::live_event_reference_at_twig(&path, root, offset).is_some()
                    || live_event_name_completion_context(source, offset)
                {
                    return event_completions(index);
                }
            }
            _ => {}
        }
        Vec::new()
    }

    fn trigger_characters(&self) -> Vec<String> {
        ["'", "\"", "-", "_"].map(String::from).to_vec()
    }
}

fn event_completions(index: &Index) -> Vec<protocol::CompletionItem> {
    let Ok(listeners) = index.live_listeners() else {
        return Vec::new();
    };
    let mut by_name: BTreeMap<String, Vec<LiveListener>> = BTreeMap::new();
    for listener in listeners {
        by_name
            .entry(lowercase(&listener.name))
            .or_default()
            .push(listener);
    }
    let mut items: Vec<protocol::CompletionItem> = by_name
        .values()
        .map(|current| {
            let listener = &current[0];
            let mut detail = live_listener_signature(listener);
            if current.len() > 1 {
                detail.push_str(" • ");
                detail.push_str(&live_event_listener_count(current.len()));
            }
            protocol::CompletionItem {
                label: listener.name.clone(),
                kind: kind(protocol::CompletionItemKind::EVENT),
                detail,
                ..Default::default()
            }
        })
        .collect();
    sort_completion_items(&mut items);
    items
}

fn php_argument_completions(
    index: &Index,
    event: &str,
    present: &[String],
) -> Vec<protocol::CompletionItem> {
    let Ok(listeners) = index.live_listeners() else {
        return Vec::new();
    };
    let configured: HashSet<String> = present.iter().map(|value| lowercase(value)).collect();
    let mut seen = HashSet::new();
    let mut items = Vec::new();
    for listener in &listeners {
        if !eq_ignore_case(&listener.name, event) {
            continue;
        }
        for parameter in &listener.parameters {
            if !parameter.live_arg {
                continue;
            }
            let key = lowercase(&parameter.name);
            if configured.contains(&key) || !seen.insert(key) {
                continue;
            }
            items.push(protocol::CompletionItem {
                label: parameter.name.clone(),
                kind: kind(protocol::CompletionItemKind::PROPERTY),
                detail: live_event_parameter_detail(parameter, listener),
                insert_text: format!("'{}' => $0", parameter.name),
                insert_text_format: snippet(),
                ..Default::default()
            });
        }
    }
    sort_completion_items(&mut items);
    items
}

fn twig_argument_completions(
    index: &Index,
    context: &LiveArgumentContext,
    line_index: &LineIndex,
) -> Vec<protocol::CompletionItem> {
    let Ok(listeners) = index.live_listeners() else {
        return Vec::new();
    };
    let edit_range = protocol_range(byte_range(context.start, context.end), line_index);
    let mut seen = HashSet::new();
    let mut items = Vec::new();
    for listener in &listeners {
        if !eq_ignore_case(&listener.name, &context.name) {
            continue;
        }
        for parameter in &listener.parameters {
            if !parameter.live_arg {
                continue;
            }
            let key = lowercase(&parameter.name);
            if context.present.contains(&key) || !seen.insert(key) {
                continue;
            }
            let attribute = format!(
                "data-live-{}-param",
                live_argument_attribute_segment(&parameter.name)
            );
            items.push(protocol::CompletionItem {
                label: attribute.clone(),
                kind: kind(protocol::CompletionItemKind::PROPERTY),
                detail: live_event_parameter_detail(parameter, listener),
                text_edit: Some(text_edit(edit_range, format!("{attribute}=\"$0\""))),
                insert_text_format: snippet(),
                ..Default::default()
            });
        }
    }
    sort_completion_items(&mut items);
    items
}

fn live_event_name_context_php(node: &SyntaxNode) -> bool {
    let Some(literal) = phpquery::string_at(node) else {
        return false;
    };
    let Some(call) = phpquery::call_at(&literal) else {
        return false;
    };
    if call.kind() != PHP_MEMBER_CALL {
        return false;
    }
    let Some(receiver) = phpquery::call_receiver(&call) else {
        return false;
    };
    if !eq_ignore_case(receiver.text().to_string().trim(), "$this") {
        return false;
    }
    match lowercase(&phpquery::call_method_name(&call)).as_str() {
        "emit" | "emitup" | "emitself" => {}
        _ => return false,
    }
    let Some(index) = phpquery::argument_index(&call, &literal) else {
        return false;
    };
    let name = phpquery::argument(&call, index)
        .map(|argument| phpquery::argument_name(&argument))
        .unwrap_or_default();
    (index == 0 && name.is_empty())
        || eq_ignore_case(&name, "event")
        || eq_ignore_case(&name, "eventName")
}

fn live_event_name_completion_context(source: &str, offset: u32) -> bool {
    let before = source_before(source, offset);
    let Some(open) = before.rfind('<') else {
        return false;
    };
    if before.rfind('>').is_some_and(|close| close > open) {
        return false;
    }
    let fragment = &before[open..];
    LIVE_EVENT_ACTION.is_match(fragment) && LIVE_EVENT_VALUE_COMPLETION.is_match(fragment)
}

fn live_event_argument_completion_context(
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
    if !lowercase(current).starts_with("data-live-")
        || current.contains('=')
        || !LIVE_EVENT_ACTION.is_match(fragment)
    {
        return None;
    }
    let captures = LIVE_EVENT_VALUE.captures(fragment)?;
    let mut event = captures.get(1)?.as_str().trim();
    if let Some(separator) = event.rfind('|') {
        event = event[separator + 1..].trim();
    }
    if event.is_empty() {
        return None;
    }
    let mut present = HashSet::new();
    for captures in LIVE_ARGUMENT_ATTRIBUTE.captures_iter(fragment) {
        let Some(name) = captures.get(1) else {
            continue;
        };
        if eq_ignore_case(name.as_str(), "action") || eq_ignore_case(name.as_str(), "event") {
            continue;
        }
        present.insert(lowercase(&live_argument_name(name.as_str())));
    }
    Some(LiveArgumentContext {
        name: event.to_string(),
        present,
        start: (open + current_start) as u32,
        end: offset,
    })
}

fn live_event_parameter_detail(parameter: &LiveActionParameter, listener: &LiveListener) -> String {
    let mut detail = format!(
        "{} ${} • {}::{}()",
        parameter.ty, parameter.php_name, listener.class, listener.method
    );
    if parameter.name != parameter.php_name {
        detail.push_str(&format!(" • LiveArg({})", parameter.name));
    }
    detail.trim().to_string()
}

fn live_listener_signature(listener: &LiveListener) -> String {
    let parameters: Vec<String> = listener
        .parameters
        .iter()
        .map(|parameter| {
            let mut name = format!("${}", parameter.php_name);
            if !parameter.ty.is_empty() {
                name = format!("{} {name}", parameter.ty);
            }
            if parameter.optional {
                name.push('?');
            }
            name
        })
        .collect();
    format!(
        "{}::{}({})",
        listener.class,
        listener.method,
        parameters.join(", ")
    )
}

fn live_event_listener_count(count: usize) -> String {
    if count == 1 {
        "1 listener".to_string()
    } else {
        format!("{count} listeners")
    }
}
