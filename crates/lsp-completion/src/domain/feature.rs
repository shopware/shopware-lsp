//! Feature flag names.

use std::sync::Arc;

use swlsp_feature::FeatureIndexer;
use swlsp_lsp::{CompletionProvider, CompletionRequest, Context};
use swlsp_lsp_protocol::{self as protocol, CompletionItemKind};

pub struct FeatureCompletionProvider {
    index: Arc<FeatureIndexer>,
}

impl FeatureCompletionProvider {
    pub fn new(index: Arc<FeatureIndexer>) -> Self {
        Self { index }
    }
}

impl CompletionProvider for FeatureCompletionProvider {
    fn get_completions(
        &self,
        _ctx: &Context,
        request: &CompletionRequest,
    ) -> Vec<protocol::CompletionItem> {
        let Some(node) = request.node.as_ref() else {
            return Vec::new();
        };
        let matches = match swlsp_uriutil::filepath::ext(&request.params.text_document.uri) {
            ".twig" => swlsp_parser_twig::query::string_in_function(node, &["feature"]),
            ".scss" => swlsp_parser_scss::query::string_in_function(node, &["feature"]),
            ".php" => swlsp_parser_php::query::string_in_call(node, 0, &["Feature::isActive"]),
            ".js" | ".ts" => swlsp_parser_js::query::string_in_call(
                node,
                0,
                &["Feature.isActive", "Shopware.Feature.isActive"],
            ),
            _ => false,
        };
        if !matches {
            return Vec::new();
        }
        let features = self.index.all_features().unwrap_or_else(|_| Arc::from([]));
        features
            .iter()
            .map(|feature| protocol::CompletionItem {
                label: feature.name.clone(),
                kind: Some(CompletionItemKind::FUNCTION),
                ..Default::default()
            })
            .collect()
    }

    fn trigger_characters(&self) -> Vec<String> {
        Vec::new()
    }
}
