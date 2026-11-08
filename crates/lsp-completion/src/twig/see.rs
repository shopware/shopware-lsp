//! Go `twig_see_completion.go`: PHP symbols, methods and templates in
//! `@see` comments.

use std::collections::HashMap;

use swlsp_lsp::CompletionRequest;
use swlsp_lsp_protocol::{self as protocol, CompletionItemKind};
use swlsp_php_resolver::MemberResolver;
use swlsp_php_semantic::{Flags, SymbolKind, Visibility};
use swlsp_php_types::Type;
use swlsp_syntax::{TextRange, TextSize};
use swlsp_twig::SeeCompletionContext;

use super::completion::TwigCompletionProvider;
use super::{request_offset, sort_by_lower_label};
use crate::common::{go_to_lower, protocol_range, text_edit};

impl TwigCompletionProvider {
    pub(crate) fn twig_see_completions(
        &self,
        request: &CompletionRequest,
    ) -> Option<Vec<protocol::CompletionItem>> {
        let root = request.root.as_ref()?;
        let line_index = request.line_index()?;
        let offset = request_offset(request)?;
        let completion = swlsp_twig::see_completion_at(root, offset)?;
        if let Some((class_name, member_prefix)) = twig_see_method_prefix(&completion.prefix) {
            return Some(self.twig_see_method_completions(
                request,
                &completion,
                &class_name,
                &member_prefix,
            ));
        }
        let replace = protocol_range(completion.range, line_index);
        let prefix = go_to_lower(&completion.prefix);
        let mut items: HashMap<String, protocol::CompletionItem> = HashMap::new();
        if let Some(php) = &self.php_index
            && !prefix.contains(['@', '/', ':', '.'])
        {
            let class_prefix = prefix.trim_start_matches('\\');
            let leading_slash = completion.prefix.starts_with('\\');
            for symbol in php.class_symbols_view().iter() {
                let name = symbol.fully_qualified.trim_start_matches('\\').to_string();
                if !class_prefix.is_empty() && !go_to_lower(&name).starts_with(class_prefix) {
                    continue;
                }
                let insert = if leading_slash {
                    format!("\\{name}")
                } else {
                    name.clone()
                };
                let kind = match symbol.kind {
                    SymbolKind::Interface => CompletionItemKind::INTERFACE,
                    SymbolKind::Enum => CompletionItemKind::ENUM,
                    _ => CompletionItemKind::CLASS,
                };
                items.insert(
                    format!("php\0{}", go_to_lower(&name)),
                    protocol::CompletionItem {
                        label: name,
                        kind: Some(kind),
                        detail: "PHP symbol for Twig @see".to_string(),
                        deprecated: symbol.flags.has(Flags::DEPRECATED),
                        text_edit: Some(text_edit(replace, insert)),
                        ..Default::default()
                    },
                );
            }
        }
        if let Some(index) = &self.twig_indexer {
            for template in index.get_all_template_files().unwrap_or_default() {
                if !prefix.is_empty() && !go_to_lower(&template).starts_with(&prefix) {
                    continue;
                }
                items.insert(
                    format!("twig\0{}", go_to_lower(&template)),
                    protocol::CompletionItem {
                        label: template.clone(),
                        kind: Some(CompletionItemKind::FILE),
                        detail: "Twig template for @see".to_string(),
                        text_edit: Some(text_edit(replace, template)),
                        ..Default::default()
                    },
                );
            }
        }
        // Go iterates a map before sorting; visit keys in order so equal
        // lowercase labels stay deterministic.
        let mut keyed: Vec<(String, protocol::CompletionItem)> = items.into_iter().collect();
        keyed.sort_by(|left, right| left.0.cmp(&right.0));
        let mut result: Vec<protocol::CompletionItem> =
            keyed.into_iter().map(|(_, item)| item).collect();
        sort_by_lower_label(&mut result);
        Some(result)
    }

    fn twig_see_method_completions(
        &self,
        request: &CompletionRequest,
        completion: &SeeCompletionContext,
        class_name: &str,
        prefix: &str,
    ) -> Vec<protocol::CompletionItem> {
        let Some(php) = &self.php_index else {
            return Vec::new();
        };
        let Some(class) = php.find_class(class_name) else {
            return Vec::new();
        };
        let Some(line_index) = request.line_index() else {
            return Vec::new();
        };
        let delimiter = completion.prefix.rfind(':').map_or(0, |index| index + 1);
        let start = completion.range.start() + TextSize::from(delimiter as u32);
        let replace = protocol_range(
            TextRange::new(start, completion.range.end().max(start)),
            line_index,
        );
        let lower_prefix = go_to_lower(prefix);
        let snapshot = php.semantic_snapshot();
        let mut items = Vec::new();
        for member in
            MemberResolver::new(&snapshot).all(&Type::named(&class.fully_qualified, Vec::new()))
        {
            let symbol = &member.symbol;
            if symbol.kind != SymbolKind::Method
                || symbol.visibility != Visibility::Public
                || !prefix.is_empty() && !go_to_lower(&symbol.name).starts_with(&lower_prefix)
            {
                continue;
            }
            items.push(protocol::CompletionItem {
                label: symbol.name.to_string(),
                kind: Some(CompletionItemKind::METHOD),
                detail: format!("{} · Twig @see method", member.ty),
                deprecated: symbol.flags.has(Flags::DEPRECATED),
                text_edit: Some(text_edit(replace, symbol.name.to_string())),
                ..Default::default()
            });
        }
        sort_by_lower_label(&mut items);
        items
    }
}

fn twig_see_method_prefix(prefix: &str) -> Option<(String, String)> {
    let (delimiter, width) = match prefix.rfind("::") {
        Some(index) => (index, 2),
        None if prefix.matches(':').count() == 1 => (prefix.rfind(':')?, 1),
        None => return None,
    };
    let class_name = prefix[..delimiter].trim_start_matches('\\');
    if class_name.is_empty() {
        return None;
    }
    Some((
        class_name.to_string(),
        prefix[delimiter + width..].to_string(),
    ))
}
