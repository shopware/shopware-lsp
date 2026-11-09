//! Console command and input names.

use std::collections::HashSet;
use std::sync::Arc;

use swlsp_console::{Index, ReferenceRole};
use swlsp_lsp::{CompletionProvider, CompletionRequest, Context};
use swlsp_lsp_protocol::{self as protocol, CompletionItemKind};
use swlsp_php::PhpIndex;

use super::{item, lower_ext, strings};
use crate::common::php_context;

/// Completes console command and input names; the PHP analysis of a
/// request is built from `php`.
pub struct ConsoleCompletionProvider {
    index: Arc<Index>,
    php: Option<PhpIndex>,
}

impl ConsoleCompletionProvider {
    pub fn new(index: Arc<Index>, php: Option<PhpIndex>) -> Self {
        Self { index, php }
    }
}

impl CompletionProvider for ConsoleCompletionProvider {
    fn get_completions(
        &self,
        _ctx: &Context,
        request: &CompletionRequest,
    ) -> Vec<protocol::CompletionItem> {
        let Some(node) = request.node.as_ref() else {
            return Vec::new();
        };
        if lower_ext(&request.params.text_document.uri) != ".php" {
            return Vec::new();
        }
        let Some(reference) = swlsp_console::reference_at(node) else {
            return Vec::new();
        };
        let context = self.php.as_ref().and_then(|php| php_context(php, request));
        if !swlsp_console::validate_reference(context.as_ref(), &reference) {
            return Vec::new();
        }
        if reference.role == ReferenceRole::Command {
            let Ok(commands) = self.index.get_commands() else {
                return Vec::new();
            };
            let mut seen = HashSet::with_capacity(commands.len());
            let mut items = Vec::with_capacity(commands.len());
            for command in commands {
                if !seen.insert(command.name.clone()) {
                    continue;
                }
                let detail = if command.description.is_empty() {
                    command.class
                } else {
                    command.description
                };
                items.push(item(command.name, CompletionItemKind::REFERENCE, detail));
            }
            return items;
        }
        let Ok(inputs) =
            swlsp_console::inputs_for_reference(context.as_ref(), &self.index, &reference)
        else {
            return Vec::new();
        };
        inputs
            .into_iter()
            .map(|input| {
                let mut detail = if input.description.is_empty() {
                    input.mode
                } else {
                    input.description
                };
                if !input.default.is_empty() {
                    if !detail.is_empty() {
                        detail.push_str(" · ");
                    }
                    detail.push_str("default ");
                    detail.push_str(&input.default);
                }
                item(input.name, CompletionItemKind::FIELD, detail)
            })
            .collect()
    }

    fn trigger_characters(&self) -> Vec<String> {
        strings(&["'", "\""])
    }
}
