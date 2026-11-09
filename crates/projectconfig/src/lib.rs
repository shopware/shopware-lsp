//! Committed and editor-supplied configuration shared by the language server
//! and CLI.
//!
//! Configuration is layered: built-in defaults, the workspace
//! `.config/shopware/lsp.yaml`, nested diagnostics-only extension files and
//! editor-local overrides. Unknown settings and IDs fail validation.

mod config;
mod scoped;
mod yaml;

pub use config::{
    CURRENT_VERSION, CatalogEntry, CheckConfig, ConfigError, DEFAULT_MAX_FILE_SIZE_MIB,
    DOMAIN_CATALOG, DiagnosticOverride, DiagnosticPolicy, DiagnosticsConfig, Effective,
    EffectiveCheck, EffectiveDiagnostics, EffectiveIndexing, EffectiveMcp, EffectivePhp,
    EffectiveShopware, FEATURE_CATALOG, IndexingConfig, MCP_TOOL_CATALOG, McpConfig,
    PROJECT_RELATIVE_PATH, Partial, PatternError, PhpConfig, SCHEMA, Severity, ShopwareConfig,
    apply_diagnostics, decode, default_effective, load, path, resolve, valid_severity, validate,
};
pub use scoped::{Scope, contains, load_scopes, scope_errors, validate_nested};
pub use yaml::{DecodeErrors, DecodeProblem, SyntaxError};

#[cfg(test)]
mod tests;
