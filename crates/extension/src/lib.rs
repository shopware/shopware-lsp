//! Shopware extensions: plugin/bundle classes, app manifests, and
//! shopware-cli extension configuration .

mod bundle;
mod config;
mod config_index;
mod config_types;
mod indexer;
mod manifest;
mod types;
mod yaml_decode;

pub use config::{CONFIG_LOCATIONS, configuration_root, default_config, parse_config_tree};
pub use config_index::{ChangeCallback, ConfigIndex};
pub use config_types::*;
pub use indexer::ExtensionIndexer;
pub use manifest::{ManifestMeta, parse_manifest_tree, parse_manifest_xml};
pub use types::{AppPermission, ShopwareExtension, ShopwareExtensionType};
