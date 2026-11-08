//! `shopware/symfony/console/commands` (port of Go `console.go`).

use std::collections::BTreeMap;
use std::sync::Arc;

use serde_json::Value;
use swlsp_console::{CatalogEntry, CatalogProvider, CatalogRequest, Index, LIST_CATALOG_COMMAND};
use swlsp_lsp::{BoxError, CommandFunc, CommandProvider, Context};

/// Exposes the Symfony console command catalog.
pub struct ConsoleCatalogProvider {
    catalog: Option<Arc<CatalogProvider>>,
}

impl ConsoleCatalogProvider {
    pub fn new(index: Arc<Index>, workspace_root: Option<&str>) -> Self {
        Self {
            catalog: Some(Arc::new(CatalogProvider::new(index, workspace_root))),
        }
    }

    /// A provider whose command reports that the catalog is unavailable.
    pub fn unavailable() -> Self {
        Self { catalog: None }
    }

    pub fn catalog(&self, ctx: &Context, query: &str) -> Result<Vec<CatalogEntry>, BoxError> {
        self.catalog_with_request(
            ctx,
            &CatalogRequest {
                query: query.to_owned(),
                ..CatalogRequest::default()
            },
        )
    }

    pub fn catalog_with_request(
        &self,
        ctx: &Context,
        request: &CatalogRequest,
    ) -> Result<Vec<CatalogEntry>, BoxError> {
        let Some(catalog) = &self.catalog else {
            return Err("symfony console catalog is unavailable".into());
        };
        catalog.catalog_with_request(ctx, request)
    }
}

fn list(
    catalog: Option<&CatalogProvider>,
    ctx: &Context,
    args: Option<&Value>,
) -> Result<Value, BoxError> {
    let Some(catalog) = catalog else {
        return Err("symfony console catalog is unavailable".into());
    };
    let request: CatalogRequest = match args {
        None | Some(Value::Null) => CatalogRequest::default(),
        Some(value) => serde_json::from_value(value.clone())
            .map_err(|error| format!("invalid console catalog request: {error}"))?,
    };
    crate::to_value(&catalog.catalog_with_request(ctx, &request)?)
}

impl CommandProvider for ConsoleCatalogProvider {
    fn commands(&self, _ctx: &Context) -> BTreeMap<String, CommandFunc> {
        let catalog = self.catalog.clone();
        let list: CommandFunc = Arc::new(move |ctx, args| list(catalog.as_deref(), ctx, args));
        BTreeMap::from([(LIST_CATALOG_COMMAND.to_owned(), list)])
    }
}
