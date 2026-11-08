//! Translation domain, key and placeholder completion in PHP and Twig (port
//! of Go `translation_completion.go`).

use std::collections::HashMap;
use std::sync::Arc;

use swlsp_lsp::{CompletionProvider, CompletionRequest, Context};
use swlsp_lsp_protocol as protocol;
use swlsp_php::{PhpIndex, assistant_argument_reference, assistant_sibling_string_argument};
use swlsp_syntax::TextRange;
use swlsp_translation::{Index, Message, ReferenceRole};

use super::{lower_ext, markdown};
use crate::common::{go_to_lower, php_context, protocol_range, text_edit, truncate_text};

use protocol::CompletionItemKind as Kind;

pub struct TranslationCompletionProvider {
    index: Option<Arc<Index>>,
    php_index: Option<PhpIndex>,
}

impl TranslationCompletionProvider {
    pub fn new(index: Option<Arc<Index>>, php_index: Option<PhpIndex>) -> Self {
        Self { index, php_index }
    }
}

impl CompletionProvider for TranslationCompletionProvider {
    fn get_completions(
        &self,
        _ctx: &Context,
        request: &CompletionRequest,
    ) -> Vec<protocol::CompletionItem> {
        let (Some(index), Some(node)) = (self.index.as_deref(), request.node.as_ref()) else {
            return Vec::new();
        };
        let extension = lower_ext(&request.params.text_document.uri);
        if extension != ".php" && extension != ".twig" {
            return Vec::new();
        }
        let context = if extension == ".php" {
            self.php_index
                .as_ref()
                .and_then(|php| php_context(php, &request.syntax))
        } else {
            None
        };
        if let Some(context) = context.as_ref() {
            if let Some(reference) =
                assistant_argument_reference(context, node, "TranslationDomain")
            {
                return assistant_edits(domain_completions(index), reference, request);
            }
            if let Some(reference) = assistant_argument_reference(context, node, "TranslationKey") {
                let domain = assistant_sibling_string_argument(context, node, "TranslationDomain")
                    .unwrap_or_else(|| "messages".to_string());
                return assistant_edits(key_completions(index, &domain), reference, request);
            }
        }
        let Some(reference) = swlsp_translation::reference_at(
            &request.params.text_document.uri,
            node,
            request.source(),
        ) else {
            return Vec::new();
        };
        if extension == ".php"
            && !swlsp_translation::validate_php_reference(
                context.as_ref(),
                &reference,
                self.php_index.as_ref(),
            )
        {
            return Vec::new();
        }
        match reference.role {
            ReferenceRole::Domain => domain_completions(index),
            ReferenceRole::Key => key_completions(index, &reference.domain),
            ReferenceRole::Placeholder => {
                placeholder_completions(index, &reference.domain, &reference.key)
            }
            ReferenceRole::None => Vec::new(),
        }
    }

    fn trigger_characters(&self) -> Vec<String> {
        Vec::new()
    }
}

fn assistant_edits(
    mut items: Vec<protocol::CompletionItem>,
    reference: TextRange,
    request: &CompletionRequest,
) -> Vec<protocol::CompletionItem> {
    let Some(line_index) = request.line_index() else {
        return items;
    };
    let replacement = protocol_range(reference, line_index);
    for item in &mut items {
        item.text_edit = Some(text_edit(replacement, item.label.clone()));
    }
    items
}

fn placeholder_completions(
    index: &Index,
    domain: &str,
    key: &str,
) -> Vec<protocol::CompletionItem> {
    let Ok(placeholders) = index.placeholders(domain, key) else {
        return Vec::new();
    };
    placeholders
        .into_iter()
        .map(|placeholder| protocol::CompletionItem {
            label: placeholder,
            kind: Some(Kind::VARIABLE),
            detail: "Translation placeholder".to_string(),
            ..Default::default()
        })
        .collect()
}

fn domain_completions(index: &Index) -> Vec<protocol::CompletionItem> {
    let Ok(domains) = index.domains() else {
        return Vec::new();
    };
    domains
        .into_iter()
        .map(|domain| {
            let keys = index.keys(&domain).map(|keys| keys.len()).unwrap_or(0);
            protocol::CompletionItem {
                label: domain,
                kind: Some(Kind::MODULE),
                detail: translation_count_label(keys),
                ..Default::default()
            }
        })
        .collect()
}

fn key_completions(index: &Index, domain: &str) -> Vec<protocol::CompletionItem> {
    let Ok(messages) = index.domain_messages(domain) else {
        return Vec::new();
    };
    let mut preferred: HashMap<&str, &Message> = HashMap::new();
    for message in &messages {
        match preferred.get(message.key.as_str()) {
            Some(current) if message_priority(message) <= message_priority(current) => {}
            _ => {
                preferred.insert(&message.key, message);
            }
        }
    }
    let mut keys: Vec<&str> = preferred.keys().copied().collect();
    keys.sort_unstable();
    keys.into_iter()
        .map(|key| {
            let message = preferred[key];
            let mut item = protocol::CompletionItem {
                label: key.to_string(),
                kind: Some(Kind::VALUE),
                detail: truncate_text(&message.text, 70),
                ..Default::default()
            };
            if !message.text.is_empty() {
                item.documentation = markdown(format!("**{}**: {}", message.locale, message.text));
            }
            item
        })
        .collect()
}

fn message_priority(message: &Message) -> i32 {
    let mut priority = 0;
    let locale = go_to_lower(&message.locale.replace('_', "-"));
    if locale == "en" || locale.starts_with("en-") {
        priority += 2;
    }
    let normalized = go_to_lower(&swlsp_uriutil::filepath::to_slash(&message.file));
    if !normalized.contains("/var/cache/") && !normalized.contains("/app/cache/") {
        priority += 1;
    }
    priority
}

fn translation_count_label(count: usize) -> String {
    if count == 1 {
        return "1 translation key".to_string();
    }
    format!("{count} translation keys")
}
