//! Completion of PHP enum names inside `enum()`.

use swlsp_lsp::{CompletionProvider, CompletionRequest, Context};
use swlsp_lsp_protocol::{self as protocol, CompletionItemKind};
use swlsp_php::PhpIndex;
use swlsp_php_semantic::SymbolKind;

use super::{lower_ext, sort_by_lower_label};

pub struct TwigEnumCompletionProvider {
    php_index: Option<PhpIndex>,
}

impl TwigEnumCompletionProvider {
    pub fn new(php_index: Option<PhpIndex>) -> Self {
        Self { php_index }
    }
}

impl CompletionProvider for TwigEnumCompletionProvider {
    fn get_completions(
        &self,
        _ctx: &Context,
        request: &CompletionRequest,
    ) -> Vec<protocol::CompletionItem> {
        let Some(php) = &self.php_index else {
            return Vec::new();
        };
        let Some(node) = request.node.as_ref() else {
            return Vec::new();
        };
        if lower_ext(&request.params.text_document.uri) != ".twig"
            || !swlsp_twig::enum_completion_at(node)
        {
            return Vec::new();
        }
        let mut items: Vec<protocol::CompletionItem> = php
            .class_symbols_view()
            .iter()
            .filter(|symbol| symbol.kind == SymbolKind::Enum)
            .map(|symbol| {
                let name = symbol
                    .fully_qualified
                    .strip_prefix('\\')
                    .unwrap_or(&symbol.fully_qualified)
                    .to_string();
                let escaped = swlsp_twig::escape_twig_class_name(&name);
                protocol::CompletionItem {
                    filter_text: format!("{name} {escaped}"),
                    insert_text: escaped,
                    kind: Some(CompletionItemKind::ENUM),
                    detail: "PHP enum".to_string(),
                    label: name,
                    ..Default::default()
                }
            })
            .collect();
        sort_by_lower_label(&mut items);
        items
    }

    fn trigger_characters(&self) -> Vec<String> {
        vec!["\\".into()]
    }
}
