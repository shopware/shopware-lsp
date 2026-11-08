//! Environment variable references (port of `environment_reference.go`).

use std::collections::HashSet;
use std::sync::Arc;

use swlsp_environment::{Index, occurrence_at, php_occurrence_at};
use swlsp_lsp::{Context, ProviderResult, ReferenceRequest, ReferencesProvider};
use swlsp_lsp_protocol as protocol;

use crate::{FileLines, request_offset};

pub struct EnvironmentReferenceProvider {
    index: Option<Arc<Index>>,
}

impl EnvironmentReferenceProvider {
    pub fn new(index: Option<Arc<Index>>) -> Self {
        Self { index }
    }
}

impl ReferencesProvider for EnvironmentReferenceProvider {
    fn get_references(
        &self,
        _ctx: &Context,
        request: &ReferenceRequest,
    ) -> ProviderResult<Vec<protocol::Location>> {
        let (Some(index), Some(offset)) = (&self.index, request_offset(request)) else {
            return Ok(Vec::new());
        };
        let path = swlsp_uriutil::path(&request.params.text_document.uri).unwrap_or_default();
        let occurrence = request
            .node
            .as_ref()
            .and_then(|node| php_occurrence_at(node, offset))
            .or_else(|| occurrence_at(&path, request.source(), offset));
        let Some(occurrence) = occurrence else {
            return Ok(Vec::new());
        };
        let Some(variable) = index.variable(&occurrence.name)? else {
            return Ok(Vec::new());
        };
        let mut occurrences = Vec::new();
        if request.params.context.include_declaration {
            occurrences.extend(variable.declarations.iter());
        }
        occurrences.extend(variable.references.iter());
        let mut files = FileLines::default();
        let mut result = Vec::with_capacity(occurrences.len());
        let mut seen = HashSet::with_capacity(occurrences.len());
        for current in occurrences {
            let Some(location) = files.location(&current.file, current.name_range) else {
                continue;
            };
            if !seen.insert((location.uri.clone(), location.range.start)) {
                continue;
            }
            result.push(location);
        }
        Ok(result)
    }
}
