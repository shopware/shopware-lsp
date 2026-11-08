//! Storefront and Administration snippet key completion (port of Go
//! `snippet_completion.go`).
//!
//! Go iterates the snippet map, so its item order is random; Rust returns
//! the items sorted by key.

use std::collections::HashMap;
use std::sync::Arc;

use swlsp_lsp::{CompletionProvider, CompletionRequest, Context};
use swlsp_lsp_protocol as protocol;
use swlsp_parser_php::query as phpquery;
use swlsp_parser_twig::query as twigquery;
use swlsp_snippet::SnippetIndexer;

use super::{lower_ext, plaintext, request_offset};
use crate::common::truncate_text;

pub struct SnippetCompletionProvider {
    snippet_indexer: Option<Arc<SnippetIndexer>>,
}

impl SnippetCompletionProvider {
    pub fn new(snippet_indexer: Option<Arc<SnippetIndexer>>) -> Self {
        Self { snippet_indexer }
    }

    fn twig_completion(&self, request: &CompletionRequest) -> Vec<protocol::CompletionItem> {
        let Some(node) = request.node.as_ref() else {
            return Vec::new();
        };
        if let Some(root) = request.root.as_ref()
            && let Some(offset) = request_offset(request)
            && swlsp_snippet::admin_twig_reference_at_offset(root, offset).is_some()
        {
            return self.admin_snippet_completions();
        }
        if twigquery::string_in_filter(node, &["trans"]) {
            return self.frontend_snippet_completions();
        }
        if twigquery::string_in_function(node, &["$tc", "$t"]) {
            return self.admin_snippet_completions();
        }
        Vec::new()
    }

    fn frontend_snippet_completions(&self) -> Vec<protocol::CompletionItem> {
        let snippets = self
            .snippet_indexer
            .as_ref()
            .and_then(|index| index.frontend_snippets_with_text().ok())
            .unwrap_or_default();
        snippet_items(snippets)
    }

    fn admin_snippet_completions(&self) -> Vec<protocol::CompletionItem> {
        let snippets = self
            .snippet_indexer
            .as_ref()
            .and_then(|index| index.admin_snippets_with_text().ok())
            .unwrap_or_default();
        snippet_items(snippets)
    }
}

fn snippet_items(snippets: HashMap<String, String>) -> Vec<protocol::CompletionItem> {
    let mut snippets: Vec<(String, String)> = snippets.into_iter().collect();
    snippets.sort_unstable_by(|left, right| left.0.cmp(&right.0));
    snippets
        .into_iter()
        .map(|(key, text)| {
            let mut item = protocol::CompletionItem {
                label: key,
                detail: truncate_text(&text, 50),
                kind: Some(protocol::CompletionItemKind::TEXT),
                ..Default::default()
            };
            if !text.is_empty() {
                item.documentation = plaintext(text);
            }
            item
        })
        .collect()
}

impl CompletionProvider for SnippetCompletionProvider {
    fn get_completions(
        &self,
        _ctx: &Context,
        request: &CompletionRequest,
    ) -> Vec<protocol::CompletionItem> {
        let Some(node) = request.node.as_ref() else {
            return Vec::new();
        };
        match lower_ext(&request.params.text_document.uri).as_str() {
            ".twig" => self.twig_completion(request),
            ".php" => {
                if phpquery::string_in_call(node, 0, &["trans"]) {
                    self.frontend_snippet_completions()
                } else {
                    Vec::new()
                }
            }
            ".js" | ".ts" => {
                if swlsp_snippet::admin_javascript_string_reference(node) {
                    self.admin_snippet_completions()
                } else {
                    Vec::new()
                }
            }
            _ => Vec::new(),
        }
    }

    fn trigger_characters(&self) -> Vec<String> {
        Vec::new()
    }
}
