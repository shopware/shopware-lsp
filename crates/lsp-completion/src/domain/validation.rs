//! Validator constraint option names (Go `validation_completion.go`).

use swlsp_lsp::{CompletionProvider, CompletionRequest, Context};
use swlsp_lsp_protocol::{self as protocol, CompletionItemKind};
use swlsp_php::PhpIndex;

use super::{item, strings};
use crate::common::php_context;

/// Go has no dependencies and reads the PHP analysis from the request
/// context; Rust builds it from `php`.
pub struct ValidationCompletionProvider {
    php: Option<PhpIndex>,
}

impl ValidationCompletionProvider {
    pub fn new(php: Option<PhpIndex>) -> Self {
        Self { php }
    }
}

impl CompletionProvider for ValidationCompletionProvider {
    fn get_completions(
        &self,
        _ctx: &Context,
        request: &CompletionRequest,
    ) -> Vec<protocol::CompletionItem> {
        let (Some(node), Some(root)) = (request.node.as_ref(), request.root.as_ref()) else {
            return Vec::new();
        };
        let context = self.php.as_ref().and_then(|php| php_context(php, request));
        let Some(reference) = swlsp_validation::option_reference_at(context.as_ref(), root, node)
        else {
            return Vec::new();
        };
        swlsp_validation::constraint_properties_in_context(context.as_ref(), &reference.constraint)
            .into_iter()
            .map(|property| {
                item(
                    property.name.to_string(),
                    CompletionItemKind::PROPERTY,
                    property.ty.to_string(),
                )
            })
            .collect()
    }

    fn trigger_characters(&self) -> Vec<String> {
        strings(&["'", "\""])
    }
}
