//! Composer packages that a nested vendor directory duplicates from the
//! workspace root.
//!
//! The root vendor directory must stay indexable: in a Flex project it is
//! where shopware/core actually lives. Extensions checked out with their own
//! composer install carry a second copy of packages the workspace already
//! provides, which would otherwise be indexed as a duplicate of core.
//!
//! A package counts as provided by the workspace when the root vendor
//! directory contains it, or when the root manifest declares it through
//! `name` or `replace` (the monorepo replaces shopware/core and ships it from
//! src/Core).

use std::collections::{HashMap, HashSet};
use std::path::PathBuf;
use std::sync::RwLock;

use crate::path_exclusions::normalize_relative_path;

/// Safe for concurrent scanner and watcher use. [`VendorPackages::default`]
/// reports nothing as duplicated.
#[derive(Debug, Default)]
pub struct VendorPackages {
    root: Option<PathBuf>,
    replaced: HashSet<String>,
    cached: RwLock<HashMap<String, bool>>,
}

#[derive(serde::Deserialize)]
struct VendorManifest {
    #[serde(default)]
    name: serde_json::Value,
    #[serde(default)]
    replace: serde_json::Value,
}

impl VendorPackages {
    /// Reads the root manifest once so later lookups only stat candidate
    /// package directories.
    pub fn new(project_root: &str) -> Self {
        let root = PathBuf::from(crate::paths::clean(project_root));
        let mut packages = Self {
            root: Some(root.clone()),
            ..Self::default()
        };
        let Ok(content) = std::fs::read(root.join("composer.json")) else {
            return packages;
        };
        let Ok(manifest) = serde_json::from_slice::<VendorManifest>(&content) else {
            return packages;
        };
        // A field of the wrong type makes the whole manifest unusable, so no
        // package is derived from a half-understood `composer.json`.
        let name = match &manifest.name {
            serde_json::Value::Null => String::new(),
            serde_json::Value::String(name) => name.clone(),
            _ => return packages,
        };
        let replace = match &manifest.replace {
            serde_json::Value::Null => Vec::new(),
            serde_json::Value::Object(map) => map.keys().cloned().collect(),
            _ => return packages,
        };
        let name = normalize_package_name(&name);
        if !name.is_empty() {
            packages.replaced.insert(name);
        }
        for name in replace {
            let normalized = normalize_package_name(&name);
            if !normalized.is_empty() {
                packages.replaced.insert(normalized);
            }
        }
        packages
    }

    /// Reports whether a workspace-relative path sits inside a nested vendor
    /// package that the workspace root already provides.
    pub fn duplicates_root_package(&self, relative_path: &str) -> bool {
        self.provides(nested_vendor_package(relative_path))
    }

    /// Reports whether a path inside a bundled archive (a PHAR) belongs to a
    /// vendor package the workspace root already provides: tools such as
    /// `phpstan.phar` ship their own copies of polyfills and libraries, which
    /// must not shadow the project's.
    pub fn bundles_root_package(&self, archive_relative_path: &str) -> bool {
        let path = normalize_relative_path(archive_relative_path);
        let mut segments = path.split('/');
        match (
            segments.next(),
            segments.next(),
            segments.next(),
            segments.next(),
        ) {
            (Some("vendor"), Some(vendor), Some(package), Some(_)) => {
                self.provides(format!("{vendor}/{package}"))
            }
            _ => false,
        }
    }

    fn provides(&self, candidate: String) -> bool {
        let Some(root) = &self.root else {
            return false;
        };
        if candidate.is_empty() {
            return false;
        }
        if let Some(duplicate) = crate::store::read_lock(&self.cached).get(&candidate) {
            return *duplicate;
        }
        let duplicate = self.replaced.contains(&candidate)
            || root
                .join("vendor")
                .join(candidate.replace('/', std::path::MAIN_SEPARATOR_STR))
                .exists();
        crate::store::write_lock(&self.cached).insert(candidate, duplicate);
        duplicate
    }
}

/// The vendor-relative package prefix for a path below a vendor directory
/// that is not the workspace root's own, and an empty string otherwise. At
/// most two segments after the vendor directory are kept so lookups stay per
/// package rather than per file.
pub(crate) fn nested_vendor_package(relative_path: &str) -> String {
    let relative_path = normalize_relative_path(relative_path);
    if relative_path.is_empty() {
        return String::new();
    }
    let segments: Vec<&str> = relative_path.split('/').collect();
    for (position, segment) in segments.iter().enumerate() {
        if *segment != "vendor" {
            continue;
        }
        if position == 0 {
            return String::new();
        }
        let remainder = &segments[position + 1..];
        if remainder.is_empty() {
            return String::new();
        }
        return remainder[..remainder.len().min(2)].join("/");
    }
    String::new()
}

fn normalize_package_name(value: &str) -> String {
    value.trim().to_lowercase()
}

#[cfg(test)]
mod tests {
    use super::*;
    use std::path::Path;

    fn fixture(root: &Path, manifest: &str, directories: &[&str]) {
        if !manifest.is_empty() {
            std::fs::write(root.join("composer.json"), manifest).unwrap();
        }
        for directory in directories {
            std::fs::create_dir_all(root.join(directory)).unwrap();
        }
    }

    fn root_str(root: &Path) -> &str {
        root.to_str().unwrap()
    }

    #[test]
    fn nested_vendor_package_cases() {
        for (path, expected) in [
            ("vendor/shopware/core/Foo.php", ""),
            ("vendor", ""),
            (
                "custom/plugins/Swag/vendor/shopware/core/Foo.php",
                "shopware/core",
            ),
            ("custom/plugins/Swag/vendor/shopware/core", "shopware/core"),
            ("custom/plugins/Swag/vendor/shopware", "shopware"),
            ("custom/plugins/Swag/vendor", ""),
            (
                "custom/plugins/Swag/vendor/composer/installed.json",
                "composer/installed.json",
            ),
            ("custom/plugins/Swag/vendor/autoload.php", "autoload.php"),
            (
                r"custom\plugins\Swag\vendor\shopware\core\Foo.php",
                "shopware/core",
            ),
            ("src/Core/Content/Product/ProductDefinition.php", ""),
            ("custom/plugins/Swag/vendors/acme/lib.php", ""),
        ] {
            assert_eq!(nested_vendor_package(path), expected, "{path}");
        }
    }

    #[test]
    fn duplicates_root_package_from_root_vendor() {
        let root = tempfile::tempdir().unwrap();
        fixture(
            root.path(),
            r#"{"name":"acme/shop"}"#,
            &["vendor/shopware/core"],
        );
        let packages = VendorPackages::new(root_str(root.path()));
        assert!(
            packages.duplicates_root_package("custom/plugins/Swag/vendor/shopware/core/Foo.php")
        );
        assert!(!packages.duplicates_root_package("vendor/shopware/core/Foo.php"));
    }

    #[test]
    fn archive_copies_of_root_packages_are_bundled_duplicates() {
        let root = tempfile::tempdir().unwrap();
        fixture(
            root.path(),
            r#"{"name":"acme/shop"}"#,
            &["vendor/symfony/polyfill-php84"],
        );
        let packages = VendorPackages::new(root_str(root.path()));
        assert!(packages.bundles_root_package("vendor/symfony/polyfill-php84/bootstrap.php"));
        // Packages only the archive has, and the archive's own code, stay.
        assert!(!packages.bundles_root_package("vendor/nikic/php-parser/lib/Parser.php"));
        assert!(!packages.bundles_root_package("src/Analyser/Scope.php"));
        assert!(!packages.bundles_root_package("vendor/symfony/polyfill-php84"));
    }

    #[test]
    fn duplicates_root_package_from_replace() {
        let root = tempfile::tempdir().unwrap();
        fixture(
            root.path(),
            r#"{"name":"shopware/platform","replace":{"shopware/core":"self.version"}}"#,
            &[],
        );
        let packages = VendorPackages::new(root_str(root.path()));
        assert!(
            packages.duplicates_root_package("custom/plugins/Swag/vendor/shopware/core/Foo.php")
        );
        assert!(
            packages
                .duplicates_root_package("custom/plugins/Swag/vendor/shopware/platform/Foo.php")
        );
        assert!(
            !packages.duplicates_root_package("custom/plugins/Swag/vendor/acme/pdf/Writer.php")
        );
    }

    #[test]
    fn duplicates_root_package_keeps_extension_only_dependency() {
        let root = tempfile::tempdir().unwrap();
        fixture(
            root.path(),
            r#"{"name":"acme/shop"}"#,
            &["vendor/shopware/core"],
        );
        let packages = VendorPackages::new(root_str(root.path()));
        assert!(
            !packages.duplicates_root_package("custom/plugins/Swag/vendor/acme/pdf/Writer.php")
        );
    }

    #[test]
    fn duplicates_root_package_without_manifest() {
        let root = tempfile::tempdir().unwrap();
        fixture(root.path(), "", &["vendor/shopware/core"]);
        let packages = VendorPackages::new(root_str(root.path()));
        assert!(
            packages.duplicates_root_package("custom/plugins/Swag/vendor/shopware/core/Foo.php")
        );
    }

    #[test]
    fn duplicates_root_package_with_malformed_manifest() {
        let root = tempfile::tempdir().unwrap();
        fixture(root.path(), "{not json", &["vendor/shopware/core"]);
        let packages = VendorPackages::new(root_str(root.path()));
        assert!(
            packages.duplicates_root_package("custom/plugins/Swag/vendor/shopware/core/Foo.php")
        );
    }

    #[test]
    fn duplicates_root_package_default_reports_nothing() {
        let packages = VendorPackages::default();
        assert!(
            !packages.duplicates_root_package("custom/plugins/Swag/vendor/shopware/core/Foo.php")
        );
    }

    #[test]
    fn duplicates_root_package_is_cached() {
        let root = tempfile::tempdir().unwrap();
        fixture(
            root.path(),
            r#"{"name":"acme/shop"}"#,
            &["vendor/shopware/core"],
        );
        let packages = VendorPackages::new(root_str(root.path()));
        assert!(
            packages.duplicates_root_package("custom/plugins/Swag/vendor/shopware/core/Foo.php")
        );
        std::fs::remove_dir_all(root.path().join("vendor/shopware/core")).unwrap();
        assert!(
            packages.duplicates_root_package("custom/plugins/Swag/vendor/shopware/core/Bar.php")
        );
    }
}
