//! DAL entity and field workspace symbols.

use std::sync::Arc;

use swlsp_lsp::{Context, ProviderResult, WorkspaceSymbolProvider};
use swlsp_lsp_protocol as protocol;
use swlsp_shopware_dal::Index;
use swlsp_textutil::lowercase;

use crate::symbol::{line_location, lower_contains};

/// Makes technical entity and field names searchable without requiring users
/// to know their PHP Definition class names.
pub struct DalWorkspaceSymbolProvider {
    index: Option<Arc<Index>>,
}

impl DalWorkspaceSymbolProvider {
    pub fn new(index: Option<Arc<Index>>) -> Self {
        Self { index }
    }
}

impl WorkspaceSymbolProvider for DalWorkspaceSymbolProvider {
    fn workspace_symbols(
        &self,
        ctx: &Context,
        query: &str,
    ) -> ProviderResult<Vec<protocol::SymbolInformation>> {
        let Some(index) = &self.index else {
            return Ok(Vec::new());
        };
        let definitions = index.definitions()?;
        let query = lowercase(query.trim());
        let mut result = Vec::new();
        for definition in &definitions {
            ctx.check()?;
            append(
                &mut result,
                &query,
                &definition.name,
                format!("Shopware DAL entity · {}", definition.class),
                &definition.file,
                definition.line,
                protocol::SymbolKind::CLASS,
            );
            for field in &definition.fields {
                append(
                    &mut result,
                    &query,
                    &field.name,
                    format!("Shopware DAL field · {} · {}", definition.name, field.type_),
                    &definition.file,
                    field.line,
                    protocol::SymbolKind::FIELD,
                );
            }
        }
        result.sort_by(|left, right| {
            left.name
                .cmp(&right.name)
                .then_with(|| left.container_name.cmp(&right.container_name))
                .then_with(|| left.location.uri.cmp(&right.location.uri))
        });
        Ok(result)
    }
}

fn append(
    result: &mut Vec<protocol::SymbolInformation>,
    query: &str,
    name: &str,
    container: String,
    path: &str,
    line: u32,
    kind: protocol::SymbolKind,
) {
    if name.is_empty() || path.is_empty() {
        return;
    }
    if !query.is_empty() && !lower_contains(&format!("{name} {container}"), query) {
        return;
    }
    result.push(protocol::SymbolInformation {
        name: name.to_owned(),
        kind,
        location: line_location(path, i64::from(line)),
        container_name: container,
        priority: 0,
    });
}
