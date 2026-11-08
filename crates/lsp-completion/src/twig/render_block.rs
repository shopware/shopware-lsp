//! Go `twig_render_block_completion.go`: block names in
//! `renderBlock()`/`renderBlockView()` calls.

use std::collections::HashMap;
use std::sync::Arc;

use swlsp_lsp::{CompletionProvider, CompletionRequest, Context};
use swlsp_lsp_protocol::{self as protocol, CompletionItemKind};
use swlsp_php::PhpIndex;
use swlsp_twig::{TemplateBlock, TwigIndexer};

use super::{lower_ext, markdown, request_offset, sort_by_lower_label};
use crate::common::{go_to_lower, php_context};

pub struct TwigRenderBlockCompletionProvider {
    twig_index: Option<Arc<TwigIndexer>>,
    php_index: Option<PhpIndex>,
}

impl TwigRenderBlockCompletionProvider {
    pub fn new(twig_index: Option<Arc<TwigIndexer>>, php_index: Option<PhpIndex>) -> Self {
        Self {
            twig_index,
            php_index,
        }
    }
}

impl CompletionProvider for TwigRenderBlockCompletionProvider {
    fn get_completions(
        &self,
        _ctx: &Context,
        request: &CompletionRequest,
    ) -> Vec<protocol::CompletionItem> {
        let (Some(twig_index), Some(php)) = (&self.twig_index, &self.php_index) else {
            return Vec::new();
        };
        let Some(root) = request.root.as_ref() else {
            return Vec::new();
        };
        if lower_ext(&request.params.text_document.uri) != ".php" {
            return Vec::new();
        }
        let Some(offset) = request_offset(request) else {
            return Vec::new();
        };
        let Some(reference) = swlsp_twig::render_block_reference_at(root, offset) else {
            return Vec::new();
        };
        let context = php_context(php, &request.syntax);
        if !swlsp_twig::validate_render_block_reference(&reference, Some(php), context.as_ref()) {
            return Vec::new();
        }
        let Ok(blocks) = twig_index.get_template_blocks(&[reference.template.as_str()]) else {
            return Vec::new();
        };
        let mut by_name: HashMap<String, TemplateBlock> = HashMap::new();
        for block in blocks {
            by_name.entry(go_to_lower(&block.name)).or_insert(block);
        }
        let mut items: Vec<protocol::CompletionItem> = by_name
            .into_values()
            .map(|block| {
                let mut value = format!("Block reachable from `{}`.", reference.template);
                if !block.documentation.is_empty() {
                    value.push_str("\n\n");
                    value.push_str(&block.documentation);
                }
                protocol::CompletionItem {
                    label: block.name,
                    kind: Some(CompletionItemKind::REFERENCE),
                    detail: "Twig block".to_string(),
                    documentation: markdown(value),
                    ..Default::default()
                }
            })
            .collect();
        // Keys are lowercase labels, so the order is total.
        sort_by_lower_label(&mut items);
        items
    }

    fn trigger_characters(&self) -> Vec<String> {
        vec!["'".into(), "\"".into()]
    }
}
