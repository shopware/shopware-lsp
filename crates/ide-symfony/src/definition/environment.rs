use std::sync::Arc;

use swlsp_environment::Index;
use swlsp_lsp::{Context, DefinitionRequest, GotoDefinitionProvider};
use swlsp_lsp_protocol as protocol;

use crate::definition::common::{file_range_location, request_offset, request_uri};

pub struct EnvironmentDefinitionProvider {
    index: Option<Arc<Index>>,
}

impl EnvironmentDefinitionProvider {
    pub fn new(index: Option<Arc<Index>>) -> Self {
        Self { index }
    }
}

impl GotoDefinitionProvider for EnvironmentDefinitionProvider {
    fn get_definition(
        &self,
        _ctx: &Context,
        request: &DefinitionRequest,
    ) -> Vec<protocol::Location> {
        let Some(index) = &self.index else {
            return Vec::new();
        };
        let Some(offset) = request_offset(request) else {
            return Vec::new();
        };
        let path = swlsp_uriutil::path(request_uri(request)).unwrap_or_default();
        let occurrence = request
            .node
            .as_ref()
            .and_then(|node| swlsp_environment::php_occurrence_at(node, offset))
            .or_else(|| swlsp_environment::occurrence_at(&path, request.source(), offset));
        let Some(occurrence) = occurrence else {
            return Vec::new();
        };
        let Ok(Some(variable)) = index.variable(&occurrence.name) else {
            return Vec::new();
        };
        variable
            .declarations
            .iter()
            .filter_map(|declaration| {
                file_range_location(&declaration.file, declaration.name_range)
            })
            .collect()
    }
}
