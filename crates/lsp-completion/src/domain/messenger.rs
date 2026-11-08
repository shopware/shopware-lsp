//! Messenger message names and handler methods (Go
//! `messenger_completion.go`).

use std::sync::Arc;

use swlsp_lsp::{CompletionProvider, CompletionRequest, Context};
use swlsp_lsp_protocol::{self as protocol, CompletionItemKind};
use swlsp_messenger::{Index, ReferenceRole};
use swlsp_php::PhpIndex;

use super::{item, strings};
use crate::common::php_context;

pub struct MessengerCompletionProvider {
    php: Option<PhpIndex>,
    index: Option<Arc<Index>>,
}

impl MessengerCompletionProvider {
    pub fn new(php: Option<PhpIndex>, index: Option<Arc<Index>>) -> Self {
        Self { php, index }
    }
}

impl CompletionProvider for MessengerCompletionProvider {
    fn get_completions(
        &self,
        _ctx: &Context,
        request: &CompletionRequest,
    ) -> Vec<protocol::CompletionItem> {
        let (Some(node), Some(root)) = (request.node.as_ref(), request.root.as_ref()) else {
            return Vec::new();
        };
        let context = self.php.as_ref().and_then(|php| php_context(php, request));
        let Some(reference) = swlsp_messenger::reference_at(
            context.as_ref(),
            &request.params.text_document.uri,
            root,
            node,
        ) else {
            return Vec::new();
        };
        match reference.role {
            ReferenceRole::Message => {
                let Some(index) = self.index.as_ref() else {
                    return Vec::new();
                };
                let Ok(names) = index.message_names() else {
                    return Vec::new();
                };
                names
                    .into_iter()
                    .map(|name| item(name, CompletionItemKind::CLASS, "Symfony Messenger message"))
                    .collect()
            }
            ReferenceRole::HandlerMethod => {
                swlsp_messenger::public_handler_methods(self.php.as_ref(), &reference.class)
                    .into_iter()
                    .map(|method| {
                        let detail = if method.doc_summary().is_empty() {
                            reference.class.clone()
                        } else {
                            method.doc_summary().to_string()
                        };
                        item(method.name.to_string(), CompletionItemKind::METHOD, detail)
                    })
                    .collect()
            }
            ReferenceRole::None => Vec::new(),
        }
    }

    fn trigger_characters(&self) -> Vec<String> {
        strings(&["'", "\""])
    }
}
