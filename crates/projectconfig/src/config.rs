//! Configuration model, decoding, validation, layering and fingerprinting
//! (port of `internal/projectconfig/config.go`).

use std::collections::{BTreeMap, HashSet};
use std::fmt;
use std::sync::OnceLock;

use regex::Regex;
use serde::{Deserialize, Deserializer, Serialize, Serializer};
use sha2::{Digest, Sha256};
use swlsp_uriutil::{filepath, go_quote};

use crate::yaml;

/// The only supported configuration `version`.
pub const CURRENT_VERSION: i64 = 1;
/// Workspace-relative location of the committed configuration file.
pub const PROJECT_RELATIVE_PATH: &str = ".config/shopware/lsp.yaml";
/// Default `indexing.maxFileSizeMiB`.
pub const DEFAULT_MAX_FILE_SIZE_MIB: i64 = 8;
const MAXIMUM_CONFIGURED_FILE_MIB: i64 = 4095;

/// The embedded JSON schema for `.config/shopware/lsp.yaml` (Go
/// `projectconfig.Schema`). The Go file stays the single source of truth.
pub const SCHEMA: &str = include_str!("../schema.json");

/// A configuration error. `Display` matches the Go error text.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct ConfigError(pub(crate) String);

impl ConfigError {
    pub(crate) fn new(message: impl Into<String>) -> Self {
        Self(message.into())
    }

    /// The error message.
    pub fn message(&self) -> &str {
        &self.0
    }
}

impl fmt::Display for ConfigError {
    fn fmt(&self, formatter: &mut fmt::Formatter<'_>) -> fmt::Result {
        formatter.write_str(&self.0)
    }
}

impl std::error::Error for ConfigError {}

/// A diagnostic severity. Like the Go string type, any value can be decoded;
/// [`valid_severity`] decides whether it is acceptable.
#[derive(Debug, Clone, PartialEq, Eq, Hash, PartialOrd, Ord)]
pub enum Severity {
    Off,
    Hint,
    Information,
    Warning,
    Error,
    /// Any other (invalid) value, kept verbatim.
    Other(String),
}

impl Severity {
    /// The wire representation.
    pub fn as_str(&self) -> &str {
        match self {
            Severity::Off => "off",
            Severity::Hint => "hint",
            Severity::Information => "information",
            Severity::Warning => "warning",
            Severity::Error => "error",
            Severity::Other(value) => value,
        }
    }
}

impl From<&str> for Severity {
    fn from(value: &str) -> Self {
        match value {
            "off" => Severity::Off,
            "hint" => Severity::Hint,
            "information" => Severity::Information,
            "warning" => Severity::Warning,
            "error" => Severity::Error,
            other => Severity::Other(other.to_owned()),
        }
    }
}

impl From<String> for Severity {
    fn from(value: String) -> Self {
        match Severity::from(value.as_str()) {
            Severity::Other(_) => Severity::Other(value),
            known => known,
        }
    }
}

impl fmt::Display for Severity {
    fn fmt(&self, formatter: &mut fmt::Formatter<'_>) -> fmt::Result {
        formatter.write_str(self.as_str())
    }
}

impl Serialize for Severity {
    fn serialize<S: Serializer>(&self, serializer: S) -> Result<S::Ok, S::Error> {
        serializer.serialize_str(self.as_str())
    }
}

impl<'de> Deserialize<'de> for Severity {
    fn deserialize<D: Deserializer<'de>>(deserializer: D) -> Result<Self, D::Error> {
        // Go decodes JSON null into a string type as the zero value.
        Ok(Option::<String>::deserialize(deserializer)?
            .unwrap_or_default()
            .into())
    }
}

/// Go JSON decoding leaves a field at its zero value when the input is null.
fn nullable<'de, D, T>(deserializer: D) -> Result<T, D::Error>
where
    D: Deserializer<'de>,
    T: Deserialize<'de> + Default,
{
    Ok(Option::<T>::deserialize(deserializer)?.unwrap_or_default())
}

#[derive(Debug, Clone, PartialEq, Eq, Default, Serialize, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct PhpConfig {
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub extensions: Option<Vec<String>>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub disabled_extensions: Option<Vec<String>>,
}

#[derive(Debug, Clone, PartialEq, Eq, Default, Serialize, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct ShopwareConfig {
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub target_version: Option<String>,
}

#[derive(Debug, Clone, PartialEq, Eq, Default, Serialize, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct IndexingConfig {
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub enabled: Option<bool>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub exclude: Option<Vec<String>>,
    #[serde(
        default,
        rename = "maxFileSizeMiB",
        skip_serializing_if = "Option::is_none"
    )]
    pub max_file_size_mib: Option<i64>,
}

#[derive(Debug, Clone, PartialEq, Eq, Default, Serialize, Deserialize)]
pub struct McpConfig {
    #[serde(
        default,
        deserialize_with = "nullable",
        skip_serializing_if = "BTreeMap::is_empty"
    )]
    pub tools: BTreeMap<String, bool>,
}

#[derive(Debug, Clone, PartialEq, Eq, Default, Serialize, Deserialize)]
pub struct DiagnosticsConfig {
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub enabled: Option<bool>,
    #[serde(
        default,
        deserialize_with = "nullable",
        skip_serializing_if = "BTreeMap::is_empty"
    )]
    pub inspections: BTreeMap<String, bool>,
    #[serde(
        default,
        deserialize_with = "nullable",
        skip_serializing_if = "BTreeMap::is_empty"
    )]
    pub rules: BTreeMap<String, Severity>,
    #[serde(
        default,
        deserialize_with = "nullable",
        skip_serializing_if = "Vec::is_empty"
    )]
    pub overrides: Vec<DiagnosticOverride>,
}

/// Applies diagnostic settings to workspace-scope-relative files. Overrides
/// are ordered; later matching entries win.
#[derive(Debug, Clone, PartialEq, Eq, Default, Serialize, Deserialize)]
pub struct DiagnosticOverride {
    #[serde(default, deserialize_with = "nullable")]
    pub files: Vec<String>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub enabled: Option<bool>,
    #[serde(
        default,
        deserialize_with = "nullable",
        skip_serializing_if = "BTreeMap::is_empty"
    )]
    pub inspections: BTreeMap<String, bool>,
    #[serde(
        default,
        deserialize_with = "nullable",
        skip_serializing_if = "BTreeMap::is_empty"
    )]
    pub rules: BTreeMap<String, Severity>,
}

#[derive(Debug, Clone, PartialEq, Eq, Default, Serialize, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct CheckConfig {
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub severity: Option<Severity>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub fail_on: Option<Severity>,
}

/// A configuration overlay. `Option` fields distinguish an omitted value
/// from an explicitly configured zero value.
#[derive(Debug, Clone, PartialEq, Eq, Default, Serialize, Deserialize)]
pub struct Partial {
    #[serde(
        rename = "$schema",
        default,
        deserialize_with = "nullable",
        skip_serializing_if = "String::is_empty"
    )]
    pub schema: String,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub version: Option<i64>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub php: Option<PhpConfig>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub shopware: Option<ShopwareConfig>,
    #[serde(
        default,
        deserialize_with = "nullable",
        skip_serializing_if = "BTreeMap::is_empty"
    )]
    pub features: BTreeMap<String, bool>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub indexing: Option<IndexingConfig>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub mcp: Option<McpConfig>,
    #[serde(
        default,
        deserialize_with = "nullable",
        skip_serializing_if = "BTreeMap::is_empty"
    )]
    pub domains: BTreeMap<String, bool>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub diagnostics: Option<DiagnosticsConfig>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub check: Option<CheckConfig>,
}

/// The resolved configuration.
#[derive(Debug, Clone, PartialEq, Eq, Serialize)]
pub struct Effective {
    pub php: EffectivePhp,
    pub shopware: EffectiveShopware,
    pub features: BTreeMap<String, bool>,
    pub indexing: EffectiveIndexing,
    pub mcp: EffectiveMcp,
    pub domains: BTreeMap<String, bool>,
    pub diagnostics: EffectiveDiagnostics,
    pub check: EffectiveCheck,
    #[serde(
        rename = "disabledDomainReasons",
        skip_serializing_if = "BTreeMap::is_empty"
    )]
    pub disabled_reason: BTreeMap<String, String>,
    #[serde(skip_serializing_if = "BTreeMap::is_empty")]
    pub origins: BTreeMap<String, String>,
}

/// Resolved PHP settings. `None` mirrors a nil Go slice (`null` in JSON),
/// which is distinct from an explicitly configured empty list.
#[derive(Debug, Clone, PartialEq, Eq, Default, Serialize)]
#[serde(rename_all = "camelCase")]
pub struct EffectivePhp {
    pub extensions: Option<Vec<String>>,
    pub disabled_extensions: Option<Vec<String>>,
}

#[derive(Debug, Clone, PartialEq, Eq, Default, Serialize)]
#[serde(rename_all = "camelCase")]
pub struct EffectiveShopware {
    pub target_version: String,
}

#[derive(Debug, Clone, PartialEq, Eq, Default, Serialize)]
pub struct EffectiveIndexing {
    pub enabled: bool,
    #[serde(skip_serializing_if = "Vec::is_empty")]
    pub exclude: Vec<String>,
    #[serde(rename = "maxFileSizeMiB")]
    pub max_file_size_mib: i64,
}

#[derive(Debug, Clone, PartialEq, Eq, Default, Serialize)]
pub struct EffectiveMcp {
    pub tools: BTreeMap<String, bool>,
}

#[derive(Debug, Clone, PartialEq, Eq, Default, Serialize)]
pub struct EffectiveDiagnostics {
    pub enabled: bool,
    pub inspections: BTreeMap<String, bool>,
    pub rules: BTreeMap<String, Severity>,
    #[serde(skip_serializing_if = "Vec::is_empty")]
    pub overrides: Vec<DiagnosticOverride>,
}

/// The resolved diagnostic policy for one document.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct DiagnosticPolicy {
    pub enabled: bool,
    pub inspections: BTreeMap<String, bool>,
    pub rules: BTreeMap<String, Severity>,
}

impl Default for DiagnosticPolicy {
    /// The built-in diagnostic defaults (Go `DefaultDiagnosticPolicy`).
    fn default() -> Self {
        Self {
            enabled: true,
            inspections: BTreeMap::new(),
            rules: BTreeMap::new(),
        }
    }
}

#[derive(Debug, Clone, PartialEq, Eq, Serialize)]
#[serde(rename_all = "camelCase")]
pub struct EffectiveCheck {
    pub severity: Severity,
    pub fail_on: Severity,
}

/// One entry of a public, stable configuration catalog.
#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize)]
#[serde(rename_all = "camelCase")]
pub struct CatalogEntry {
    pub id: &'static str,
    pub label: &'static str,
    #[serde(skip_serializing_if = "str::is_empty")]
    pub description: &'static str,
    #[serde(skip_serializing_if = "str::is_empty")]
    pub parent: &'static str,
    #[serde(skip_serializing_if = "<[_]>::is_empty")]
    pub depends_on: &'static [&'static str],
}

const fn entry(id: &'static str, label: &'static str) -> CatalogEntry {
    CatalogEntry {
        id,
        label,
        description: "",
        parent: "",
        depends_on: &[],
    }
}

const fn child(
    id: &'static str,
    label: &'static str,
    parent: &'static str,
    depends_on: &'static [&'static str],
) -> CatalogEntry {
    CatalogEntry {
        id,
        label,
        description: "",
        parent,
        depends_on,
    }
}

pub static FEATURE_CATALOG: &[CatalogEntry] = &[
    entry("completion", "Completion"),
    entry("definition", "Go to Definition"),
    entry("implementation", "Go to Implementation"),
    entry("typeHierarchy", "Type Hierarchy"),
    entry("callHierarchy", "Call Hierarchy"),
    entry("references", "References"),
    entry("codeLens", "Code Lenses"),
    entry("hover", "Hover"),
    entry("signatureHelp", "Signature Help"),
    entry("rename", "Rename"),
    entry("inlayHints", "Inlay Hints"),
    entry("documentLinks", "Document Links"),
    entry("documentSymbols", "Document Symbols"),
    entry("documentHighlights", "Document Highlights"),
    entry("linkedEditing", "Linked Editing"),
    entry("foldingRanges", "Folding Ranges"),
    entry("formatting", "Document Formatting"),
    entry("selectionRanges", "Selection Ranges"),
    entry("documentColors", "Document Colors"),
    entry("semanticTokens", "Semantic Tokens"),
    entry("codeActions", "Code Actions"),
    entry("workspaceSymbols", "Workspace Symbols"),
    entry("fileRename", "File Rename"),
    entry("commands", "Shopware Commands"),
];

/// The stable public MCP tool names accepted in committed and editor-local
/// configuration. Tools default to enabled.
pub static MCP_TOOL_CATALOG: &[CatalogEntry] = &[
    entry("shopware_diagnostics", "Diagnostics"),
    entry("shopware_code_actions", "Code Actions"),
    entry("shopware_apply_code_action", "Apply Code Action"),
    entry("shopware_hover", "Hover"),
    entry("shopware_definition", "Definitions"),
    entry("shopware_references", "References"),
    entry("shopware_workspace_symbols", "Workspace Symbols"),
    entry("shopware_scaffold_catalog", "Scaffold Catalog"),
    entry("shopware_scaffold", "Create Scaffold"),
    entry(
        "shopware_entity_schema_bootstrap",
        "Entity Schema Bootstrap",
    ),
    entry(
        "shopware_entity_schema_field_types",
        "Entity Schema Field Types",
    ),
    entry("shopware_entity_schema_search", "Entity Schema Search"),
    entry("shopware_entity_schema_load", "Entity Schema Load"),
    entry("shopware_entity_schema_preview", "Entity Schema Preview"),
    entry("shopware_entity_schema_apply", "Entity Schema Apply"),
    entry(
        "shopware_entity_schema_reconcile",
        "Entity Schema Reconcile",
    ),
];

/// Deliberately public and stable: these IDs are persisted in project
/// configuration and returned by the configuration catalog request.
pub static DOMAIN_CATALOG: &[CatalogEntry] = &[
    entry("php", "PHP"),
    CatalogEntry {
        id: "twig",
        label: "Twig",
        description: "",
        parent: "",
        depends_on: &["php", "symfony.services"],
    },
    entry("scss", "SCSS"),
    entry("administration", "Shopware Administration"),
    entry("symfony", "Symfony"),
    child("symfony.services", "Symfony Services", "symfony", &["php"]),
    child("symfony.routes", "Symfony Routes", "symfony", &["php"]),
    child("symfony.console", "Symfony Console", "symfony", &["php"]),
    child(
        "symfony.doctrine",
        "Doctrine DBAL",
        "symfony",
        &["php", "symfony.services"],
    ),
    child("symfony.assets", "Symfony Assets", "symfony", &[]),
    child(
        "symfony.events",
        "Symfony Events",
        "symfony",
        &["php", "symfony.services"],
    ),
    child(
        "symfony.messenger",
        "Symfony Messenger",
        "symfony",
        &["php"],
    ),
    child(
        "symfony.environment",
        "Environment Variables",
        "symfony",
        &[],
    ),
    child(
        "symfony.configuration",
        "Symfony Configuration",
        "symfony",
        &["php"],
    ),
    child(
        "symfony.serializer",
        "Symfony Serializer",
        "symfony",
        &["php"],
    ),
    child(
        "symfony.validation",
        "Symfony Validation",
        "symfony",
        &["php"],
    ),
    child(
        "symfony.twigComponents",
        "Symfony Twig Components",
        "symfony",
        &["twig"],
    ),
    entry("shopware", "Shopware"),
    child(
        "shopware.extensions",
        "Shopware Extensions",
        "shopware",
        &["php"],
    ),
    child("shopware.snippets", "Shopware Snippets", "shopware", &[]),
    child("shopware.translations", "Translations", "shopware", &[]),
    child("shopware.featureFlags", "Feature Flags", "shopware", &[]),
    child(
        "shopware.systemConfig",
        "System Config",
        "shopware",
        &["php"],
    ),
    child("shopware.theme", "Theme", "shopware", &[]),
    child(
        "shopware.twigVersioning",
        "Twig Block Versioning",
        "shopware",
        &["twig"],
    ),
    child("shopware.dal", "Shopware DAL", "shopware", &["php"]),
    child("shopware.appScripts", "App Scripts", "shopware", &[]),
    child(
        "shopware.migrations",
        "Shopware Migrations",
        "shopware",
        &["php"],
    ),
    child(
        "shopware.entitySchema",
        "Entity Schema",
        "shopware",
        &["shopware.dal"],
    ),
    child("shopware.store", "Shopware Store Metadata", "shopware", &[]),
];

fn catalog_map(catalog: &[CatalogEntry]) -> BTreeMap<String, bool> {
    catalog
        .iter()
        .map(|entry| (entry.id.to_owned(), true))
        .collect()
}

fn catalog_contains(catalog: &[CatalogEntry], id: &str) -> bool {
    catalog.iter().any(|entry| entry.id == id)
}

impl Default for Effective {
    /// The built-in defaults (Go `Default`).
    fn default() -> Self {
        Self {
            php: EffectivePhp::default(),
            shopware: EffectiveShopware::default(),
            features: catalog_map(FEATURE_CATALOG),
            indexing: EffectiveIndexing {
                enabled: true,
                exclude: Vec::new(),
                max_file_size_mib: DEFAULT_MAX_FILE_SIZE_MIB,
            },
            mcp: EffectiveMcp {
                tools: catalog_map(MCP_TOOL_CATALOG),
            },
            domains: catalog_map(DOMAIN_CATALOG),
            diagnostics: EffectiveDiagnostics {
                enabled: true,
                ..EffectiveDiagnostics::default()
            },
            check: EffectiveCheck {
                severity: Severity::Warning,
                fail_on: Severity::Off,
            },
            disabled_reason: BTreeMap::new(),
            origins: BTreeMap::new(),
        }
    }
}

/// The built-in defaults (Go `Default`).
pub fn default_effective() -> Effective {
    Effective::default()
}

/// The configuration file path below `root` (Go `Path`).
pub fn path(root: &str) -> String {
    filepath::join(&[root, &filepath::from_slash(PROJECT_RELATIVE_PATH)])
}

/// Loads the workspace configuration. `Ok(None)` means no file exists.
pub fn load(root: &str) -> Result<Option<Partial>, ConfigError> {
    let path = path(root);
    let data = match std::fs::read(&path) {
        Ok(data) => data,
        Err(error) if error.kind() == std::io::ErrorKind::NotFound => return Ok(None),
        Err(error) => return Err(ConfigError(format!("read {path}: {error}"))),
    };
    decode(&data)
        .map(Some)
        .map_err(|error| ConfigError(format!("{path}: {error}")))
}

/// Decodes and validates one YAML (or JSON) configuration document with
/// unknown fields rejected (Go `Decode`).
pub fn decode(data: &[u8]) -> Result<Partial, ConfigError> {
    let source = std::str::from_utf8(data)
        .map_err(|_| ConfigError::new("decode configuration: yaml: invalid leading UTF-8 octet"))?;
    let documents = match yaml::load_documents(source) {
        yaml::LoadOutcome::Documents(documents) => documents,
        yaml::LoadOutcome::SyntaxError(error) => {
            return Err(ConfigError(format!("decode configuration: {error}")));
        }
    };
    let Some(first) = documents.first() else {
        return Err(ConfigError::new("decode configuration: EOF"));
    };
    let mut decoder = yaml::Decoder::new();
    let value = decoder.partial(first);
    decoder
        .finish()
        .map_err(|error| ConfigError(format!("decode configuration: {error}")))?;
    if documents.len() > 1 {
        return Err(ConfigError::new(
            "decode configuration: multiple JSON values",
        ));
    }
    let Some(version) = value.version else {
        return Err(ConfigError::new("configuration version is required"));
    };
    if version != CURRENT_VERSION {
        return Err(ConfigError(format!(
            "unsupported configuration version {version}"
        )));
    }
    validate(&value)?;
    Ok(value)
}

fn target_version_pattern() -> &'static Regex {
    static PATTERN: OnceLock<Regex> = OnceLock::new();
    PATTERN.get_or_init(|| {
        Regex::new(r"^$|^v?[0-9]+\.[0-9]+(?:\.[0-9]+)?(?:\.[0-9]+)?(?:[-+][0-9A-Za-z.-]+)?$")
            .expect("target version pattern is valid")
    })
}

/// Validates semantic constraints the YAML decoder cannot express (Go
/// `Validate`). Unknown feature, MCP tool and domain IDs are rejected.
pub fn validate(value: &Partial) -> Result<(), ConfigError> {
    if let Some(version) = value
        .shopware
        .as_ref()
        .and_then(|shopware| shopware.target_version.as_ref())
        && !target_version_pattern().is_match(version.trim())
    {
        return Err(ConfigError(format!(
            "invalid Shopware target version {}",
            go_quote(version)
        )));
    }
    if let Some(php) = &value.php {
        for (label, values) in [
            ("php.extensions", &php.extensions),
            ("php.disabledExtensions", &php.disabled_extensions),
        ] {
            if values
                .iter()
                .flatten()
                .any(|current| current.trim().is_empty())
            {
                return Err(ConfigError(format!(
                    "{label} must not contain an empty extension"
                )));
            }
        }
    }
    if let Some(indexing) = &value.indexing {
        for pattern in indexing.exclude.iter().flatten() {
            validate_path_pattern(pattern)
                .map_err(|error| ConfigError(format!("indexing.exclude: {error}")))?;
        }
        if let Some(size) = indexing.max_file_size_mib
            && !(0..=MAXIMUM_CONFIGURED_FILE_MIB).contains(&size)
        {
            return Err(ConfigError(format!(
                "indexing.maxFileSizeMiB must be between 0 and {MAXIMUM_CONFIGURED_FILE_MIB}"
            )));
        }
    }
    if let Some(id) = value
        .features
        .keys()
        .find(|id| !catalog_contains(FEATURE_CATALOG, id))
    {
        return Err(ConfigError(format!("unknown feature {}", go_quote(id))));
    }
    if let Some(mcp) = &value.mcp
        && let Some(id) = mcp
            .tools
            .keys()
            .find(|id| !catalog_contains(MCP_TOOL_CATALOG, id))
    {
        return Err(ConfigError(format!("unknown MCP tool {}", go_quote(id))));
    }
    if let Some(id) = value
        .domains
        .keys()
        .find(|id| !catalog_contains(DOMAIN_CATALOG, id))
    {
        return Err(ConfigError(format!("unknown domain {}", go_quote(id))));
    }
    if let Some(diagnostics) = &value.diagnostics {
        validate_diagnostics(diagnostics)?;
    }
    if let Some(check) = &value.check {
        if let Some(severity) = &check.severity
            && !valid_severity(severity, false)
        {
            return Err(ConfigError(format!(
                "invalid check severity {}",
                go_quote(severity.as_str())
            )));
        }
        if let Some(severity) = &check.fail_on
            && !valid_severity(severity, true)
        {
            return Err(ConfigError(format!(
                "invalid check failOn severity {}",
                go_quote(severity.as_str())
            )));
        }
    }
    Ok(())
}

fn validate_diagnostics(value: &DiagnosticsConfig) -> Result<(), ConfigError> {
    validate_diagnostic_maps(&value.inspections, &value.rules)?;
    for (index, entry) in value.overrides.iter().enumerate() {
        if entry.files.is_empty() {
            return Err(ConfigError(format!(
                "diagnostics.overrides[{index}].files must not be empty"
            )));
        }
        if entry.enabled.is_none() && entry.inspections.is_empty() && entry.rules.is_empty() {
            return Err(ConfigError(format!(
                "diagnostics.overrides[{index}] must configure enabled, inspections, or rules"
            )));
        }
        for pattern in &entry.files {
            validate_path_pattern(pattern).map_err(|error| {
                ConfigError(format!("diagnostics.overrides[{index}].files: {error}"))
            })?;
        }
        validate_diagnostic_maps(&entry.inspections, &entry.rules)
            .map_err(|error| ConfigError(format!("diagnostics.overrides[{index}]: {error}")))?;
    }
    Ok(())
}

fn validate_diagnostic_maps(
    inspections: &BTreeMap<String, bool>,
    rules: &BTreeMap<String, Severity>,
) -> Result<(), ConfigError> {
    for (id, severity) in rules {
        if id.trim().is_empty() {
            return Err(ConfigError::new("diagnostic rule ID must not be empty"));
        }
        if !valid_severity(severity, true) {
            return Err(ConfigError(format!(
                "invalid severity {} for diagnostic {}",
                go_quote(severity.as_str()),
                go_quote(id)
            )));
        }
    }
    if inspections.keys().any(|id| id.trim().is_empty()) {
        return Err(ConfigError::new("inspection ID must not be empty"));
    }
    Ok(())
}

fn normalize_pattern_separators(pattern: &str) -> String {
    filepath::to_slash(&pattern.trim().replace('\\', "/"))
}

fn validate_path_pattern(pattern: &str) -> Result<(), ConfigError> {
    let pattern = normalize_pattern_separators(pattern);
    if pattern.is_empty() {
        return Err(ConfigError::new("pattern must not be empty"));
    }
    let bytes = pattern.as_bytes();
    let windows_absolute =
        bytes.len() >= 3 && bytes[1] == b':' && bytes[2] == b'/' && bytes[0].is_ascii_alphabetic();
    if filepath::is_abs(&filepath::from_slash(&pattern))
        || pattern.starts_with('/')
        || windows_absolute
    {
        return Err(ConfigError(format!(
            "pattern {} must be relative",
            go_quote(&pattern)
        )));
    }
    if pattern.split('/').any(|component| component == "..") {
        return Err(ConfigError(format!(
            "pattern {} must not escape its configuration scope",
            go_quote(&pattern)
        )));
    }
    swlsp_pathmatch::compile(&[pattern.as_str()])
        .map(|_| ())
        .map_err(|error| ConfigError(error.to_string()))
}

/// Reports whether `value` is a known severity, optionally rejecting `off`
/// (Go `ValidSeverity`).
pub fn valid_severity(value: &Severity, allow_off: bool) -> bool {
    match value {
        Severity::Other(_) => false,
        Severity::Off => allow_off,
        _ => true,
    }
}

/// Layers project and editor configuration over the built-in defaults and
/// cascades disabled domain dependencies (Go `Resolve`).
pub fn resolve(project: &Partial, editor: &Partial) -> Effective {
    let mut result = Effective::default();
    apply(&mut result, project, "project");
    apply(&mut result, editor, "editor");
    resolve_domain_dependencies(&mut result);
    result
}

fn apply(target: &mut Effective, value: &Partial, source: &str) {
    let origin = |target: &mut Effective, key: String| {
        target.origins.insert(key, source.to_owned());
    };
    if let Some(php) = &value.php {
        if let Some(extensions) = &php.extensions {
            target.php.extensions = Some(normalize_strings(extensions));
            origin(target, "php.extensions".to_owned());
        }
        if let Some(extensions) = &php.disabled_extensions {
            target.php.disabled_extensions = Some(normalize_strings(extensions));
            origin(target, "php.disabledExtensions".to_owned());
        }
    }
    if let Some(version) = value
        .shopware
        .as_ref()
        .and_then(|shopware| shopware.target_version.as_ref())
    {
        target.shopware.target_version = version.trim().to_owned();
        origin(target, "shopware.targetVersion".to_owned());
    }
    for (id, enabled) in &value.features {
        target.features.insert(id.clone(), *enabled);
        origin(target, format!("features.{id}"));
    }
    if let Some(indexing) = &value.indexing {
        if let Some(enabled) = indexing.enabled {
            target.indexing.enabled = enabled;
            origin(target, "indexing.enabled".to_owned());
        }
        if let Some(exclude) = &indexing.exclude {
            target.indexing.exclude = normalize_path_patterns(exclude);
            origin(target, "indexing.exclude".to_owned());
        }
        if let Some(size) = indexing.max_file_size_mib {
            target.indexing.max_file_size_mib = size;
            origin(target, "indexing.maxFileSizeMiB".to_owned());
        }
    }
    if let Some(mcp) = &value.mcp {
        for (id, enabled) in &mcp.tools {
            target.mcp.tools.insert(id.clone(), *enabled);
            origin(target, format!("mcp.tools.{id}"));
        }
    }
    for (id, enabled) in &value.domains {
        target.domains.insert(id.clone(), *enabled);
        origin(target, format!("domains.{id}"));
    }
    if let Some(diagnostics) = &value.diagnostics {
        if let Some(enabled) = diagnostics.enabled {
            target.diagnostics.enabled = enabled;
            origin(target, "diagnostics.enabled".to_owned());
        }
        for (id, enabled) in &diagnostics.inspections {
            target.diagnostics.inspections.insert(id.clone(), *enabled);
            origin(target, format!("diagnostics.inspections.{id}"));
        }
        for (id, severity) in &diagnostics.rules {
            target
                .diagnostics
                .rules
                .insert(id.clone(), severity.clone());
            origin(target, format!("diagnostics.rules.{id}"));
        }
        target
            .diagnostics
            .overrides
            .extend(diagnostics.overrides.iter().cloned());
    }
    if let Some(check) = &value.check {
        if let Some(severity) = &check.severity {
            target.check.severity = severity.clone();
            origin(target, "check.severity".to_owned());
        }
        if let Some(severity) = &check.fail_on {
            target.check.fail_on = severity.clone();
            origin(target, "check.failOn".to_owned());
        }
    }
}

fn resolve_domain_dependencies(target: &mut Effective) {
    let mut changed = true;
    while changed {
        changed = false;
        for entry in DOMAIN_CATALOG {
            if !target.domain_enabled(entry.id) {
                continue;
            }
            let parent = (!entry.parent.is_empty()).then_some(entry.parent);
            let missing = entry
                .depends_on
                .iter()
                .copied()
                .chain(parent)
                .find(|dependency| !target.domain_enabled(dependency));
            if let Some(dependency) = missing {
                target.domains.insert(entry.id.to_owned(), false);
                target
                    .disabled_reason
                    .insert(entry.id.to_owned(), format!("requires {dependency}"));
                changed = true;
            }
        }
    }
}

impl Effective {
    pub fn feature_enabled(&self, id: &str) -> bool {
        self.features.get(id).copied().unwrap_or(false)
    }

    pub fn domain_enabled(&self, id: &str) -> bool {
        self.domains.get(id).copied().unwrap_or(false)
    }

    /// Tools default to enabled unless explicitly disabled.
    pub fn mcp_tool_enabled(&self, id: &str) -> bool {
        self.mcp.tools.get(id).copied().unwrap_or(true)
    }

    /// Inspections default to enabled while diagnostics are enabled.
    pub fn inspection_enabled(&self, id: &str) -> bool {
        self.diagnostics.enabled
            && self
                .diagnostics
                .inspections
                .get(id)
                .copied()
                .unwrap_or(true)
    }

    /// The configured severity override for a diagnostic rule, if any.
    pub fn rule_severity(&self, id: &str) -> Option<&Severity> {
        self.diagnostics.rules.get(id)
    }

    /// A SHA-256 hex digest of the settings that require re-indexing when
    /// they change (PHP, Shopware, indexing, domains). The hashed JSON is
    /// byte-identical to Go's `encoding/json` output.
    pub fn structural_fingerprint(&self) -> String {
        #[derive(Serialize)]
        struct Payload<'a> {
            php: &'a EffectivePhp,
            shopware: &'a EffectiveShopware,
            indexing: &'a EffectiveIndexing,
            domains: &'a BTreeMap<String, bool>,
        }
        let encoded = go_json(&Payload {
            php: &self.php,
            shopware: &self.shopware,
            indexing: &self.indexing,
            domains: &self.domains,
        });
        let digest = Sha256::digest(encoded.as_bytes());
        let mut result = String::with_capacity(64);
        for byte in digest {
            result.push_str(&format!("{byte:02x}"));
        }
        result
    }
}

/// Serializes like Go `json.Marshal`, which escapes `<`, `>`, `&`, U+2028 and
/// U+2029 inside strings. Those characters never occur outside strings in
/// JSON output, so a textual replacement is exact.
fn go_json<T: Serialize>(value: &T) -> String {
    let encoded = serde_json::to_string(value).unwrap_or_default();
    if !encoded.contains(['<', '>', '&', '\u{2028}', '\u{2029}']) {
        return encoded;
    }
    let mut result = String::with_capacity(encoded.len() + 16);
    for character in encoded.chars() {
        match character {
            '<' => result.push_str("\\u003c"),
            '>' => result.push_str("\\u003e"),
            '&' => result.push_str("\\u0026"),
            '\u{2028}' => result.push_str("\\u2028"),
            '\u{2029}' => result.push_str("\\u2029"),
            character => result.push(character),
        }
    }
    result
}

/// Applies one configuration layer and its matching ordered overrides.
/// `relative_path` must be slash-normalized and relative to the layer's
/// owning scope (Go `ApplyDiagnostics`).
pub fn apply_diagnostics(
    policy: &mut DiagnosticPolicy,
    value: Option<&DiagnosticsConfig>,
    relative_path: &str,
) {
    let Some(value) = value else {
        return;
    };
    apply_diagnostic_values(policy, value.enabled, &value.inspections, &value.rules);
    let relative_path =
        filepath::to_slash(relative_path.strip_prefix("./").unwrap_or(relative_path));
    for entry in &value.overrides {
        if entry
            .files
            .iter()
            .any(|pattern| swlsp_pathmatch::ant(pattern, &relative_path))
        {
            apply_diagnostic_values(policy, entry.enabled, &entry.inspections, &entry.rules);
        }
    }
}

fn apply_diagnostic_values(
    policy: &mut DiagnosticPolicy,
    enabled: Option<bool>,
    inspections: &BTreeMap<String, bool>,
    rules: &BTreeMap<String, Severity>,
) {
    if let Some(enabled) = enabled {
        policy.enabled = enabled;
    }
    for (id, value) in inspections {
        policy.inspections.insert(id.clone(), *value);
    }
    for (id, value) in rules {
        policy.rules.insert(id.clone(), value.clone());
    }
}

/// Lowercases like Go's `strings.ToLower` (one rune per rune).
fn go_to_lower(value: &str) -> String {
    if value.is_ascii() {
        return value.to_ascii_lowercase();
    }
    value
        .chars()
        .map(|character| character.to_lowercase().next().unwrap_or(character))
        .collect()
}

fn normalize_strings(values: &[String]) -> Vec<String> {
    let mut seen = HashSet::with_capacity(values.len());
    let mut result: Vec<String> = values
        .iter()
        .map(|value| value.trim())
        .filter(|value| !value.is_empty() && seen.insert(go_to_lower(value)))
        .map(str::to_owned)
        .collect();
    result.sort();
    result
}

fn normalize_path_patterns(values: &[String]) -> Vec<String> {
    let mut seen = HashSet::with_capacity(values.len());
    let mut result = Vec::with_capacity(values.len());
    for value in values {
        let value = normalize_pattern_separators(value);
        let value = value.strip_prefix("./").unwrap_or(&value).to_owned();
        if value.is_empty() || !seen.insert(value.clone()) {
            continue;
        }
        result.push(value);
    }
    result
}
