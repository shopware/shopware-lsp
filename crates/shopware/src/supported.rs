//! The oldest Shopware version the code of a file has to run on.
//!
//! A project runs on the one version it has installed. An extension states
//! the versions it supports in its `composer.json`
//! (`"shopware/core": "~6.6.0 || ~6.7.0"`), and its code has to work on the
//! oldest of them: a rewrite to what only a newer version offers would break
//! the others.

use std::path::{Path, PathBuf};
use std::sync::Arc;

use serde::Deserialize;
use swlsp_php_project::{Version, parse_version_constraint};

use crate::version::{ResolvedVersion, VersionSource};

const SHOPWARE_PACKAGES: &[&str] = &[
    "shopware/core",
    "shopware/platform",
    "shopware/administration",
    "shopware/storefront",
];
const EXTENSION_TYPES: &[&str] = &["shopware-platform-plugin", "shopware-bundle"];
/// Where extensions live below a project root.
const EXTENSION_DIRECTORIES: &[&str] = &["custom/plugins", "custom/static-plugins"];

/// The installed Shopware version and, per extension, the oldest version
/// its `composer.json` supports. Read once; cheap to clone.
#[derive(Clone, Debug, Default)]
pub struct SupportedVersions {
    installed: ResolvedVersion,
    /// Extension roots with their oldest supported version.
    extensions: Arc<[(PathBuf, Version)]>,
}

impl From<ResolvedVersion> for SupportedVersions {
    /// No extension states its own range: every file runs on `installed`.
    fn from(installed: ResolvedVersion) -> Self {
        Self {
            installed,
            extensions: Arc::from([]),
        }
    }
}

#[derive(Deserialize, Default)]
struct ExtensionComposer {
    #[serde(default, rename = "type")]
    kind: Option<String>,
    #[serde(default)]
    require: std::collections::BTreeMap<String, serde_json::Value>,
}

impl SupportedVersions {
    /// Reads the `composer.json` of the root, when the root is an extension
    /// itself, and of the extensions below it. A file that cannot be read
    /// or says nothing usable is skipped.
    pub fn discover(root: impl AsRef<Path>, installed: ResolvedVersion) -> Self {
        let root = root.as_ref();
        let mut candidates = vec![root.to_path_buf()];
        for directory in EXTENSION_DIRECTORIES {
            let Ok(entries) = std::fs::read_dir(root.join(directory)) else {
                continue;
            };
            candidates.extend(entries.flatten().map(|entry| entry.path()));
        }
        candidates.sort();
        let extensions: Vec<(PathBuf, Version)> = candidates
            .into_iter()
            .filter_map(|directory| {
                let content = std::fs::read(directory.join("composer.json")).ok()?;
                Some((directory, minimum_in_composer(&content)?))
            })
            .collect();
        Self {
            installed,
            extensions: Arc::from(extensions),
        }
    }

    /// The version the project has installed or was told to target.
    pub fn installed(&self) -> ResolvedVersion {
        self.installed
    }

    /// The oldest version the file at `path` has to run on: the lower
    /// bound of the extension owning it, when that is older than the
    /// installed version or nothing is installed. An explicitly configured
    /// target version always wins.
    pub fn minimum_for(&self, path: impl AsRef<Path>) -> ResolvedVersion {
        if self.installed.source == VersionSource::Explicit {
            return self.installed;
        }
        let path = path.as_ref();
        let Some((_, minimum)) = self
            .extensions
            .iter()
            .filter(|(root, _)| path.starts_with(root))
            .max_by_key(|(root, _)| root.as_os_str().len())
        else {
            return self.installed;
        };
        if self.installed.known && self.installed.version <= *minimum {
            return self.installed;
        }
        ResolvedVersion {
            version: *minimum,
            source: VersionSource::ExtensionConstraint,
            known: true,
        }
    }

    /// [`Self::minimum_for`] for a document URI.
    pub fn minimum_for_uri(&self, uri: &str) -> ResolvedVersion {
        match swlsp_uriutil::path(uri) {
            Ok(path) => self.minimum_for(path),
            Err(_) => self.installed,
        }
    }
}

/// The oldest Shopware version an extension's `composer.json` supports;
/// `None` for anything but an extension, or without a usable constraint.
fn minimum_in_composer(content: &[u8]) -> Option<Version> {
    let composer: ExtensionComposer = serde_json::from_slice(content).ok()?;
    if !EXTENSION_TYPES.contains(&composer.kind.as_deref().unwrap_or("").trim()) {
        return None;
    }
    SHOPWARE_PACKAGES
        .iter()
        .find_map(|package| composer.require.get(*package))
        .and_then(|constraint| constraint.as_str())
        .and_then(constraint_lower_bound)
}

/// The lowest version a Composer constraint allows: the smallest lower
/// bound of its `||` alternatives. `None` when an alternative has none
/// (`*`, `<6.7`, a branch), since then any version may be meant.
pub fn constraint_lower_bound(constraint: &str) -> Option<Version> {
    let mut lowest: Option<Version> = None;
    for alternative in constraint.split('|').filter(|part| !part.trim().is_empty()) {
        // A hyphen range starts with its lower bound.
        let alternative = alternative.split(" - ").next().unwrap_or(alternative);
        let bound = alternative
            .split([' ', ','])
            .map(str::trim)
            .filter(|part| !part.is_empty() && !part.starts_with('<') && !part.starts_with("!="))
            .filter_map(parse_version_constraint)
            .min()?;
        lowest = Some(lowest.map_or(bound, |current| current.min(bound)));
    }
    lowest
}

#[cfg(test)]
mod tests {
    use super::*;

    fn version(major: i64, minor: i64, patch: i64) -> Version {
        Version::new(major, minor, patch)
    }

    fn installed(minor: i64, source: VersionSource) -> ResolvedVersion {
        ResolvedVersion {
            version: version(6, minor, 16),
            source,
            known: true,
        }
    }

    #[test]
    fn the_lower_bound_is_the_smallest_of_the_alternatives() {
        for (constraint, bound) in [
            ("~6.6.0 || ~6.7.0", Some(version(6, 6, 0))),
            ("~6.7.0||~6.6.4", Some(version(6, 6, 4))),
            ("^6.7", Some(version(6, 7, 0))),
            (">=6.5.8 <6.8", Some(version(6, 5, 8))),
            (">=6.6,<6.7.2", Some(version(6, 6, 0))),
            ("6.6.0 - 6.7.5", Some(version(6, 6, 0))),
            ("6.7.1.0", Some(version(6, 7, 1))),
            ("v6.6.10.2 | ~6.7", Some(version(6, 6, 10))),
            // Any version may be meant.
            ("*", None),
            ("<6.7", None),
            ("~6.6.0 || dev-trunk", None),
            ("", None),
        ] {
            assert_eq!(constraint_lower_bound(constraint), bound, "{constraint}");
        }
    }

    #[test]
    fn an_extension_runs_on_the_oldest_version_it_supports() {
        let directory = tempfile::tempdir().unwrap();
        let root = directory.path();
        let write = |path: &str, content: &str| {
            let path = root.join(path);
            std::fs::create_dir_all(path.parent().unwrap()).unwrap();
            std::fs::write(path, content).unwrap();
        };
        // A shop: its own constraint says nothing, the lock file does.
        write(
            "composer.json",
            r#"{"type":"project","require":{"shopware/core":"~6.5.0"}}"#,
        );
        write(
            "custom/plugins/Both/composer.json",
            r#"{"type":"shopware-platform-plugin","require":{"shopware/core":"~6.6.0 || ~6.7.0"}}"#,
        );
        write(
            "custom/plugins/Current/composer.json",
            r#"{"type":"shopware-platform-plugin","require":{"shopware/core":"~6.7.0"}}"#,
        );
        write(
            "custom/static-plugins/Next/composer.json",
            r#"{"type":"shopware-platform-plugin","require":{"shopware/core":"~6.8.0"}}"#,
        );
        write(
            "custom/plugins/Any/composer.json",
            r#"{"type":"shopware-platform-plugin","require":{"shopware/core":"*"}}"#,
        );
        write("custom/plugins/Broken/composer.json", "{");
        write(
            "custom/plugins/Library/composer.json",
            r#"{"type":"library","require":{"shopware/core":"~6.4.0"}}"#,
        );
        let lock = installed(7, VersionSource::ComposerLock);
        let versions = SupportedVersions::discover(root, lock);
        let minimum = |path: &str| {
            let resolved = versions.minimum_for(root.join(path));
            (resolved.version, resolved.source, resolved.known)
        };
        assert_eq!(
            minimum("custom/plugins/Both/src/Service.php"),
            (version(6, 6, 0), VersionSource::ExtensionConstraint, true)
        );
        // Also within a minor: the plugin has to run on its first patch.
        assert_eq!(
            minimum("custom/plugins/Current/src/Service.php"),
            (version(6, 7, 0), VersionSource::ExtensionConstraint, true)
        );
        // A later range, no range, no extension: the installed version.
        for path in [
            "custom/static-plugins/Next/src/Service.php",
            "custom/plugins/Any/src/Service.php",
            "custom/plugins/Broken/src/Service.php",
            "custom/plugins/Library/src/Service.php",
            "custom/plugins/BothOther/src/Service.php",
            "src/Service.php",
        ] {
            assert_eq!(minimum(path), (lock.version, lock.source, true), "{path}");
        }
        assert_eq!(versions.installed(), lock);

        // A configured target version is a decision: it applies everywhere.
        let explicit = installed(7, VersionSource::Explicit);
        let versions = SupportedVersions::discover(root, explicit);
        assert_eq!(
            versions.minimum_for(root.join("custom/plugins/Both/src/Service.php")),
            explicit
        );

        // An extension checked on its own, with nothing installed.
        let plugin = root.join("custom/plugins/Both");
        let versions = SupportedVersions::discover(&plugin, ResolvedVersion::default());
        let resolved = versions.minimum_for(plugin.join("src/Service.php"));
        assert_eq!((resolved.version, resolved.known), (version(6, 6, 0), true));
        assert!(
            !SupportedVersions::from(ResolvedVersion::default())
                .minimum_for("/a")
                .known
        );
    }
}
