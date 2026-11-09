//! The editor-integration catalog command.

use std::collections::BTreeMap;
use std::sync::Arc;

use serde_json::Value;
use swlsp_lsp::{BoxError, CommandFunc, CommandProvider, Context};

pub const CATALOG_COMMAND: &str = "shopware/integration/catalog";

/// Serves [`swlsp_integration::current_catalog`].
#[derive(Debug, Default, Clone, Copy)]
pub struct Provider;

impl CommandProvider for Provider {
    fn commands(&self, _ctx: &Context) -> BTreeMap<String, CommandFunc> {
        let catalog: CommandFunc = Arc::new(|ctx: &Context, _args: Option<&Value>| {
            ctx.check()?;
            serde_json::to_value(swlsp_integration::current_catalog())
                .map_err(|error| Box::new(error) as BoxError)
        });
        BTreeMap::from([(CATALOG_COMMAND.to_owned(), catalog)])
    }
}
