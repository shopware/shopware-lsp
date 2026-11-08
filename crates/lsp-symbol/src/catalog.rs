//! Persisted workspace-symbol catalog with live fallbacks (port of
//! `catalog_symbol.go`).

use std::sync::Arc;

use swlsp_indexer::{WorkspaceSymbol, WorkspaceSymbolCatalog, WorkspaceSymbolQueryOptions};
use swlsp_lsp::{Context, ProviderResult, WorkspaceSymbolProvider};
use swlsp_lsp_protocol as protocol;

use crate::symbol_kind;

const CATALOG_LIMIT: usize = 500;

/// Uses the persisted FTS catalog once initial indexing completes. During a
/// cold index it delegates to the live providers, preserving
/// workspace-symbol availability during startup.
pub struct CatalogWorkspaceSymbolProvider {
    catalog: Option<Arc<WorkspaceSymbolCatalog>>,
    fallbacks: Vec<Arc<dyn WorkspaceSymbolProvider>>,
    excluded_domains: Vec<String>,
}

impl CatalogWorkspaceSymbolProvider {
    pub fn new(
        catalog: Option<Arc<WorkspaceSymbolCatalog>>,
        fallbacks: Vec<Arc<dyn WorkspaceSymbolProvider>>,
    ) -> Self {
        Self {
            catalog,
            fallbacks,
            excluded_domains: Vec::new(),
        }
    }

    /// Removes catalog domains at query time. Fallback providers are already
    /// framework-specific and remain available during cold indexing.
    pub fn exclude_domains<S: Into<String>>(&mut self, domains: impl IntoIterator<Item = S>) {
        self.excluded_domains = domains.into_iter().map(Into::into).collect();
    }
}

impl WorkspaceSymbolProvider for CatalogWorkspaceSymbolProvider {
    fn workspace_symbols(
        &self,
        ctx: &Context,
        query: &str,
    ) -> ProviderResult<Vec<protocol::SymbolInformation>> {
        if let Some(catalog) = &self.catalog
            && catalog.ready()?
        {
            let symbols = catalog.query_with_options(
                query,
                CATALOG_LIMIT,
                &WorkspaceSymbolQueryOptions {
                    excluded_domains: self.excluded_domains.clone(),
                },
            )?;
            let result: Vec<_> = symbols.iter().map(catalog_protocol_symbol).collect();
            // PHP declarations are the first migrated domain. A miss still
            // delegates to the framework providers until their indexers
            // publish into the catalog as well.
            if !result.is_empty() || self.fallbacks.is_empty() {
                return Ok(result);
            }
        }
        let mut result = Vec::new();
        for fallback in &self.fallbacks {
            ctx.check()?;
            let symbols = fallback
                .workspace_symbols(ctx, query)
                .map_err(|error| format!("fallback workspace symbols: {error}"))?;
            result.extend(symbols);
        }
        Ok(result)
    }
}

fn catalog_protocol_symbol(symbol: &WorkspaceSymbol) -> protocol::SymbolInformation {
    let position = |value: &swlsp_indexer::WorkspaceSymbolPosition| protocol::Position {
        line: value.line.max(0) as u32,
        character: value.character.max(0) as u32,
    };
    protocol::SymbolInformation {
        name: symbol.name.clone(),
        kind: symbol_kind(symbol.kind.0),
        location: protocol::Location {
            uri: swlsp_uriutil::file_uri(&symbol.path),
            range: protocol::Range {
                start: position(&symbol.range.start),
                end: position(&symbol.range.end),
            },
        },
        container_name: symbol.container_name.clone(),
        priority: symbol.priority,
    }
}
