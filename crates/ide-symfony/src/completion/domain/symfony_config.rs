//! Symfony configuration root names.

use std::sync::Arc;

use swlsp_lsp::{CompletionProvider, CompletionRequest, Context};
use swlsp_lsp_protocol::{self as protocol, CompletionItemKind};
use swlsp_symfonyconfig::Index;

use super::{item, strings};
use crate::completion::common::sort_completion_items;

pub struct SymfonyConfigCompletionProvider {
    index: Arc<Index>,
}

impl SymfonyConfigCompletionProvider {
    pub fn new(index: Arc<Index>) -> Self {
        Self { index }
    }
}

impl CompletionProvider for SymfonyConfigCompletionProvider {
    fn get_completions(
        &self,
        _ctx: &Context,
        request: &CompletionRequest,
    ) -> Vec<protocol::CompletionItem> {
        let Some(node) = request.node.as_ref() else {
            return Vec::new();
        };
        if swlsp_symfonyconfig::root_reference_at(node).is_none() {
            return Vec::new();
        }
        let Ok(path) = swlsp_uriutil::path(&request.params.text_document.uri) else {
            return Vec::new();
        };
        if !swlsp_uriutil::filepath::to_slash(&path).contains("/config/") {
            return Vec::new();
        }
        let Ok(names) = self.index.names() else {
            return Vec::new();
        };
        let mut items: Vec<_> = names
            .into_iter()
            .map(|name| {
                let mut detail = String::from("Symfony configuration root");
                if let Ok(roots) = self.index.roots(&name)
                    && let Some(first) = roots.first()
                    && !first.class.is_empty()
                {
                    detail.push_str(" · ");
                    detail.push_str(&first.class);
                }
                item(name, CompletionItemKind::PROPERTY, detail)
            })
            .collect();
        sort_completion_items(&mut items);
        items
    }

    fn trigger_characters(&self) -> Vec<String> {
        strings(&["'", "\""])
    }
}
