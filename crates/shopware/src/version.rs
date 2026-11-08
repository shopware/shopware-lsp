//! Resolution of the Shopware version a workspace targets.

use std::collections::BTreeMap;
use std::fmt;
use std::path::{Path, PathBuf};
use std::sync::LazyLock;

use regex::Regex;
use serde::Deserialize;
use swlsp_php_project::{Model, Version, parse_version_constraint};

/// The evidence a version was resolved from.
#[derive(Clone, Copy, Debug, Default, PartialEq, Eq, Hash)]
pub enum VersionSource {
    /// No evidence (Go's empty string).
    #[default]
    None,
    Explicit,
    ComposerLock,
    PlatformComposer,
    PlatformKernel,
    PlatformBranchAlias,
}

impl VersionSource {
    /// The Go string value.
    pub fn as_str(self) -> &'static str {
        match self {
            VersionSource::None => "",
            VersionSource::Explicit => "explicit",
            VersionSource::ComposerLock => "composer-lock",
            VersionSource::PlatformComposer => "platform-composer",
            VersionSource::PlatformKernel => "platform-kernel",
            VersionSource::PlatformBranchAlias => "platform-branch-alias",
        }
    }
}

impl fmt::Display for VersionSource {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        f.write_str(self.as_str())
    }
}

/// A Shopware version and the evidence used to select it. `known` is false
/// when the workspace does not provide enough stable evidence; callers
/// should disable version-gated behavior in that case.
#[derive(Clone, Copy, Debug, Default, PartialEq, Eq)]
pub struct ResolvedVersion {
    pub version: Version,
    pub source: VersionSource,
    pub known: bool,
}

impl ResolvedVersion {
    pub fn at_least(&self, major: i64, minor: i64, patch: i64) -> bool {
        self.known && self.version.at_least_patch(major, minor, patch)
    }

    fn known(version: Version, source: VersionSource) -> Self {
        ResolvedVersion {
            version,
            source,
            known: true,
        }
    }
}

/// A version resolution failure.
#[derive(Clone, Debug, PartialEq, Eq)]
pub struct VersionError(String);

impl fmt::Display for VersionError {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        f.write_str(&self.0)
    }
}

impl std::error::Error for VersionError {}

/// Resolves the targeted Shopware version from, in order: an explicit
/// target, the installed `shopware/core` (or `shopware/platform`) package,
/// and a `shopware/platform` checkout's composer version, kernel fallback
/// constant, or consistent branch aliases.
#[derive(Clone, Debug)]
pub struct VersionResolver<'a> {
    root: PathBuf,
    project: Option<&'a Model>,
    explicit_target: String,
}

static EXPLICIT_VERSION: LazyLock<Regex> = LazyLock::new(|| {
    Regex::new(r"(?i)^v?([0-9]+)\.([0-9]+)(?:\.([0-9]+))?(?:\.[0-9]+)?(?:[-+][0-9a-z.-]+)?$")
        .unwrap()
});

static KERNEL_FALLBACK_VERSION: LazyLock<regex::bytes::Regex> = LazyLock::new(|| {
    regex::bytes::Regex::new(
        r#"(?m-u)^[\t ]*(?:(?:final|public|protected|private)[\t ]+)*const[\t ]+SHOPWARE_FALLBACK_VERSION[\t ]*=[\t ]*['"]([^'"]+)['"]"#,
    )
    .unwrap()
});

#[derive(Deserialize, Default)]
struct PlatformComposer {
    #[serde(default)]
    name: Option<String>,
    #[serde(default)]
    version: Option<String>,
    #[serde(default)]
    extra: Option<PlatformExtra>,
}

#[derive(Deserialize, Default)]
struct PlatformExtra {
    #[serde(default, rename = "branch-alias")]
    branch_alias: Option<BTreeMap<String, String>>,
}

impl<'a> VersionResolver<'a> {
    pub fn new(
        root: impl Into<PathBuf>,
        project: Option<&'a Model>,
        explicit_target: &str,
    ) -> Self {
        VersionResolver {
            root: root.into(),
            project,
            explicit_target: explicit_target.trim().to_owned(),
        }
    }

    pub fn resolve(&self) -> Result<ResolvedVersion, VersionError> {
        if !self.explicit_target.is_empty() {
            let Some(version) = parse_explicit_version(&self.explicit_target) else {
                return Err(VersionError(format!(
                    "resolve Shopware version: invalid explicit target {:?}; expected major.minor[.patch[.build]]",
                    self.explicit_target
                )));
            };
            return Ok(ResolvedVersion::known(version, VersionSource::Explicit));
        }
        if let Some(project) = self.project
            && let Some(version) =
                project.dependency_version(&["shopware/core", "shopware/platform"])
        {
            return Ok(ResolvedVersion::known(version, VersionSource::ComposerLock));
        }
        self.resolve_platform_checkout()
    }

    fn resolve_platform_checkout(&self) -> Result<ResolvedVersion, VersionError> {
        if self.root.as_os_str().to_string_lossy().trim().is_empty() {
            return Ok(ResolvedVersion::default());
        }
        let content = match std::fs::read(self.root.join("composer.json")) {
            Ok(content) => content,
            Err(err) if err.kind() == std::io::ErrorKind::NotFound => {
                return Ok(ResolvedVersion::default());
            }
            Err(err) => {
                return Err(VersionError(format!(
                    "resolve Shopware version: read root composer.json: {err}"
                )));
            }
        };
        let composer: PlatformComposer = serde_json::from_slice(&content).map_err(|err| {
            VersionError(format!(
                "resolve Shopware version: parse root composer.json: {err}"
            ))
        })?;
        let name = composer.name.unwrap_or_default();
        if !swlsp_php_types::equal_fold(name.trim(), "shopware/platform") {
            return Ok(ResolvedVersion::default());
        }
        if let Some(version) = parse_version_constraint(composer.version.as_deref().unwrap_or("")) {
            return Ok(ResolvedVersion::known(
                version,
                VersionSource::PlatformComposer,
            ));
        }
        if let Some(version) = platform_kernel_version(&self.root)? {
            return Ok(ResolvedVersion::known(
                version,
                VersionSource::PlatformKernel,
            ));
        }
        let aliases = composer
            .extra
            .and_then(|extra| extra.branch_alias)
            .unwrap_or_default();
        if let Some(version) = consistent_branch_alias_version(&aliases) {
            return Ok(ResolvedVersion::known(
                version,
                VersionSource::PlatformBranchAlias,
            ));
        }
        Ok(ResolvedVersion::default())
    }
}

fn parse_explicit_version(source: &str) -> Option<Version> {
    let captures = EXPLICIT_VERSION.captures(source.trim())?;
    let major = captures[1].parse().ok()?;
    let minor = captures[2].parse().ok()?;
    let patch = match captures.get(3) {
        Some(patch) => patch.as_str().parse().ok()?,
        None => 0,
    };
    Some(Version::new(major, minor, patch))
}

fn platform_kernel_version(root: &Path) -> Result<Option<Version>, VersionError> {
    let content = match std::fs::read(root.join("src").join("Core").join("Kernel.php")) {
        Ok(content) => content,
        Err(err) if err.kind() == std::io::ErrorKind::NotFound => return Ok(None),
        Err(err) => {
            return Err(VersionError(format!(
                "resolve Shopware version: read platform Kernel.php: {err}"
            )));
        }
    };
    let Some(captures) = KERNEL_FALLBACK_VERSION.captures(&content) else {
        return Ok(None);
    };
    Ok(parse_version_constraint(&String::from_utf8_lossy(
        &captures[1],
    )))
}

fn consistent_branch_alias_version(aliases: &BTreeMap<String, String>) -> Option<Version> {
    let mut selected: Option<Version> = None;
    for alias in aliases.values() {
        let Some(version) = parse_version_constraint(alias) else {
            continue;
        };
        match selected {
            None => selected = Some(version),
            Some(current) if current != version => return None,
            Some(_) => {}
        }
    }
    selected
}

#[cfg(test)]
mod tests {
    use super::*;
    use swlsp_php_project::Package;

    fn package(name: &str, version: &str) -> Package {
        Package {
            name: name.to_owned(),
            version: version.to_owned(),
            ..Package::default()
        }
    }

    fn model(packages: Vec<Package>) -> Model {
        Model {
            dependencies: packages,
            ..Model::default()
        }
    }

    fn write(root: &Path, relative: &str, content: &str) {
        let path = root.join(relative);
        std::fs::create_dir_all(path.parent().unwrap()).unwrap();
        std::fs::write(path, content).unwrap();
    }

    #[test]
    fn prefers_explicit_target() {
        let root = tempfile::tempdir().unwrap();
        let model = model(vec![package("shopware/core", "v6.6.10.0")]);
        let resolved = VersionResolver::new(root.path(), Some(&model), "v6.8.1.0-dev")
            .resolve()
            .unwrap();
        assert_eq!(
            resolved,
            ResolvedVersion {
                version: Version::new(6, 8, 1),
                source: VersionSource::Explicit,
                known: true,
            }
        );
    }

    #[test]
    fn rejects_invalid_explicit_target() {
        let root = tempfile::tempdir().unwrap();
        let error = VersionResolver::new(root.path(), None, "next")
            .resolve()
            .unwrap_err();
        assert!(
            error
                .to_string()
                .contains("expected major.minor[.patch[.build]]")
        );
    }

    #[test]
    fn uses_installed_composer_package() {
        let root = tempfile::tempdir().unwrap();
        let model = model(vec![
            package("vendor/package", "1.0.0"),
            package("shopware/core", "v6.7.3.1"),
        ]);
        let resolved = VersionResolver::new(root.path(), Some(&model), "")
            .resolve()
            .unwrap();
        assert_eq!(resolved.version, Version::new(6, 7, 3));
        assert_eq!(resolved.source, VersionSource::ComposerLock);
        assert!(resolved.known);
        assert!(resolved.at_least(6, 7, 3));
        assert!(!resolved.at_least(6, 7, 4));
    }

    #[test]
    fn uses_platform_composer_version() {
        let root = tempfile::tempdir().unwrap();
        write(
            root.path(),
            "composer.json",
            r#"{
  "name": "shopware/platform",
  "version": "6.6.10.0"
}"#,
        );
        let resolved = VersionResolver::new(root.path(), None, "")
            .resolve()
            .unwrap();
        assert_eq!(resolved.version, Version::new(6, 6, 10));
        assert_eq!(resolved.source, VersionSource::PlatformComposer);
        assert!(resolved.known);
    }

    #[test]
    fn uses_platform_kernel_fallback() {
        let root = tempfile::tempdir().unwrap();
        write(
            root.path(),
            "composer.json",
            r#"{"name":"shopware/platform"}"#,
        );
        write(
            root.path(),
            "src/Core/Kernel.php",
            "<?php
// const SHOPWARE_FALLBACK_VERSION = '1.0.0';
final class Kernel
{
    final public const SHOPWARE_FALLBACK_VERSION = '6.7.9999999-dev';
}",
        );
        let resolved = VersionResolver::new(root.path(), None, "")
            .resolve()
            .unwrap();
        assert_eq!(resolved.version, Version::new(6, 7, 9_999_999));
        assert_eq!(resolved.source, VersionSource::PlatformKernel);
        assert!(resolved.known);
    }

    #[test]
    fn uses_consistent_platform_branch_aliases() {
        let root = tempfile::tempdir().unwrap();
        write(
            root.path(),
            "composer.json",
            r#"{
  "name": "shopware/platform",
  "extra": {"branch-alias": {
    "dev-main": "6.7.x-dev",
    "dev-trunk": "6.7.x-dev"
  }}
}"#,
        );
        let resolved = VersionResolver::new(root.path(), None, "")
            .resolve()
            .unwrap();
        assert_eq!(resolved.version, Version::new(6, 7, 0));
        assert_eq!(resolved.source, VersionSource::PlatformBranchAlias);
        assert!(resolved.known);
    }

    #[test]
    fn leaves_ambiguous_aliases_unknown() {
        let root = tempfile::tempdir().unwrap();
        write(
            root.path(),
            "composer.json",
            r#"{
  "name": "shopware/platform",
  "extra": {"branch-alias": {
    "dev-main": "6.7.x-dev",
    "dev-next": "6.8.x-dev"
  }}
}"#,
        );
        let resolved = VersionResolver::new(root.path(), None, "")
            .resolve()
            .unwrap();
        assert!(!resolved.known);
        assert!(!resolved.at_least(1, 0, 0));
    }

    #[test]
    fn leaves_non_platform_root_unknown() {
        let root = tempfile::tempdir().unwrap();
        write(root.path(), "composer.json", r#"{"name":"vendor/plugin"}"#);
        write(
            root.path(),
            "src/Core/Kernel.php",
            "<?php
final class Kernel {
    public const SHOPWARE_FALLBACK_VERSION = '6.8.0.0';
}",
        );
        let resolved = VersionResolver::new(root.path(), None, "")
            .resolve()
            .unwrap();
        assert!(!resolved.known);
    }

    #[test]
    fn leaves_missing_composer_unknown() {
        let root = tempfile::tempdir().unwrap();
        let resolved = VersionResolver::new(root.path(), None, "")
            .resolve()
            .unwrap();
        assert!(!resolved.known);
        assert_eq!(resolved.source.as_str(), "");
    }

    #[test]
    fn reports_malformed_platform_composer() {
        let root = tempfile::tempdir().unwrap();
        write(root.path(), "composer.json", "{");
        let error = VersionResolver::new(root.path(), None, "")
            .resolve()
            .unwrap_err();
        assert!(error.to_string().contains("parse root composer.json"));
    }

    #[test]
    fn explicit_version_syntax() {
        assert_eq!(parse_explicit_version("6.5"), Some(Version::new(6, 5, 0)));
        assert_eq!(
            parse_explicit_version("V6.5.8.2+build.1"),
            Some(Version::new(6, 5, 8))
        );
        assert_eq!(parse_explicit_version("6"), None);
        assert_eq!(parse_explicit_version("6.5.x"), None);
    }
}
