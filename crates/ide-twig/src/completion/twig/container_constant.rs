//! Completion of `!php/const` and `!php/enum` values in Symfony YAML service configuration.

use std::collections::HashSet;

use swlsp_lsp::{CompletionProvider, CompletionRequest, Context};
use swlsp_lsp_protocol::{self as protocol, CompletionItemKind};
use swlsp_php::PhpIndex;
use swlsp_php_semantic::{Flags, Symbol, SymbolKind, Visibility};
use swlsp_symfony::ContainerValueKind;
use swlsp_syntax::{LineIndex, TextRange, TextSize};

use super::{markdown, request_offset, sort_by_lower_label};
use crate::completion::common::{is_yaml_file, protocol_range, text_edit};
use swlsp_textutil::lowercase;

pub struct ContainerConstantCompletionProvider {
    php_index: Option<PhpIndex>,
}

impl ContainerConstantCompletionProvider {
    pub fn new(php_index: Option<PhpIndex>) -> Self {
        Self { php_index }
    }
}

impl CompletionProvider for ContainerConstantCompletionProvider {
    fn get_completions(
        &self,
        _ctx: &Context,
        request: &CompletionRequest,
    ) -> Vec<protocol::CompletionItem> {
        let Some(php) = &self.php_index else {
            return Vec::new();
        };
        let Some(line_index) = request.line_index() else {
            return Vec::new();
        };
        if !is_yaml_file(&request.params.text_document.uri) {
            return Vec::new();
        }
        let Some(offset) = request_offset(request) else {
            return Vec::new();
        };
        let Some(reference) =
            swlsp_symfony::yaml_container_constant_completion_at(request.source(), offset)
        else {
            return Vec::new();
        };
        let project = php.project();
        if reference.kind == ContainerValueKind::Enum
            && !swlsp_symfony::supports_yaml_container_enum(project.as_deref())
        {
            return Vec::new();
        }
        if let Some(scope) = reference.name.find("::") {
            let class_name = &reference.name[..scope];
            let partial = &reference.name[scope + 2..];
            let start = reference.range.start() + TextSize::from((scope + 2) as u32);
            let replace = TextRange::new(start, reference.range.end().max(start));
            return class_constant_items(
                php,
                class_name,
                partial,
                replace,
                line_index,
                reference.kind,
            );
        }
        if reference.kind == ContainerValueKind::Enum {
            return all_enum_items(php, &reference.name, reference.range, line_index);
        }
        all_constant_items(php, &reference.name, reference.range, line_index)
    }

    fn trigger_characters(&self) -> Vec<String> {
        vec![":".into(), "\\".into()]
    }
}

fn class_constant_items(
    php: &PhpIndex,
    class_name: &str,
    partial: &str,
    replace: TextRange,
    line_index: &LineIndex,
    kind: ContainerValueKind,
) -> Vec<protocol::CompletionItem> {
    let partial = lowercase(partial);
    let mut items = Vec::new();
    let mut seen = HashSet::new();
    for symbol in php.constants(class_name) {
        if kind == ContainerValueKind::Enum && symbol.kind != SymbolKind::EnumCase {
            continue;
        }
        if symbol.visibility != Visibility::Public || !lowercase(&symbol.name).starts_with(&partial)
        {
            continue;
        }
        if !seen.insert(symbol.name.to_string()) {
            continue;
        }
        items.push(container_constant_completion_item(
            &symbol.name,
            &format!("{class_name}::{}", symbol.name),
            &symbol,
            replace,
            line_index,
        ));
    }
    sort_by_lower_label(&mut items);
    items
}

fn all_enum_items(
    php: &PhpIndex,
    prefix: &str,
    replace: TextRange,
    line_index: &LineIndex,
) -> Vec<protocol::CompletionItem> {
    let prefix = lowercase(prefix);
    let mut items = Vec::new();
    let mut seen = HashSet::new();
    let mut add = |label: String, symbol: &Symbol, kind: CompletionItemKind| {
        if !lowercase(&label).starts_with(&prefix) || !seen.insert(label.clone()) {
            return;
        }
        let mut item =
            container_constant_completion_item(&label, &label, symbol, replace, line_index);
        item.kind = Some(kind);
        items.push(item);
    };
    if swlsp_symfony::supports_yaml_container_enum_class(php.project().as_deref()) {
        for symbol in php.class_symbols_view().iter() {
            if symbol.kind != SymbolKind::Enum {
                continue;
            }
            add(
                symbol
                    .fully_qualified
                    .strip_prefix('\\')
                    .unwrap_or(&symbol.fully_qualified)
                    .to_string(),
                symbol,
                CompletionItemKind::ENUM,
            );
        }
    }
    for symbol in php.constant_symbols() {
        if symbol.kind != SymbolKind::EnumCase {
            continue;
        }
        add(
            php.constant_symbol_name(&symbol),
            &symbol,
            CompletionItemKind::ENUM_MEMBER,
        );
    }
    sort_by_lower_label(&mut items);
    items
}

fn all_constant_items(
    php: &PhpIndex,
    prefix: &str,
    replace: TextRange,
    line_index: &LineIndex,
) -> Vec<protocol::CompletionItem> {
    let prefix = lowercase(prefix);
    let mut items = Vec::new();
    let mut seen = HashSet::new();
    for symbol in php.constant_symbols() {
        if symbol.kind != SymbolKind::GlobalConstant && symbol.visibility != Visibility::Public {
            continue;
        }
        let label = php.constant_symbol_name(&symbol);
        let lower = lowercase(&label);
        if matches!(lower.as_str(), "true" | "false" | "null") || !lower.starts_with(&prefix) {
            continue;
        }
        if !seen.insert(label.clone()) {
            continue;
        }
        items.push(container_constant_completion_item(
            &label, &label, &symbol, replace, line_index,
        ));
    }
    sort_by_lower_label(&mut items);
    items
}

fn container_constant_completion_item(
    label: &str,
    detail: &str,
    symbol: &Symbol,
    replace: TextRange,
    line_index: &LineIndex,
) -> protocol::CompletionItem {
    protocol::CompletionItem {
        label: label.to_string(),
        kind: Some(if symbol.kind == SymbolKind::EnumCase {
            CompletionItemKind::ENUM_MEMBER
        } else {
            CompletionItemKind::CONSTANT
        }),
        detail: detail.to_string(),
        text_edit: Some(text_edit(protocol_range(replace, line_index), label)),
        documentation: if symbol.doc_summary().is_empty() {
            None
        } else {
            markdown(symbol.doc_summary())
        },
        deprecated: symbol.flags.has(Flags::DEPRECATED),
        ..Default::default()
    }
}
