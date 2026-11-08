use std::collections::BTreeSet;
use std::path::Path;
use std::sync::Arc;

use swlsp_extension::{ExtensionIndexer, ShopwareExtension, ShopwareExtensionType};
use swlsp_uriutil::filepath;

const ICON_DIRECTORY: [&str; 6] = ["Resources", "app", "storefront", "dist", "assets", "icon"];

/// Lists Storefront icon packs and icons of indexed extensions and the
/// Shopware Storefront itself.
pub struct IconProvider {
    project_root: String,
    extension_indexer: Arc<ExtensionIndexer>,
}

impl IconProvider {
    pub fn new(project_root: impl Into<String>, extension_indexer: Arc<ExtensionIndexer>) -> Self {
        Self {
            project_root: project_root.into(),
            extension_indexer,
        }
    }

    fn extensions(&self) -> Vec<ShopwareExtension> {
        let mut extensions: Vec<ShopwareExtension> = self
            .extension_indexer
            .get_all()
            .map(|all| all.to_vec())
            .unwrap_or_default();
        let defaults = [
            filepath::join(&[self.project_root.as_str(), "src", "Storefront"]),
            filepath::join(&[
                self.project_root.as_str(),
                "vendor",
                "shopware",
                "storefront",
            ]),
            filepath::join(&[
                self.project_root.as_str(),
                "vendor",
                "shopware",
                "platform",
                "src",
                "Storefront",
            ]),
        ];
        // Only the first existing default Storefront is added.
        if let Some(path) = defaults.into_iter().find(|path| Path::new(path).exists()) {
            extensions.push(ShopwareExtension {
                name: "Storefront".to_owned(),
                kind: ShopwareExtensionType::Bundle,
                path,
                permissions: Vec::new(),
            });
        }
        extensions
    }

    fn icon_directory(extension: &ShopwareExtension, rest: &[&str]) -> String {
        // Bundle extensions point at their class file; `Resources/` is next
        // to it.
        let base = if filepath::ext(&extension.path).eq_ignore_ascii_case(".php") {
            filepath::dir(&extension.path)
        } else {
            extension.path.clone()
        };
        let mut parts = vec![base.as_str()];
        parts.extend(ICON_DIRECTORY);
        parts.extend(rest);
        filepath::join(&parts)
    }

    /// Distinct icon pack directory names, sorted.
    pub fn icon_packs(&self) -> Vec<String> {
        let mut packs = BTreeSet::new();
        for extension in self.extensions() {
            let Ok(entries) = std::fs::read_dir(Self::icon_directory(&extension, &[])) else {
                continue;
            };
            for entry in entries.flatten() {
                if entry.file_type().is_ok_and(|kind| kind.is_dir()) {
                    packs.insert(entry.file_name().to_string_lossy().into_owned());
                }
            }
        }
        packs.into_iter().collect()
    }

    /// Distinct icon names (`.svg` files without extension) of `pack`, sorted.
    pub fn icons(&self, pack: &str) -> Vec<String> {
        let mut icons = BTreeSet::new();
        for extension in self.extensions() {
            let Ok(entries) = std::fs::read_dir(Self::icon_directory(&extension, &[pack])) else {
                continue;
            };
            for entry in entries.flatten() {
                if entry.file_type().is_ok_and(|kind| kind.is_dir()) {
                    continue;
                }
                let name = entry.file_name().to_string_lossy().into_owned();
                if let Some(icon) = name.strip_suffix(".svg") {
                    icons.insert(icon.to_owned());
                }
            }
        }
        icons.into_iter().collect()
    }

    /// The first existing file of `icon` in `pack`, or an empty string.
    pub fn icon(&self, pack: &str, icon: &str) -> String {
        let file = format!("{icon}.svg");
        self.extensions()
            .iter()
            .map(|extension| Self::icon_directory(extension, &[pack, &file]))
            .find(|path| Path::new(path).exists())
            .unwrap_or_default()
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn lists_icon_packs_from_default_storefront_and_extensions() {
        let project = tempfile::tempdir().unwrap();
        let root = project.path().to_string_lossy().into_owned();
        let storefront = format!("{root}/src/Storefront/Resources/app/storefront/dist/assets/icon");
        std::fs::create_dir_all(format!("{storefront}/default")).unwrap();
        std::fs::write(format!("{storefront}/default/arrow.svg"), "<svg/>").unwrap();
        std::fs::write(format!("{storefront}/default/readme.txt"), "").unwrap();
        let plugin = format!("{root}/custom/plugins/Demo/src");
        let plugin_icons = format!("{plugin}/Resources/app/storefront/dist/assets/icon");
        std::fs::create_dir_all(format!("{plugin_icons}/custom")).unwrap();
        std::fs::create_dir_all(format!("{plugin_icons}/default")).unwrap();
        std::fs::write(format!("{plugin_icons}/default/arrow.svg"), "<svg/>").unwrap();
        std::fs::write(format!("{plugin_icons}/custom/star.svg"), "<svg/>").unwrap();

        let cache = tempfile::tempdir().unwrap();
        let indexer = Arc::new(ExtensionIndexer::new(cache.path(), None).unwrap());
        indexer
            .index(&swlsp_indexer::ParsedFile::new(
                format!("{plugin}/Demo.php"),
                b"<?php\nnamespace Demo;\nclass Demo extends \\Shopware\\Core\\Framework\\Plugin {}\n".to_vec(),
            ))
            .unwrap();
        // Bundle paths point at the class file; its `Resources/` sibling
        // holds the plugin icons.
        let provider = IconProvider::new(root.clone(), indexer.clone());
        assert_eq!(provider.icon_packs(), ["custom", "default"]);
        assert_eq!(provider.icons("default"), ["arrow"]);
        assert_eq!(provider.icons("custom"), ["star"]);
        assert_eq!(
            provider.icon("custom", "star"),
            format!("{plugin_icons}/custom/star.svg")
        );
        // Extension icons override the default Storefront icon of the same
        // pack and name.
        assert_eq!(
            provider.icon("default", "arrow"),
            format!("{plugin_icons}/default/arrow.svg")
        );
        assert_eq!(provider.icon("default", "missing"), "");
        use swlsp_indexer::Indexer;
        indexer.close().unwrap();
    }
}
