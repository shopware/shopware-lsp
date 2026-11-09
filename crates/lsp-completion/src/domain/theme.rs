//! Theme configuration keys.

use std::collections::HashSet;
use std::sync::Arc;

use swlsp_lsp::{CompletionProvider, CompletionRequest, Context};
use swlsp_lsp_protocol as protocol;
use swlsp_theme::ThemeConfigIndexer;

use super::lower_ext;

pub struct ThemeCompletionProvider {
    index: Arc<ThemeConfigIndexer>,
}

impl ThemeCompletionProvider {
    pub fn new(index: Arc<ThemeConfigIndexer>) -> Self {
        Self { index }
    }
}

fn label_item(label: String) -> protocol::CompletionItem {
    protocol::CompletionItem {
        label,
        ..Default::default()
    }
}

impl CompletionProvider for ThemeCompletionProvider {
    fn get_completions(
        &self,
        _ctx: &Context,
        request: &CompletionRequest,
    ) -> Vec<protocol::CompletionItem> {
        let Some(node) = request.node.as_ref() else {
            return Vec::new();
        };
        match lower_ext(&request.params.text_document.uri).as_str() {
            ".scss" => {
                let Ok(fields) = self.index.all_theme_config_fields() else {
                    return Vec::new();
                };
                let mut seen = HashSet::new();
                fields
                    .iter()
                    .filter(|field| field.scss && seen.insert(field.key.as_str()))
                    .map(|field| label_item(format!("${}", field.key)))
                    .collect()
            }
            ".twig" => {
                if !swlsp_parser_twig::query::string_in_function(node, &["theme_config"]) {
                    return Vec::new();
                }
                let Ok(themes) = self.index.theme_config_fields() else {
                    return Vec::new();
                };
                let mut seen = HashSet::new();
                themes
                    .iter()
                    .filter(|theme| seen.insert(theme.as_str()))
                    .map(|theme| label_item(theme.clone()))
                    .collect()
            }
            _ => Vec::new(),
        }
    }

    fn trigger_characters(&self) -> Vec<String> {
        Vec::new()
    }
}
