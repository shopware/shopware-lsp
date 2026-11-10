//! Twig template references from Twig and PHP.

use std::collections::HashSet;
use std::sync::Arc;

use swlsp_lsp::{Context, ProviderResult, ReferenceRequest, ReferencesProvider};
use swlsp_lsp_protocol as protocol;
use swlsp_syntax::SyntaxNode;
use swlsp_twig::{
    TemplateReference, TwigIndexer, php_template_references, template_names, template_reference_at,
    twig_template_references,
};
use swlsp_uriutil::filepath;

use crate::reference::{FileLines, document_location, lower_ext, request_offset, unique_locations};

pub struct TwigTemplateReferenceProvider {
    index: Option<Arc<TwigIndexer>>,
}

impl TwigTemplateReferenceProvider {
    pub fn new(index: Option<Arc<TwigIndexer>>) -> Self {
        Self { index }
    }
}

impl ReferencesProvider for TwigTemplateReferenceProvider {
    fn get_references(
        &self,
        _ctx: &Context,
        request: &ReferenceRequest,
    ) -> ProviderResult<Vec<protocol::Location>> {
        let (Some(index), Some(root), Some(line_index), Some(offset)) = (
            &self.index,
            &request.root,
            request.line_index(),
            request_offset(request),
        ) else {
            return Ok(Vec::new());
        };
        let Ok(path) = swlsp_uriutil::path(&request.params.text_document.uri) else {
            return Ok(Vec::new());
        };
        let current_references = template_references_in_document(&path, root);
        let Some(current) = template_reference_at(&current_references, offset) else {
            return Ok(Vec::new());
        };

        let mut target_names = vec![current.template.clone()];
        let files = index.get_twig_files_by_rel_path(&current.template)?;
        let unique_targets: HashSet<String> = files
            .iter()
            .map(|file| filepath::clean(&file.path))
            .collect();
        if unique_targets.len() == 1
            && let Some(target) = unique_targets.iter().next()
        {
            target_names = template_names(target);
        }
        let mut lines = FileLines::default();
        let mut locations = Vec::new();
        for reference in index.get_template_references(&target_names)? {
            if reference.file_path == path {
                continue;
            }
            locations.extend(lines.location(&reference.file_path, reference.range));
        }
        for reference in &current_references {
            if template_name_matches_any(&reference.template, &target_names) {
                locations.push(document_location(request, line_index, reference.range));
            }
        }
        if request.params.context.include_declaration {
            for file in &files {
                locations.push(protocol::Location {
                    uri: swlsp_uriutil::file_uri(&file.path),
                    range: protocol::Range::default(),
                });
            }
        }
        Ok(unique_locations(locations))
    }
}

fn template_references_in_document(path: &str, root: &SyntaxNode) -> Vec<TemplateReference> {
    match lower_ext(path).as_str() {
        ".twig" => twig_template_references(path, root),
        ".php" => php_template_references(path, root),
        _ => Vec::new(),
    }
}

fn normalized_template_name(name: &str) -> String {
    let normalized = filepath::to_slash(name.trim());
    normalized
        .strip_prefix('/')
        .unwrap_or(&normalized)
        .to_owned()
}

fn template_name_matches_any(template: &str, names: &[String]) -> bool {
    let template = normalized_template_name(template);
    names
        .iter()
        .any(|name| normalized_template_name(name) == template)
}
