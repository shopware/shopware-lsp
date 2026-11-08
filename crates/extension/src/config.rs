use swlsp_syntax::SyntaxNode;
use swlsp_uriutil::filepath;

use crate::config_types::Config;
use crate::paths::dir;
use crate::yaml_decode::decode_into;

/// shopware-cli's first-existing-file precedence.
pub const CONFIG_LOCATIONS: [&str; 3] = [
    ".config/shopware-extension.yml",
    ".shopware-extension.yml",
    ".shopware-extension.yaml",
];

const DEFAULT_COMPATIBILITY_DATE: &str = "2026-02-11";

pub fn default_config() -> Config {
    let mut config = Config {
        compatibility_date: DEFAULT_COMPATIBILITY_DATE.to_owned(),
        ..Config::default()
    };
    config.build.zip.assets.enabled = true;
    config.build.zip.composer.enabled = true;
    config
}

/// Extracts the complete CLI extension configuration from a YAML tree.
/// Decoding errors are kept as data so incomplete editor input still
/// publishes a useful snapshot instead of falling back to a lower-precedence
/// file.
pub fn parse_config_tree(path: &str, root: &SyntaxNode) -> Config {
    let mut config = default_config();
    config.storage_location = filepath::clean(path);
    if let Some(error) = decode_into(root, &mut config) {
        config.decode_error = error;
    }
    if config.compatibility_date.is_empty() {
        config.compatibility_date = DEFAULT_COMPATIBILITY_DATE.to_owned();
    }
    config
}

/// The extension root of a supported configuration location, without
/// touching the disk.
pub fn configuration_root(path: &str) -> Option<String> {
    let path = filepath::clean(path);
    match filepath::base(&path).as_str() {
        ".shopware-extension.yml" | ".shopware-extension.yaml" => Some(dir(&path)),
        "shopware-extension.yml" => {
            let directory = dir(&path);
            (filepath::base(&directory) == ".config").then(|| dir(&directory))
        }
        _ => None,
    }
}

#[cfg(test)]
mod tests;
