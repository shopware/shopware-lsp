//! Detects whether a workspace is a Shopware or Symfony project the language
//! server supports.

mod composer;
mod manifest;

pub use composer::ComposerError;
pub use manifest::ManifestError;

use std::collections::HashSet;
use std::fmt;
use std::io;

use serde::{Deserialize, Serialize};
use swlsp_projectconfig::PROJECT_RELATIVE_PATH;
use swlsp_uriutil::filepath;

/// The detected project kind.
#[derive(Debug, Clone, Copy, PartialEq, Eq, Hash, Default, Serialize, Deserialize)]
#[serde(rename_all = "lowercase")]
pub enum Kind {
    #[default]
    Unknown,
    Configured,
    Symfony,
    Shopware,
}

impl Kind {
    pub fn as_str(self) -> &'static str {
        match self {
            Kind::Unknown => "unknown",
            Kind::Configured => "configured",
            Kind::Symfony => "symfony",
            Kind::Shopware => "shopware",
        }
    }
}

impl fmt::Display for Kind {
    fn fmt(&self, formatter: &mut fmt::Formatter<'_>) -> fmt::Result {
        formatter.write_str(self.as_str())
    }
}

/// One file-backed reason for a detection result.
#[derive(Debug, Clone, PartialEq, Eq, Hash, Serialize, Deserialize)]
pub struct Evidence {
    pub path: String,
    pub reason: String,
}

impl Evidence {
    fn new(path: &str, reason: impl Into<String>) -> Self {
        Self {
            path: path.to_owned(),
            reason: reason.into(),
        }
    }
}

/// The detection result.
#[derive(Debug, Clone, PartialEq, Eq, Default, Serialize, Deserialize)]
pub struct DetectResult {
    pub supported: bool,
    pub kind: Kind,
    pub evidence: Vec<Evidence>,
}

/// A detection failure.
#[derive(Debug, thiserror::Error)]
pub enum DetectError {
    #[error("resolve project root: {0}")]
    ResolveRoot(#[source] io::Error),
    #[error("inspect project root: {0}")]
    InspectRoot(#[source] io::Error),
    #[error("project root is not a directory: {0}")]
    NotDirectory(String),
    /// A project file exists but could not be inspected or read.
    #[error("{operation} {file}: {source}")]
    File {
        operation: &'static str,
        file: String,
        #[source]
        source: io::Error,
    },
    #[error("parse composer.json: {0}")]
    ComposerJson(#[source] ComposerError),
    #[error("parse composer.lock: {0}")]
    ComposerLock(#[source] ComposerError),
    #[error("parse manifest.xml: {0}")]
    Manifest(#[source] ManifestError),
}

const SHOPWARE_PACKAGES: &[&str] = &[
    "shopware/core",
    "shopware/platform",
    "shopware/production",
    "shopware/development",
];

/// Classifies the project at `root`. Shopware evidence wins over Symfony,
/// which wins over an explicit configuration file. Malformed metadata is an
/// error unless other evidence already proves support.
pub fn detect(root: &str) -> Result<DetectResult, DetectError> {
    let absolute = filepath::abs(root).map_err(DetectError::ResolveRoot)?;
    let metadata = std::fs::metadata(&absolute).map_err(DetectError::InspectRoot)?;
    if !metadata.is_dir() {
        return Err(DetectError::NotDirectory(absolute));
    }
    let join =
        |relative: &str| filepath::join(&[absolute.as_str(), &filepath::from_slash(relative)]);

    let mut configured = Vec::new();
    let mut shopware = Vec::new();
    let mut symfony = Vec::new();
    if regular_file(&join(PROJECT_RELATIVE_PATH))? {
        configured.push(Evidence::new(
            PROJECT_RELATIVE_PATH,
            "explicit Shopware LSP configuration",
        ));
    }

    if let Some(content) = read_optional(&join("composer.json"))? {
        match composer::parse_manifest(&content) {
            Ok(manifest) => {
                shopware.extend(composer_shopware_evidence(&manifest));
                symfony.extend(composer_symfony_evidence(&manifest));
            }
            Err(error) => {
                if let Some(result) = existing_support(&[], &[], &configured) {
                    return Ok(result);
                }
                return Err(DetectError::ComposerJson(error));
            }
        }
    }
    if let Some(result) = existing_support(&shopware, &[], &[]) {
        return Ok(result);
    }

    if let Some(content) = read_optional(&join("manifest.xml"))? {
        match manifest::is_shopware_manifest(&content) {
            Ok(true) => shopware.push(Evidence::new(
                "manifest.xml",
                "Shopware app manifest schema",
            )),
            Ok(false) => {}
            Err(error) => {
                if let Some(result) = existing_support(&shopware, &symfony, &configured) {
                    return Ok(result);
                }
                return Err(DetectError::Manifest(error));
            }
        }
    }
    if let Some(result) = existing_support(&shopware, &[], &[]) {
        return Ok(result);
    }

    if let Some(content) = read_optional(&join("config/bundles.php"))?
        && contains_framework_bundle(&content)
    {
        symfony.push(Evidence::new(
            "config/bundles.php",
            "registers Symfony FrameworkBundle",
        ));
    }

    if shopware.is_empty()
        && symfony.is_empty()
        && let Some(content) = read_optional(&join("composer.lock"))?
    {
        let lock = match composer::parse_lock(&content) {
            Ok(lock) => lock,
            Err(error) => {
                if let Some(result) = existing_support(&shopware, &symfony, &configured) {
                    return Ok(result);
                }
                return Err(DetectError::ComposerLock(error));
            }
        };
        for name in lock.packages.iter().chain(&lock.packages_dev) {
            let name = normalized_package(name);
            if SHOPWARE_PACKAGES.contains(&name.as_str()) {
                shopware.push(Evidence::new("composer.lock", format!("contains {name}")));
            } else if name == "symfony/framework-bundle" {
                symfony.push(Evidence::new(
                    "composer.lock",
                    "contains symfony/framework-bundle",
                ));
            }
        }
    }

    Ok(
        existing_support(&shopware, &symfony, &configured).unwrap_or(DetectResult {
            supported: false,
            kind: Kind::Unknown,
            evidence: Vec::new(),
        }),
    )
}

fn composer_shopware_evidence(manifest: &composer::ComposerManifest) -> Vec<Evidence> {
    let mut result = Vec::new();
    let name = normalized_package(&manifest.name);
    if SHOPWARE_PACKAGES.contains(&name.as_str()) {
        result.push(Evidence::new(
            "composer.json",
            format!("package name is {name}"),
        ));
    }
    if manifest.kind.trim().to_lowercase() == "shopware-platform-plugin" {
        result.push(Evidence::new(
            "composer.json",
            "type is shopware-platform-plugin",
        ));
    }
    if !manifest.shopware_plugin_class.trim().is_empty() {
        result.push(Evidence::new(
            "composer.json",
            "declares extra.shopware-plugin-class",
        ));
    }
    for name in dependency_names(manifest) {
        if SHOPWARE_PACKAGES.contains(&name.as_str()) {
            result.push(Evidence::new("composer.json", format!("requires {name}")));
        }
    }
    deduplicate_evidence(result)
}

fn composer_symfony_evidence(manifest: &composer::ComposerManifest) -> Vec<Evidence> {
    if dependency_names(manifest)
        .iter()
        .any(|name| name == "symfony/framework-bundle")
    {
        return vec![Evidence::new(
            "composer.json",
            "requires symfony/framework-bundle",
        )];
    }
    Vec::new()
}

fn dependency_names(manifest: &composer::ComposerManifest) -> Vec<String> {
    let mut result: Vec<String> = manifest
        .require
        .iter()
        .chain(&manifest.require_dev)
        .map(|name| normalized_package(name))
        .collect();
    result.sort();
    result.dedup();
    result
}

fn contains_framework_bundle(content: &[u8]) -> bool {
    let source = String::from_utf8_lossy(content);
    source.contains(r"Symfony\Bundle\FrameworkBundle\FrameworkBundle")
        || source.contains("FrameworkBundle::class")
}

fn supported(kind: Kind, evidence: &[Evidence]) -> DetectResult {
    DetectResult {
        supported: true,
        kind,
        evidence: deduplicate_evidence(evidence.to_vec()),
    }
}

fn existing_support(
    shopware: &[Evidence],
    symfony: &[Evidence],
    configured: &[Evidence],
) -> Option<DetectResult> {
    if !shopware.is_empty() {
        return Some(supported(Kind::Shopware, shopware));
    }
    if !symfony.is_empty() {
        return Some(supported(Kind::Symfony, symfony));
    }
    if !configured.is_empty() {
        return Some(supported(Kind::Configured, configured));
    }
    None
}

fn deduplicate_evidence(values: Vec<Evidence>) -> Vec<Evidence> {
    let mut seen = HashSet::with_capacity(values.len());
    values
        .into_iter()
        .filter(|value| seen.insert((value.path.clone(), value.reason.clone())))
        .collect()
}

fn normalized_package(value: &str) -> String {
    value.trim().to_lowercase()
}

fn regular_file(path: &str) -> Result<bool, DetectError> {
    match std::fs::metadata(path) {
        Ok(metadata) => Ok(metadata.is_file()),
        Err(error) if error.kind() == io::ErrorKind::NotFound => Ok(false),
        Err(source) => Err(DetectError::File {
            operation: "inspect",
            file: filepath::base(path),
            source,
        }),
    }
}

fn read_optional(path: &str) -> Result<Option<Vec<u8>>, DetectError> {
    match std::fs::read(path) {
        Ok(content) => Ok(Some(content)),
        Err(error) if error.kind() == io::ErrorKind::NotFound => Ok(None),
        Err(source) => Err(DetectError::File {
            operation: "read",
            file: filepath::base(path),
            source,
        }),
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    type Case<'a> = (&'a str, &'a [(&'a str, &'a str)], Kind);

    fn detect_files(files: &[(&str, &str)]) -> Result<DetectResult, DetectError> {
        let directory = tempfile::tempdir().unwrap();
        for (name, content) in files {
            let path = directory.path().join(name);
            std::fs::create_dir_all(path.parent().unwrap()).unwrap();
            std::fs::write(path, content).unwrap();
        }
        detect(directory.path().to_str().unwrap())
    }

    #[test]
    fn detect_project_kinds() {
        let manifest = r#"<manifest xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance" xsi:noNamespaceSchemaLocation="https://raw.githubusercontent.com/shopware/platform/trunk/src/Core/Framework/App/Manifest/Schema/manifest-3.0.xsd"></manifest>"#;
        let cases: &[Case<'_>] = &[
            ("unknown PHP", &[("src/Test.php", "<?php")], Kind::Unknown),
            (
                "legacy JSON ignored",
                &[(".config/shopware/lsp.json", r#"{"version":1}"#)],
                Kind::Unknown,
            ),
            (
                "configured",
                &[(".config/shopware/lsp.yaml", "version: 1\n")],
                Kind::Configured,
            ),
            (
                "platform",
                &[("composer.json", r#"{"name":"shopware/platform"}"#)],
                Kind::Shopware,
            ),
            (
                "production package",
                &[("composer.json", r#"{"name":"shopware/production"}"#)],
                Kind::Shopware,
            ),
            (
                "plugin type",
                &[("composer.json", r#"{"type":"shopware-platform-plugin"}"#)],
                Kind::Shopware,
            ),
            (
                "plugin class",
                &[(
                    "composer.json",
                    r#"{"extra":{"shopware-plugin-class":"Acme\\Plugin"}}"#,
                )],
                Kind::Shopware,
            ),
            (
                "Shopware dependency",
                &[("composer.json", r#"{"require-dev":{"shopware/core":"*"}}"#)],
                Kind::Shopware,
            ),
            (
                "Shopware lock",
                &[
                    ("composer.json", "{}"),
                    (
                        "composer.lock",
                        r#"{"packages":[{"name":"shopware/core"}]}"#,
                    ),
                ],
                Kind::Shopware,
            ),
            (
                "Shopware app",
                &[("manifest.xml", manifest)],
                Kind::Shopware,
            ),
            (
                "Symfony dependency",
                &[(
                    "composer.json",
                    r#"{"require":{"symfony/framework-bundle":"*"}}"#,
                )],
                Kind::Symfony,
            ),
            (
                "Symfony lock",
                &[(
                    "composer.lock",
                    r#"{"packages-dev":[{"name":"symfony/framework-bundle"}]}"#,
                )],
                Kind::Symfony,
            ),
            (
                "Symfony bundles",
                &[(
                    "config/bundles.php",
                    r"<?php return [Symfony\Bundle\FrameworkBundle\FrameworkBundle::class => ['all' => true]];",
                )],
                Kind::Symfony,
            ),
            (
                "Symfony components only",
                &[(
                    "composer.json",
                    r#"{"require":{"symfony/console":"*","symfony/http-kernel":"*"}}"#,
                )],
                Kind::Unknown,
            ),
        ];
        for (name, files, kind) in cases {
            let result = detect_files(files).unwrap_or_else(|error| panic!("{name}: {error}"));
            assert_eq!(result.kind, *kind, "{name}");
            assert_eq!(result.supported, *kind != Kind::Unknown, "{name}");
            if result.supported {
                assert!(!result.evidence.is_empty(), "{name}");
            }
        }
    }

    #[test]
    fn detect_prefers_shopware_over_symfony() {
        let result = detect_files(&[(
            "composer.json",
            r#"{"require":{"shopware/core":"*","symfony/framework-bundle":"*"}}"#,
        )])
        .unwrap();
        assert_eq!(result.kind, Kind::Shopware);
        assert_eq!(
            result.evidence,
            [Evidence::new("composer.json", "requires shopware/core")]
        );
    }

    #[test]
    fn detect_rejects_malformed_composer_metadata() {
        let error = detect_files(&[("composer.json", "{")]).unwrap_err();
        assert!(error.to_string().contains("parse composer.json"), "{error}");
        let error = detect_files(&[("composer.lock", "{")]).unwrap_err();
        assert!(error.to_string().contains("parse composer.lock"), "{error}");
        let error = detect_files(&[("manifest.xml", "<manifest a=x>")]).unwrap_err();
        assert!(error.to_string().contains("parse manifest.xml"), "{error}");
    }

    #[test]
    fn explicit_configuration_still_opts_into_malformed_project_metadata() {
        let result = detect_files(&[
            (".config/shopware/lsp.yaml", "version: 1\n"),
            ("composer.json", "{"),
        ])
        .unwrap();
        assert_eq!(result.kind, Kind::Configured);
    }

    #[test]
    fn detect_rejects_missing_or_file_roots() {
        let directory = tempfile::tempdir().unwrap();
        let file = directory.path().join("file");
        std::fs::write(&file, "").unwrap();
        let file = file.to_str().unwrap();
        assert_eq!(
            detect(file).unwrap_err().to_string(),
            format!("project root is not a directory: {file}")
        );
        let missing = directory.path().join("missing");
        assert!(
            detect(missing.to_str().unwrap())
                .unwrap_err()
                .to_string()
                .starts_with("inspect project root: ")
        );
    }

    #[test]
    fn result_serializes_supported_kind_and_evidence() {
        let result = detect_files(&[]).unwrap();
        assert_eq!(
            serde_json::to_string(&result).unwrap(),
            r#"{"supported":false,"kind":"unknown","evidence":[]}"#
        );
        let result = detect_files(&[(".config/shopware/lsp.yaml", "version: 1\n")]).unwrap();
        assert_eq!(
            serde_json::to_string(&result).unwrap(),
            r#"{"supported":true,"kind":"configured","evidence":[{"path":".config/shopware/lsp.yaml","reason":"explicit Shopware LSP configuration"}]}"#
        );
    }
}
