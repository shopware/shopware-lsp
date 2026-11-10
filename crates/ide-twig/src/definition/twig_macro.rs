//! Twig macro calls to their declarations.

use std::collections::HashSet;
use std::sync::Arc;

use swlsp_lsp::{Context, DefinitionRequest, GotoDefinitionProvider};
use swlsp_lsp_protocol as protocol;
use swlsp_syntax::{LineIndex, SyntaxNode};
use swlsp_twig::{Macro, MacroReference, TwigIndexer};
use swlsp_uriutil::file_uri;

use crate::definition::common::{protocol_range, request_offset, request_uri};
use crate::definition::twig_common::uri_path;

pub struct TwigMacroDefinitionProvider {
    index: Option<Arc<TwigIndexer>>,
}

impl TwigMacroDefinitionProvider {
    pub fn new(index: Option<Arc<TwigIndexer>>) -> Self {
        Self { index }
    }
}

impl GotoDefinitionProvider for TwigMacroDefinitionProvider {
    fn get_definition(
        &self,
        _ctx: &Context,
        request: &DefinitionRequest,
    ) -> Vec<protocol::Location> {
        let (Some(index), Some(_), Some(root), Some(lines)) = (
            self.index.as_deref(),
            request.node.as_ref(),
            request.root.as_ref(),
            request.line_index(),
        ) else {
            return Vec::new();
        };
        let uri = request_uri(request);
        // The extension check is case-sensitive.
        if !swlsp_uriutil::filepath::ext(uri).eq(".twig") {
            return Vec::new();
        }
        let Some(offset) = request_offset(request) else {
            return Vec::new();
        };
        let path = uri_path(uri);
        let Some(reference) = swlsp_twig::macro_reference_at(&path, root, offset) else {
            return Vec::new();
        };
        let macros = definitions(index, &path, root, &reference);
        let mut seen = HashSet::new();
        let mut result = Vec::new();
        for definition in &macros {
            let location = if definition.file_path == path {
                protocol::Location {
                    uri: uri.to_owned(),
                    range: protocol_range(lines, definition.name_range),
                }
            } else {
                let Ok(source) = std::fs::read(&definition.file_path) else {
                    continue;
                };
                let lines = LineIndex::new(String::from_utf8_lossy(&source).into_owned());
                protocol::Location {
                    uri: file_uri(&definition.file_path),
                    range: protocol_range(&lines, definition.name_range),
                }
            };
            if !seen.insert((location.uri.clone(), definition.name_range)) {
                continue;
            }
            result.push(location);
        }
        result
    }
}

fn definitions(
    index: &TwigIndexer,
    path: &str,
    root: &SyntaxNode,
    reference: &MacroReference,
) -> Vec<Macro> {
    let mut result = Vec::new();
    for template in &reference.templates {
        if let Ok(macros) = index.find_macro(template, &reference.name) {
            result.extend(macros);
        }
    }
    let current_templates = swlsp_twig::template_names(path);
    if reference
        .templates
        .iter()
        .any(|target| current_templates.contains(target))
    {
        result.retain(|definition| definition.file_path != path);
        result.extend(
            swlsp_twig::macros_in_document(path, root)
                .into_iter()
                .filter(|definition| definition.name == reference.name),
        );
    }
    result
}

#[cfg(test)]
#[path = "twig_macro_tests.rs"]
mod tests;
