//! Completion of custom tag names after `{%`.

use std::collections::HashMap;

use swlsp_lsp::CompletionRequest;
use swlsp_lsp_protocol::{self as protocol, CompletionItemKind, InsertTextFormat};

use super::completion::TwigCompletionProvider;
use super::{markdown, request_offset, sort_by_lower_label};
use swlsp_textutil::lowercase;

#[derive(Default)]
struct TwigTagCompletionStatus {
    all_deprecated: bool,
    message: String,
    class: String,
}

impl TwigCompletionProvider {
    pub(crate) fn twig_tag_completions(
        &self,
        request: &CompletionRequest,
    ) -> Option<Vec<protocol::CompletionItem>> {
        let index = self.twig_indexer.as_ref()?;
        let offset = request_offset(request)?;
        let prefix = twig_tag_completion_prefix(request.source().as_bytes(), offset)?;
        let tags = index.get_all_twig_tags().ok()?;
        let mut statuses: HashMap<String, TwigTagCompletionStatus> = HashMap::new();
        for tag in tags {
            let status =
                statuses
                    .entry(tag.name.clone())
                    .or_insert_with(|| TwigTagCompletionStatus {
                        all_deprecated: true,
                        ..Default::default()
                    });
            status.all_deprecated = status.all_deprecated && tag.deprecated;
            if status.message.is_empty() {
                status.message = tag.deprecation;
            }
            if status.class.is_empty() {
                status.class = tag.class;
            }
        }
        let lower_prefix = lowercase(&prefix);
        // Names are visited in byte order so case variants sharing a
        // lowercase label keep a stable order.
        let mut names: Vec<&String> = statuses.keys().collect();
        names.sort();
        let mut items = Vec::with_capacity(statuses.len() + 3);
        for name in names {
            let status = &statuses[name];
            if !prefix.is_empty() && !lowercase(name).starts_with(&lower_prefix) {
                continue;
            }
            let mut item = protocol::CompletionItem {
                label: name.clone(),
                insert_text: name.clone(),
                kind: Some(CompletionItemKind::KEYWORD),
                detail: "Twig custom tag".to_string(),
                documentation: markdown(format!("Provided by `{}`.", status.class)),
                ..Default::default()
            };
            if status.all_deprecated {
                item.deprecated = true;
                item.detail = "Deprecated Twig custom tag".to_string();
                let mut value = "**Deprecated Twig custom tag**".to_string();
                if !status.message.is_empty() {
                    value.push_str("\n\n");
                    value.push_str(&status.message);
                }
                if !status.class.is_empty() {
                    value.push_str(&format!("\n\nProvided by `{}`.", status.class));
                }
                item.documentation = markdown(value);
            }
            items.push(item);
        }
        for label in ["endtrans", "endtranschoice"] {
            if statuses.contains_key(label) || !label.starts_with(&lower_prefix) {
                continue;
            }
            items.push(protocol::CompletionItem {
                label: label.to_string(),
                insert_text: label.to_string(),
                kind: Some(CompletionItemKind::KEYWORD),
                detail: "Twig closing tag".to_string(),
                ..Default::default()
            });
        }
        if !statuses.contains_key("types") && "types".starts_with(&lower_prefix) {
            items.push(protocol::CompletionItem {
                label: "types".to_string(),
                insert_text: "types { ${1:variable}: '${2:Type}' }".to_string(),
                kind: Some(CompletionItemKind::SNIPPET),
                detail: "Twig type declaration tag".to_string(),
                insert_text_format: Some(InsertTextFormat::SNIPPET),
                ..Default::default()
            });
        }
        sort_by_lower_label(&mut items);
        Some(items)
    }
}

/// The partial tag name when the cursor ends
/// a tag name right after `{%`.
pub(crate) fn twig_tag_completion_prefix(content: &[u8], offset: u32) -> Option<String> {
    let offset = offset as usize;
    if offset > content.len() {
        return None;
    }
    let before = &content[..offset];
    let open = rfind(before, b"{%")?;
    if rfind(before, b"%}").is_some_and(|close| close > open) {
        return None;
    }
    let mut cursor = open + 2;
    if cursor < before.len() && before[cursor] == b'-' {
        cursor += 1;
    }
    while cursor < before.len() && twig_tag_completion_space(before[cursor]) {
        cursor += 1;
    }
    let start = cursor;
    while cursor < before.len() && twig_tag_completion_name_byte(before[cursor]) {
        cursor += 1;
    }
    if cursor != before.len() {
        return None;
    }
    let mut probe = before.to_vec();
    probe.push(b'x');
    for usage in swlsp_twig::twig_tag_usages(&probe) {
        if u32::from(usage.range.start()) as usize == start
            && u32::from(usage.range.end()) as usize == probe.len()
        {
            return Some(String::from_utf8_lossy(&before[start..cursor]).into_owned());
        }
    }
    None
}

fn rfind(haystack: &[u8], needle: &[u8]) -> Option<usize> {
    haystack
        .windows(needle.len())
        .rposition(|window| window == needle)
}

fn twig_tag_completion_space(value: u8) -> bool {
    matches!(value, b' ' | b'\t' | b'\r' | b'\n')
}

fn twig_tag_completion_name_byte(value: u8) -> bool {
    value == b'_' || value == b'-' || value.is_ascii_alphanumeric() || value >= 0x80
}
