//! Symfony environment variable names (Go `environment_completion.go`).

use std::sync::Arc;

use swlsp_environment::Index;
use swlsp_lsp::{CompletionProvider, CompletionRequest, Context};
use swlsp_lsp_protocol::{self as protocol, CompletionItemKind};

use super::{item, strings};
use crate::common::{protocol_range, text_edit};

pub struct EnvironmentCompletionProvider {
    index: Arc<Index>,
}

impl EnvironmentCompletionProvider {
    pub fn new(index: Arc<Index>) -> Self {
        Self { index }
    }
}

impl CompletionProvider for EnvironmentCompletionProvider {
    fn get_completions(
        &self,
        _ctx: &Context,
        request: &CompletionRequest,
    ) -> Vec<protocol::CompletionItem> {
        let Some(line_index) = request.line_index() else {
            return Vec::new();
        };
        let position = request.params.position;
        let offset = line_index.offset_utf16(position.line, position.character);
        let reference = request
            .node
            .as_ref()
            .and_then(|node| swlsp_environment::php_completion_reference_at(node, offset))
            .or_else(|| swlsp_environment::completion_reference_at(request.source(), offset));
        let Some(reference) = reference else {
            return Vec::new();
        };
        let Ok(variables) = self.index.variables() else {
            return Vec::new();
        };
        let range = protocol_range(reference.name_range, line_index);
        variables
            .into_iter()
            .filter(|variable| !variable.declarations.is_empty())
            .map(|variable| {
                let mut completion = item(
                    variable.name.clone(),
                    CompletionItemKind::VARIABLE,
                    format!(
                        "Symfony environment variable · {} declaration(s)",
                        variable.declarations.len()
                    ),
                );
                completion.text_edit = Some(text_edit(range, variable.name));
                completion
            })
            .collect()
    }

    fn trigger_characters(&self) -> Vec<String> {
        strings(&["(", ":"])
    }
}
