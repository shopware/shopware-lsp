//! Go `twig_constant_completion.go`: PHP constants inside `constant()`.

use std::collections::HashSet;
use std::sync::Arc;

use swlsp_lsp::{CompletionProvider, CompletionRequest, Context};
use swlsp_lsp_protocol::{self as protocol, CompletionItemKind};
use swlsp_php::PhpIndex;
use swlsp_php_semantic::{Flags, Symbol, SymbolKind, Visibility};
use swlsp_twig::{ConstantCompletionContext, PhpAccessResolver, TwigIndexer};

use super::{lower_ext, markdown};
use crate::common::{compare_completion_items, go_to_lower, protocol_range, text_edit};

pub struct TwigConstantCompletionProvider {
    php_index: Option<PhpIndex>,
    twig_index: Option<Arc<TwigIndexer>>,
}

impl TwigConstantCompletionProvider {
    pub fn new(php_index: Option<PhpIndex>, twig_index: Option<Arc<TwigIndexer>>) -> Self {
        Self {
            php_index,
            twig_index,
        }
    }

    fn member_items(
        &self,
        php: &PhpIndex,
        classes: &[String],
        partial: &str,
        completion: &ConstantCompletionContext,
        object_relative: bool,
        request: &CompletionRequest,
    ) -> Vec<protocol::CompletionItem> {
        let partial = go_to_lower(partial);
        let mut items = Vec::new();
        let mut seen = HashSet::new();
        for class in classes {
            for symbol in php.constants(class) {
                if symbol.visibility != Visibility::Public
                    || !go_to_lower(&symbol.name).starts_with(&partial)
                {
                    continue;
                }
                let key = format!("{}\0{}", go_to_lower(class), go_to_lower(&symbol.name));
                if !seen.insert(key) {
                    continue;
                }
                let insert = if object_relative {
                    symbol.name.to_string()
                } else {
                    format!(
                        "{}::{}",
                        swlsp_twig::escape_twig_class_name(class),
                        symbol.name
                    )
                };
                items.push(twig_constant_completion_item(
                    &symbol.name,
                    &format!("{class}::{}", symbol.name),
                    insert,
                    &symbol,
                    completion,
                    request,
                ));
            }
        }
        sort_twig_constant_items(&mut items);
        items
    }

    fn all_items(
        &self,
        php: &PhpIndex,
        completion: &ConstantCompletionContext,
        request: &CompletionRequest,
    ) -> Vec<protocol::CompletionItem> {
        let prefix = go_to_lower(
            completion
                .value
                .strip_prefix('\\')
                .unwrap_or(&completion.value),
        );
        let mut items = Vec::new();
        let mut seen = HashSet::new();
        for symbol in php.constant_symbols() {
            if symbol.kind != SymbolKind::GlobalConstant && symbol.visibility != Visibility::Public
            {
                continue;
            }
            let display = php.constant_symbol_name(&symbol);
            let lower = go_to_lower(&display);
            if matches!(lower.as_str(), "true" | "false" | "null") || !lower.starts_with(&prefix) {
                continue;
            }
            if !seen.insert(display.clone()) {
                continue;
            }
            items.push(twig_constant_completion_item(
                &twig_constant_short_label(&display),
                &display,
                swlsp_twig::escape_twig_class_name(&display),
                &symbol,
                completion,
                request,
            ));
        }
        sort_twig_constant_items(&mut items);
        items
    }
}

impl CompletionProvider for TwigConstantCompletionProvider {
    fn get_completions(
        &self,
        _ctx: &Context,
        request: &CompletionRequest,
    ) -> Vec<protocol::CompletionItem> {
        let Some(php) = &self.php_index else {
            return Vec::new();
        };
        let (Some(node), Some(root)) = (request.node.as_ref(), request.root.as_ref()) else {
            return Vec::new();
        };
        if request.line_index().is_none() || lower_ext(&request.params.text_document.uri) != ".twig"
        {
            return Vec::new();
        }
        let Ok(path) = swlsp_uriutil::path(&request.params.text_document.uri) else {
            return Vec::new();
        };
        let resolver = PhpAccessResolver::new(Some(php), self.twig_index.as_deref());
        let Some(completion) =
            swlsp_twig::constant_completion_context_at(&path, root, node, &resolver)
        else {
            return Vec::new();
        };
        if !completion.class.is_empty() {
            let partial = match completion.value.rfind("::") {
                Some(separator) => &completion.value[separator + 2..],
                None => completion.value.as_str(),
            };
            return self.member_items(
                php,
                std::slice::from_ref(&completion.class),
                partial,
                &completion,
                false,
                request,
            );
        }
        if completion.object_argument {
            if completion.receiver_classes.is_empty() {
                return Vec::new();
            }
            return self.member_items(
                php,
                &completion.receiver_classes,
                &completion.value,
                &completion,
                true,
                request,
            );
        }
        self.all_items(php, &completion, request)
    }

    fn trigger_characters(&self) -> Vec<String> {
        vec!["\\".into(), ":".into()]
    }
}

fn twig_constant_completion_item(
    label: &str,
    detail: &str,
    insert: String,
    symbol: &Symbol,
    completion: &ConstantCompletionContext,
    request: &CompletionRequest,
) -> protocol::CompletionItem {
    let kind = if symbol.kind == SymbolKind::EnumCase {
        CompletionItemKind::ENUM_MEMBER
    } else {
        CompletionItemKind::CONSTANT
    };
    let range = request
        .line_index()
        .map(|line_index| protocol_range(completion.content_range, line_index))
        .unwrap_or_default();
    protocol::CompletionItem {
        label: label.to_string(),
        filter_text: format!("{detail} {insert}"),
        kind: Some(kind),
        detail: detail.to_string(),
        text_edit: Some(text_edit(range, insert)),
        documentation: if symbol.doc_summary().is_empty() {
            None
        } else {
            markdown(symbol.doc_summary())
        },
        deprecated: symbol.flags.has(Flags::DEPRECATED),
        ..Default::default()
    }
}

fn twig_constant_short_label(display: &str) -> String {
    if let Some(separator) = display.rfind("::") {
        let mut class = &display[..separator];
        if let Some(namespace) = class.rfind('\\') {
            class = &class[namespace + 1..];
        }
        return format!("{class}{}", &display[separator..]);
    }
    if let Some(namespace) = display.rfind('\\') {
        return display[namespace + 1..].to_string();
    }
    display.to_string()
}

fn sort_twig_constant_items(items: &mut Vec<protocol::CompletionItem>) {
    let mut keyed: Vec<(String, String, protocol::CompletionItem)> = std::mem::take(items)
        .into_iter()
        .map(|item| (go_to_lower(&item.label), go_to_lower(&item.detail), item))
        .collect();
    // Go compares with EqualFold first; full case folding and lowercase
    // equality agree for these identifiers.
    keyed.sort_unstable_by(|left, right| {
        left.0
            .cmp(&right.0)
            .then_with(|| left.1.cmp(&right.1))
            .then_with(|| compare_completion_items(&left.2, &right.2))
    });
    *items = keyed.into_iter().map(|(_, _, item)| item).collect();
}
