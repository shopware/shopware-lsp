//! Console helper names.

use std::collections::HashSet;

use swlsp_console::HelperCatalog;
use swlsp_lsp::{CompletionProvider, CompletionRequest, Context};
use swlsp_lsp_protocol::{self as protocol, CompletionItemKind};
use swlsp_php::PhpIndex;

use super::{item, markdown, strings};
use crate::common::{php_context, protocol_range, text_edit};

pub struct ConsoleHelperCompletionProvider {
    php: Option<PhpIndex>,
    catalog: Option<HelperCatalog>,
}

impl ConsoleHelperCompletionProvider {
    pub fn new(php: Option<PhpIndex>) -> Self {
        Self {
            catalog: php.clone().map(HelperCatalog::new),
            php,
        }
    }
}

impl CompletionProvider for ConsoleHelperCompletionProvider {
    fn get_completions(
        &self,
        _ctx: &Context,
        request: &CompletionRequest,
    ) -> Vec<protocol::CompletionItem> {
        let (Some(php), Some(catalog), Some(node), Some(line_index)) = (
            self.php.as_ref(),
            self.catalog.as_ref(),
            request.node.as_ref(),
            request.line_index(),
        ) else {
            return Vec::new();
        };
        let Some(reference) = swlsp_console::helper_reference_at(node) else {
            return Vec::new();
        };
        let context = php_context(php, request);
        if !swlsp_console::validate_helper_reference(php, context.as_ref(), &reference) {
            return Vec::new();
        }
        let range = protocol_range(reference.range, line_index);
        let mut seen = HashSet::new();
        let mut result = Vec::new();
        for helper in catalog.helpers() {
            if !seen.insert(helper.name.clone()) {
                continue;
            }
            let mut completion = item(
                helper.name.clone(),
                CompletionItemKind::REFERENCE,
                helper.class,
            );
            completion.text_edit = Some(text_edit(range, helper.name));
            if !helper.summary.is_empty() {
                completion.documentation = Some(markdown(helper.summary));
            }
            result.push(completion);
        }
        result
    }

    fn trigger_characters(&self) -> Vec<String> {
        strings(&["'", "\""])
    }
}
