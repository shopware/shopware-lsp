//! `shopware/extension/all`: every Shopware extension detected in the
//! workspace.

use std::collections::BTreeMap;
use std::sync::Arc;

use serde::Serialize;
use swlsp_extension::{ExtensionIndexer, ShopwareExtension};
use swlsp_lsp::{BoxError, CommandFunc, CommandProvider, Context};

/// Lists every detected Shopware extension.
pub struct ExtensionCommandProvider {
    index: Arc<ExtensionIndexer>,
}

/// The extension JSON shape the editor clients expect: PascalCase members
/// and `null` permissions when there are none.
#[derive(Serialize)]
#[serde(rename_all = "PascalCase")]
struct ExtensionWire<'a> {
    name: &'a str,
    #[serde(rename = "Type")]
    kind: u8,
    path: &'a str,
    permissions: Option<Vec<PermissionWire<'a>>>,
}

#[derive(Serialize)]
#[serde(rename_all = "PascalCase")]
struct PermissionWire<'a> {
    operation: &'a str,
    entity: &'a str,
    line: i64,
}

fn wire(extension: &ShopwareExtension) -> ExtensionWire<'_> {
    ExtensionWire {
        name: &extension.name,
        kind: extension.kind as u8,
        path: &extension.path,
        permissions: (!extension.permissions.is_empty()).then(|| {
            extension
                .permissions
                .iter()
                .map(|permission| PermissionWire {
                    operation: &permission.operation,
                    entity: &permission.entity,
                    line: permission.line,
                })
                .collect()
        }),
    }
}

impl ExtensionCommandProvider {
    pub fn new(index: Arc<ExtensionIndexer>) -> Self {
        Self { index }
    }
}

/// The extensions in repository order, or `null` when there are none.
fn all_extensions(index: &ExtensionIndexer) -> Result<serde_json::Value, BoxError> {
    let extensions = index.get_all()?;
    if extensions.is_empty() {
        return Ok(serde_json::Value::Null);
    }
    let wire: Vec<ExtensionWire<'_>> = extensions.iter().map(wire).collect();
    crate::commands::to_value(&wire)
}

impl CommandProvider for ExtensionCommandProvider {
    fn commands(&self, _ctx: &Context) -> BTreeMap<String, CommandFunc> {
        let index = self.index.clone();
        let all: CommandFunc = Arc::new(move |_ctx, _args| all_extensions(&index));
        BTreeMap::from([("shopware/extension/all".to_owned(), all)])
    }
}
