//! Completion of imported macro names.

use std::collections::HashSet;
use std::sync::Arc;

use swlsp_lsp::{CompletionProvider, CompletionRequest, Context};
use swlsp_lsp_protocol::{self as protocol, CompletionItemKind, InsertTextFormat};
use swlsp_parser_twig::query as twigquery;
use swlsp_parser_twig::syntax::TWIG_VAR;
use swlsp_syntax::SyntaxNode;
use swlsp_textutil::eq_ignore_case;
use swlsp_twig::{Macro, MacroImport, TwigIndexer};

use super::{lower_ext, markdown};
use crate::completion::common::sort_completion_items;
use swlsp_textutil::lowercase;

pub struct TwigMacroCompletionProvider {
    index: Option<Arc<TwigIndexer>>,
}

impl TwigMacroCompletionProvider {
    pub fn new(index: Option<Arc<TwigIndexer>>) -> Self {
        Self { index }
    }

    fn namespace_completions(
        &self,
        index: &TwigIndexer,
        path: &str,
        root: &SyntaxNode,
        templates: &[String],
    ) -> Vec<protocol::CompletionItem> {
        let mut macros = Vec::new();
        for template in templates {
            if let Ok(current) = index.get_macros(template) {
                macros.extend(current);
            }
        }
        let macros = overlay_current_macros(path, root, templates, macros);
        let mut seen = HashSet::new();
        let mut items = Vec::new();
        for current in &macros {
            if !seen.insert(lowercase(&current.name)) {
                continue;
            }
            items.push(macro_completion_item(&current.name, current));
        }
        sort_completion_items(&mut items);
        items
    }

    fn direct_completions(
        &self,
        index: &TwigIndexer,
        path: &str,
        root: &SyntaxNode,
        imports: &[MacroImport],
    ) -> Vec<protocol::CompletionItem> {
        let mut items = Vec::new();
        for current in imports {
            let mut definitions = Vec::new();
            for template in &current.templates {
                if let Ok(macros) = index.find_macro(template, &current.name) {
                    definitions.extend(macros);
                }
            }
            let definitions = overlay_current_macros(path, root, &current.templates, definitions);
            let definition = definitions
                .into_iter()
                .find(|candidate| eq_ignore_case(&candidate.name, &current.name))
                .unwrap_or_default();
            items.push(macro_completion_item(&current.alias, &definition));
        }
        sort_completion_items(&mut items);
        items
    }
}

impl CompletionProvider for TwigMacroCompletionProvider {
    fn get_completions(
        &self,
        _ctx: &Context,
        request: &CompletionRequest,
    ) -> Vec<protocol::CompletionItem> {
        let Some(index) = &self.index else {
            return Vec::new();
        };
        let (Some(node), Some(root)) = (request.node.as_ref(), request.root.as_ref()) else {
            return Vec::new();
        };
        if lower_ext(&request.params.text_document.uri) != ".twig"
            || twigquery::closest_node_of_kind(node, &[TWIG_VAR]).is_none()
        {
            return Vec::new();
        }
        let path = swlsp_uriutil::path(&request.params.text_document.uri).unwrap_or_default();
        let Some(current) = swlsp_twig::macro_completion_at(&path, root, node) else {
            return Vec::new();
        };
        if !current.templates.is_empty() {
            return self.namespace_completions(index, &path, root, &current.templates);
        }
        self.direct_completions(index, &path, root, &current.direct)
    }

    fn trigger_characters(&self) -> Vec<String> {
        vec![".".into()]
    }
}

fn overlay_current_macros(
    path: &str,
    root: &SyntaxNode,
    templates: &[String],
    persisted: Vec<Macro>,
) -> Vec<Macro> {
    let current_templates = swlsp_twig::template_names(path);
    if !templates
        .iter()
        .any(|target| current_templates.contains(target))
    {
        return persisted;
    }
    let mut result: Vec<Macro> = persisted
        .into_iter()
        .filter(|current| current.file_path != path)
        .collect();
    result.extend(swlsp_twig::macros_in_document(path, root));
    result
}

fn macro_completion_item(label: &str, definition: &Macro) -> protocol::CompletionItem {
    let detail = if definition.name.is_empty() {
        "Twig macro".to_string()
    } else {
        definition.signature()
    };
    protocol::CompletionItem {
        label: label.to_string(),
        kind: Some(CompletionItemKind::METHOD),
        detail,
        insert_text: format!("{label}($0)"),
        insert_text_format: Some(InsertTextFormat::SNIPPET),
        documentation: if definition.documentation.is_empty() {
            None
        } else {
            markdown(definition.documentation.clone())
        },
        ..Default::default()
    }
}
