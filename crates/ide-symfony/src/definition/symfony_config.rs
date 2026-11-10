use std::collections::HashSet;
use std::sync::Arc;

use swlsp_lsp::{Context, DefinitionRequest, GotoDefinitionProvider};
use swlsp_lsp_protocol as protocol;
use swlsp_symfonyconfig::{Index, ResourceReference, RootReference};
use swlsp_syntax::LineIndex;

use crate::definition::common::{protocol_range, request_uri};

pub struct SymfonyConfigDefinitionProvider {
    index: Option<Arc<Index>>,
}

impl SymfonyConfigDefinitionProvider {
    pub fn new(index: Option<Arc<Index>>) -> Self {
        Self { index }
    }

    fn root_definitions(
        &self,
        index: &Index,
        request: &DefinitionRequest,
        reference: &RootReference,
    ) -> Vec<protocol::Location> {
        let Ok(roots) = index.roots(&reference.name) else {
            return Vec::new();
        };
        let uri = request_uri(request);
        let current_path = swlsp_uriutil::path(uri).unwrap_or_default();
        let mut result = Vec::new();
        for root in &roots {
            if root.file == current_path
                && let Some(lines) = request.line_index()
            {
                result.push(protocol::Location {
                    uri: uri.to_owned(),
                    range: protocol_range(lines, root.range),
                });
                continue;
            }
            let Ok(source) = std::fs::read(&root.file) else {
                continue;
            };
            let lines = LineIndex::new(String::from_utf8_lossy(&source).into_owned());
            result.push(protocol::Location {
                uri: swlsp_uriutil::file_uri(&root.file),
                range: protocol_range(&lines, root.range),
            });
        }
        unique_locations(result)
    }
}

fn resource_definitions(
    request: &DefinitionRequest,
    reference: &ResourceReference,
) -> Vec<protocol::Location> {
    let Ok(current_path) = swlsp_uriutil::path(request_uri(request)) else {
        return Vec::new();
    };
    unique_locations(
        swlsp_symfonyconfig::resource_files(&current_path, &reference.path)
            .iter()
            .map(|path| protocol::Location {
                uri: swlsp_uriutil::file_uri(path),
                range: protocol::Range::default(),
            })
            .collect(),
    )
}

fn unique_locations(mut locations: Vec<protocol::Location>) -> Vec<protocol::Location> {
    locations.sort_by(|left, right| {
        left.uri
            .cmp(&right.uri)
            .then(left.range.start.line.cmp(&right.range.start.line))
            .then(left.range.start.character.cmp(&right.range.start.character))
    });
    let mut seen = HashSet::new();
    locations.retain(|location| {
        seen.insert((
            location.uri.clone(),
            location.range.start.line,
            location.range.start.character,
            location.range.end.line,
            location.range.end.character,
        ))
    });
    locations
}

impl GotoDefinitionProvider for SymfonyConfigDefinitionProvider {
    fn get_definition(
        &self,
        _ctx: &Context,
        request: &DefinitionRequest,
    ) -> Vec<protocol::Location> {
        let (Some(index), Some(node)) = (&self.index, request.node.as_ref()) else {
            return Vec::new();
        };
        if let Some(reference) = swlsp_symfonyconfig::root_reference_at(node) {
            return self.root_definitions(index, request, &reference);
        }
        if let Some(reference) = swlsp_symfonyconfig::resource_reference_at(node) {
            return resource_definitions(request, &reference);
        }
        Vec::new()
    }
}
