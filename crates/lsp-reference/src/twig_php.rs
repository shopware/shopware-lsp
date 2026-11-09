//! Twig usages of PHP declarations and Twig extension callables.

use std::sync::Arc;

use swlsp_lsp::{Context, ProviderResult, ReferenceRequest, ReferencesProvider};
use swlsp_lsp_protocol as protocol;
use swlsp_php::{PhpIndex, symbol_at};
use swlsp_textutil::eq_ignore_case;
use swlsp_twig::{
    TwigIndexer, extension_usage_at, extension_usages_in_document, php_usage_target_for_symbol,
};
use swlsp_uriutil::filepath;

use crate::{
    FileLines, document_location, lower_ext, php_semantic_state, request_offset, sort_locations,
    unique_locations,
};

/// Adds typed Twig member and direct class-name usages to Find References
/// for PHP declarations.
pub struct TwigPhpReferenceProvider {
    index: Option<Arc<TwigIndexer>>,
    php: Option<PhpIndex>,
}

impl TwigPhpReferenceProvider {
    pub fn new(index: Option<Arc<TwigIndexer>>, php: Option<PhpIndex>) -> Self {
        Self { index, php }
    }
}

impl ReferencesProvider for TwigPhpReferenceProvider {
    fn get_references(
        &self,
        _ctx: &Context,
        request: &ReferenceRequest,
    ) -> ProviderResult<Vec<protocol::Location>> {
        let (Some(index), Some(php), Some(_), Some(_), Some(offset)) = (
            &self.index,
            &self.php,
            &request.document,
            &request.root,
            request_offset(request),
        ) else {
            return Ok(Vec::new());
        };
        let Ok(path) = swlsp_uriutil::path(&request.params.text_document.uri) else {
            return Ok(Vec::new());
        };
        match lower_ext(&path).as_str() {
            ".twig" => twig_extension_references(index, request, &path, offset),
            ".php" => php_references(index, php, request, &path, offset),
            _ => Ok(Vec::new()),
        }
    }
}

fn php_references(
    index: &TwigIndexer,
    php: &PhpIndex,
    request: &ReferenceRequest,
    path: &str,
    offset: u32,
) -> ProviderResult<Vec<protocol::Location>> {
    let Some((document, snapshot)) = php_semantic_state(php, request, path) else {
        return Ok(Vec::new());
    };
    let Some(symbol) = symbol_at(&document, &snapshot, offset) else {
        return Ok(Vec::new());
    };
    let Some(target) = php_usage_target_for_symbol(&snapshot, &symbol) else {
        return Ok(Vec::new());
    };
    let mut files = FileLines::default();
    let mut locations = Vec::new();
    for reference in index.get_php_usage_references(&target)? {
        locations.extend(files.location(&reference.file_path, reference.range));
    }
    for usage in index.get_extension_usages_for_php_symbol(php, &symbol)? {
        locations.extend(files.location(&usage.file_path, usage.range));
    }
    Ok(unique_sorted(locations))
}

fn twig_extension_references(
    index: &TwigIndexer,
    request: &ReferenceRequest,
    path: &str,
    offset: u32,
) -> ProviderResult<Vec<protocol::Location>> {
    let (Some(root), Some(line_index)) = (&request.root, request.line_index()) else {
        return Ok(Vec::new());
    };
    let Some(target) = extension_usage_at(path, root, offset) else {
        return Ok(Vec::new());
    };
    let clean_path = filepath::clean(path);
    let mut files = FileLines::default();
    let mut locations = Vec::new();
    for usage in index.get_extension_usages(target.kind, &target.name)? {
        if filepath::clean(&usage.file_path) == clean_path {
            continue;
        }
        locations.extend(files.location(&usage.file_path, usage.range));
    }
    for usage in extension_usages_in_document(path, root) {
        if usage.kind != target.kind || !eq_ignore_case(&usage.name, &target.name) {
            continue;
        }
        if filepath::clean(&usage.file_path) == clean_path {
            locations.push(document_location(request, line_index, usage.range));
        } else {
            locations.extend(files.location(&usage.file_path, usage.range));
        }
    }
    Ok(unique_sorted(locations))
}

fn unique_sorted(locations: Vec<protocol::Location>) -> Vec<protocol::Location> {
    let mut result = unique_locations(locations);
    sort_locations(&mut result);
    result
}
