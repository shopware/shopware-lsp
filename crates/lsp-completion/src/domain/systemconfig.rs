//! System configuration keys and domains (Go `systemconfig_completion.go`).

use std::sync::Arc;

use swlsp_lsp::{CompletionProvider, CompletionRequest, Context};
use swlsp_lsp_protocol as protocol;
use swlsp_php::PhpIndex;
use swlsp_syntax::SyntaxNode;
use swlsp_systemconfig::SystemConfigIndexer;

use crate::common::php_context;

const SYSTEM_CONFIG_SERVICE: &str = "Shopware\\Core\\System\\SystemConfig\\SystemConfigService";

pub struct SystemConfigCompletionProvider {
    index: Arc<SystemConfigIndexer>,
    php: Option<PhpIndex>,
}

impl SystemConfigCompletionProvider {
    pub fn new(index: Arc<SystemConfigIndexer>, php: Option<PhpIndex>) -> Self {
        Self { index, php }
    }

    fn key_items(&self) -> Vec<protocol::CompletionItem> {
        let Ok(entries) = self.index.all_system_config_entries() else {
            return Vec::new();
        };
        entries
            .iter()
            .map(|entry| label_item(entry.name.clone()))
            .collect()
    }

    fn php_completion(
        &self,
        request: &CompletionRequest,
        node: &SyntaxNode,
    ) -> Vec<protocol::CompletionItem> {
        let Some(php) = self.php.as_ref() else {
            return Vec::new();
        };
        let context = php_context(php, request);
        if !php.is_method_called_on_class(context.as_ref(), node, SYSTEM_CONFIG_SERVICE) {
            return Vec::new();
        }
        if php.is_method_called_name(
            node,
            &["get", "getInt", "getString", "getFloat", "getBool", "set"],
        ) {
            return self.key_items();
        }
        if php.is_method_called_name(node, &["getDomain"]) {
            let Ok(entries) = self.index.all_system_config_entries() else {
                return Vec::new();
            };
            let mut domains: Vec<&str> = Vec::new();
            for entry in entries.iter() {
                if !domains.contains(&entry.namespace.as_str()) {
                    domains.push(&entry.namespace);
                }
            }
            return domains
                .into_iter()
                .map(|domain| label_item(domain.to_string()))
                .collect();
        }
        Vec::new()
    }
}

fn label_item(label: String) -> protocol::CompletionItem {
    protocol::CompletionItem {
        label,
        ..Default::default()
    }
}

impl CompletionProvider for SystemConfigCompletionProvider {
    fn get_completions(
        &self,
        _ctx: &Context,
        request: &CompletionRequest,
    ) -> Vec<protocol::CompletionItem> {
        let extension = swlsp_uriutil::filepath::ext(&request.params.text_document.uri);
        let Some(node) = request.node.as_ref() else {
            return Vec::new();
        };
        if extension == ".twig" && swlsp_parser_twig::query::string_in_function(node, &["config"]) {
            return self.key_items();
        }
        if extension == ".php" {
            return self.php_completion(request, node);
        }
        Vec::new()
    }

    fn trigger_characters(&self) -> Vec<String> {
        Vec::new()
    }
}
