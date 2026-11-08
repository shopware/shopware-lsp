//! The portable counterpart of Symfony plugin routing-resource gutter
//! markers.

use std::collections::HashSet;
use std::sync::Arc;

use swlsp_lsp::{CodeLensProvider, CodeLensRequest, Context, ProviderResult};
use swlsp_lsp_protocol::{self as protocol, Range};
use swlsp_php::PhpIndex;
use swlsp_symfony::{RouteIndexer, RouteResourceResolver};

use crate::{
    by_position, cancelled, lower_ext, related_lens, related_protocol_range, related_source_line,
    related_target, same_path, unique_related_targets,
};

pub struct RouteResourceCodeLensProvider {
    index: Option<Arc<RouteIndexer>>,
    resolver: RouteResourceResolver,
}

impl RouteResourceCodeLensProvider {
    pub fn new(index: Option<Arc<RouteIndexer>>, php: Option<PhpIndex>) -> Self {
        Self {
            index,
            resolver: RouteResourceResolver::new(php, None),
        }
    }
}

impl CodeLensProvider for RouteResourceCodeLensProvider {
    fn get_code_lenses(
        &self,
        ctx: &Context,
        request: &CodeLensRequest,
    ) -> ProviderResult<Vec<protocol::CodeLens>> {
        let document = &request.document;
        let Some(root) = document.root() else {
            return Ok(Vec::new());
        };
        let path = swlsp_uriutil::path(&request.params.text_document.uri)?;
        if !matches!(
            lower_ext(&path).as_str(),
            ".php" | ".yaml" | ".yml" | ".xml"
        ) {
            return Ok(Vec::new());
        }
        let lines = document.line_index();
        let mut result = Vec::new();
        let mut seen = HashSet::new();
        for reference in swlsp_symfony::route_resource_references(&root) {
            cancelled(ctx)?;
            if !seen.insert(reference.range) {
                continue;
            }
            let targets = unique_related_targets(
                self.resolver
                    .files(&path, &reference)
                    .iter()
                    .map(|target| related_target(target, 1))
                    .collect(),
            );
            if targets.is_empty() {
                continue;
            }
            let title = if targets.len() > 1 {
                format!("Open {} matching route files", targets.len())
            } else {
                "Open routing resource".to_owned()
            };
            result.push(related_lens(
                related_protocol_range(reference.range, lines),
                title,
                targets,
            ));
        }
        if let Some(reverse) = self.reverse_resource_lens(&path)? {
            result.push(reverse);
        }
        result.sort_by(by_position);
        Ok(result)
    }

    fn resolve_code_lens(
        &self,
        _ctx: &Context,
        lens: &protocol::CodeLens,
    ) -> ProviderResult<Option<protocol::CodeLens>> {
        Ok(Some(lens.clone()))
    }
}

impl RouteResourceCodeLensProvider {
    fn reverse_resource_lens(&self, path: &str) -> ProviderResult<Option<protocol::CodeLens>> {
        let Some(index) = &self.index else {
            return Ok(None);
        };
        let mut targets = Vec::new();
        for resource in index.get_route_resource_imports()? {
            if same_path(&resource.file_path, path)
                || !self
                    .resolver
                    .matches(&resource.file_path, path, &resource.reference())
            {
                continue;
            }
            targets.push(related_target(
                &resource.file_path,
                related_source_line(&resource.file_path, resource.range.start().into()),
            ));
        }
        let targets = unique_related_targets(targets);
        if targets.is_empty() {
            return Ok(None);
        }
        let title = if targets.len() > 1 {
            format!("Open {} routing imports", targets.len())
        } else {
            "Open routing import".to_owned()
        };
        Ok(Some(related_lens(Range::default(), title, targets)))
    }
}
