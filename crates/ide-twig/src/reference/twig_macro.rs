//! Twig macro references.

use std::sync::Arc;

use swlsp_lsp::{Context, ProviderResult, ReferenceRequest, ReferencesProvider};
use swlsp_lsp_protocol as protocol;
use swlsp_textutil::eq_ignore_case;
use swlsp_twig::{
    MacroReference, MacroReferenceRole, TwigIndexer, macro_reference_at,
    macro_references_in_document,
};

use crate::reference::{FileLines, document_location, lower_ext, request_offset, unique_locations};

pub struct TwigMacroReferenceProvider {
    index: Option<Arc<TwigIndexer>>,
}

impl TwigMacroReferenceProvider {
    pub fn new(index: Option<Arc<TwigIndexer>>) -> Self {
        Self { index }
    }
}

impl ReferencesProvider for TwigMacroReferenceProvider {
    fn get_references(
        &self,
        _ctx: &Context,
        request: &ReferenceRequest,
    ) -> ProviderResult<Vec<protocol::Location>> {
        let (Some(index), Some(_), Some(root), Some(line_index), Some(offset)) = (
            &self.index,
            &request.node,
            &request.root,
            request.line_index(),
            request_offset(request),
        ) else {
            return Ok(Vec::new());
        };
        if lower_ext(&request.params.text_document.uri) != ".twig" {
            return Ok(Vec::new());
        }
        let current_path =
            swlsp_uriutil::path(&request.params.text_document.uri).unwrap_or_default();
        let Some(current) = macro_reference_at(&current_path, root, offset) else {
            return Ok(Vec::new());
        };
        let include_declaration = request.params.context.include_declaration;
        let mut files = FileLines::default();
        let mut locations = Vec::new();
        if include_declaration {
            for template in &current.templates {
                for found in index.find_macro(template, &current.name)? {
                    if found.file_path == current_path {
                        continue;
                    }
                    locations.extend(files.location(&found.file_path, found.name_range));
                }
            }
        }
        for template in &current.templates {
            for usage in index.get_macro_usages(template, &current.name)? {
                if usage.file_path == current_path {
                    continue;
                }
                locations.extend(files.location(&usage.file_path, usage.range));
            }
        }
        for reference in macro_references_in_document(&current_path, root) {
            if !same_macro_reference(&current, &reference)
                || (reference.role == MacroReferenceRole::Declaration && !include_declaration)
            {
                continue;
            }
            locations.push(document_location(request, line_index, reference.range));
        }
        Ok(unique_locations(locations))
    }
}

fn same_macro_reference(left: &MacroReference, right: &MacroReference) -> bool {
    eq_ignore_case(&left.name, &right.name)
        && left
            .templates
            .iter()
            .any(|template| right.templates.contains(template))
}
