use serde::{Deserialize, Serialize};

use crate::paths::{dir, join};
use swlsp_uriutil::filepath;

#[derive(Clone, Copy, Debug, Default, PartialEq, Eq, Hash, Serialize, Deserialize)]
pub enum ShopwareExtensionType {
    #[default]
    Bundle = 0,
    App = 1,
}

#[derive(Clone, Debug, Default, PartialEq, Eq, Serialize, Deserialize)]
pub struct ShopwareExtension {
    pub name: String,
    #[serde(rename = "type")]
    pub kind: ShopwareExtensionType,
    /// The plugin class file for bundles, the app root directory for apps.
    pub path: String,
    pub permissions: Vec<AppPermission>,
}

#[derive(Clone, Debug, Default, PartialEq, Eq, Serialize, Deserialize)]
pub struct AppPermission {
    pub operation: String,
    pub entity: String,
    pub line: i64,
}

impl ShopwareExtension {
    pub fn storefront_views_path(&self) -> String {
        join(&[&self.root_path(), "Resources", "views"])
    }

    /// The source root that owns the extension's `Resources` directory.
    /// Bundle paths point at the plugin class while app paths already point
    /// at their root directory.
    pub fn root_path(&self) -> String {
        if self.kind == ShopwareExtensionType::Bundle {
            return dir(&self.path);
        }
        filepath::clean(&self.path)
    }

    pub fn administration_source_path(&self) -> String {
        join(&[
            &self.root_path(),
            "Resources",
            "app",
            "administration",
            "src",
        ])
    }
}
