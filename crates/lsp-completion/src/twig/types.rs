//! Go `twig_types_completion.go`: PHP class names inside `{% types %}`.

use std::collections::HashSet;

use swlsp_lsp::CompletionRequest;
use swlsp_lsp_protocol::{self as protocol, CompletionItemKind};
use swlsp_php_semantic::{Flags, SymbolKind};

use super::completion::TwigCompletionProvider;
use super::{request_offset, sort_by_lower_label};
use crate::common::{go_to_lower, protocol_range, text_edit};

impl TwigCompletionProvider {
    pub(crate) fn twig_types_tag_completions(
        &self,
        request: &CompletionRequest,
    ) -> Option<Vec<protocol::CompletionItem>> {
        let line_index = request.line_index()?;
        let offset = request_offset(request)?;
        let completion = swlsp_twig::types_tag_completion_at(request.source().as_bytes(), offset)?;
        let Some(php) = &self.php_index else {
            return Some(Vec::new());
        };
        let range = protocol_range(completion.range, line_index);
        let prefix = go_to_lower(
            completion
                .prefix
                .strip_prefix('\\')
                .unwrap_or(&completion.prefix),
        );
        let mut seen = HashSet::new();
        let mut items = Vec::new();
        for symbol in php.class_symbols_view().iter() {
            let name = symbol
                .fully_qualified
                .strip_prefix('\\')
                .unwrap_or(&symbol.fully_qualified)
                .to_string();
            let key = go_to_lower(&name);
            if seen.contains(&key) || !prefix.is_empty() && !key.starts_with(&prefix) {
                continue;
            }
            seen.insert(key);
            let (kind, detail) = match symbol.kind {
                SymbolKind::Interface => (CompletionItemKind::INTERFACE, "PHP interface"),
                SymbolKind::Enum => (CompletionItemKind::ENUM, "PHP enum"),
                SymbolKind::Trait => (CompletionItemKind::CLASS, "PHP trait"),
                _ => (CompletionItemKind::CLASS, "PHP class"),
            };
            let escaped = swlsp_twig::escape_twig_class_name(&name);
            items.push(protocol::CompletionItem {
                filter_text: format!("{name} {escaped}"),
                kind: Some(kind),
                detail: format!("{detail} for Twig types tag"),
                text_edit: Some(text_edit(range, escaped)),
                deprecated: symbol.flags.has(Flags::DEPRECATED),
                label: name,
                ..Default::default()
            });
        }
        sort_by_lower_label(&mut items);
        Some(items)
    }
}
