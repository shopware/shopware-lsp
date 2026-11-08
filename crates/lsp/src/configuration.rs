//! Layered configuration: defaults, workspace file, nested extension scopes
//! and editor overrides; the configuration catalog; method/feature gating
//! and inspection-to-domain routing.

use std::collections::{BTreeMap, BTreeSet};

use serde::{Serialize, Serializer};
use serde_json::{Value, json};
use swlsp_lsp_protocol as protocol;
use swlsp_projectconfig::{
    self as projectconfig, CatalogEntry, DiagnosticPolicy, Effective, Partial, Scope, Severity,
};
use swlsp_uriutil::{filepath, go_quote};

use crate::context::RpcError;
use crate::inspection::DiagnosticId;
use crate::server::{Server, decode_params, read, write};

/// Mutable configuration owned by the server.
#[derive(Debug, Clone)]
pub(crate) struct ConfigurationState {
    pub project: Partial,
    pub scopes: Vec<Scope>,
    pub editor: Partial,
    pub effective: Effective,
    /// Go `configurationErr` (joined error messages).
    pub error: Option<String>,
    pub issues: Vec<ConfigurationIssue>,
    pub pending_fingerprint: String,
}

impl Default for ConfigurationState {
    fn default() -> Self {
        Self {
            project: Partial::default(),
            scopes: Vec::new(),
            editor: Partial::default(),
            effective: projectconfig::default_effective(),
            error: None,
            issues: Vec::new(),
            pending_fingerprint: String::new(),
        }
    }
}

fn null_if_empty<S: Serializer, T: Serialize>(
    values: &[T],
    serializer: S,
) -> Result<S::Ok, S::Error> {
    if values.is_empty() {
        serializer.serialize_none()
    } else {
        values.serialize(serializer)
    }
}

#[derive(Debug, Clone, PartialEq, Serialize)]
#[serde(rename_all = "camelCase")]
pub struct ConfigurationCatalog {
    pub path: String,
    pub effective: Effective,
    #[serde(skip_serializing_if = "Vec::is_empty")]
    pub scopes: Vec<Scope>,
    pub features: Vec<CatalogEntry>,
    pub mcp_tools: Vec<CatalogEntry>,
    pub domains: Vec<CatalogEntry>,
    #[serde(serialize_with = "null_if_empty")]
    pub inspections: Vec<ConfigurationInspection>,
    #[serde(skip_serializing_if = "String::is_empty")]
    pub error: String,
    #[serde(skip_serializing_if = "Vec::is_empty")]
    pub errors: Vec<ConfigurationIssue>,
}

#[derive(Debug, Clone, PartialEq, Eq, Serialize)]
pub struct ConfigurationIssue {
    pub path: String,
    pub message: String,
}

#[derive(Debug, Clone, PartialEq, Eq, Serialize)]
pub struct ConfigurationInspection {
    pub id: String,
    #[serde(serialize_with = "null_if_empty")]
    pub languages: Vec<String>,
    #[serde(serialize_with = "null_if_empty")]
    pub rules: Vec<ConfigurationDiagnostic>,
}

#[derive(Debug, Clone, PartialEq, Eq, Serialize)]
#[serde(rename_all = "camelCase")]
pub struct ConfigurationDiagnostic {
    pub id: String,
    pub source: String,
    pub default_severity: String,
    pub default_enabled: bool,
}

#[derive(Debug, Clone, PartialEq, Eq, Default, Serialize)]
#[serde(rename_all = "camelCase")]
pub struct ConfigurationReloadResult {
    pub applied: bool,
    pub restart_required: bool,
    #[serde(skip_serializing_if = "String::is_empty")]
    pub error: String,
    #[serde(skip_serializing_if = "Vec::is_empty")]
    pub errors: Vec<ConfigurationIssue>,
}

/// Go `errors.Join` for optional messages.
pub(crate) fn join_errors(left: Option<String>, right: Option<String>) -> Option<String> {
    match (left, right) {
        (Some(left), Some(right)) => Some(format!("{left}\n{right}")),
        (left, right) => left.or(right),
    }
}

fn join_all(errors: impl IntoIterator<Item = Option<String>>) -> Option<String> {
    errors.into_iter().fold(None, join_errors)
}

/// Builds the editor overlay from initialization options; explicit
/// configuration wins over the legacy PHP/Shopware option fields.
pub(crate) fn editor_configuration(options: &protocol::InitializationOptions) -> Partial {
    let mut result = options.configuration.clone().unwrap_or_default();
    let mut php = result.php.take().unwrap_or_default();
    if php.extensions.is_none() && !options.php_extensions.is_empty() {
        php.extensions = Some(options.php_extensions.clone());
    }
    if php.disabled_extensions.is_none() && !options.disabled_php_extensions.is_empty() {
        php.disabled_extensions = Some(options.disabled_php_extensions.clone());
    }
    if php.extensions.is_some() || php.disabled_extensions.is_some() {
        result.php = Some(php);
    }
    if !options.shopware_target_version.is_empty()
        && result
            .shopware
            .as_ref()
            .is_none_or(|shopware| shopware.target_version.is_none())
    {
        result
            .shopware
            .get_or_insert_with(Default::default)
            .target_version = Some(options.shopware_target_version.clone());
    }
    result
}

fn scope_configuration_issues(scopes: &[Scope]) -> Vec<ConfigurationIssue> {
    scopes
        .iter()
        .filter(|scope| !scope.error.is_empty())
        .map(|scope| ConfigurationIssue {
            path: scope.path.clone(),
            message: scope.error.clone(),
        })
        .collect()
}

fn diagnostic_inspection_enabled(policy: &DiagnosticPolicy, id: &str) -> bool {
    policy.enabled && policy.inspections.get(id).copied().unwrap_or(true)
}

pub(crate) fn inspection_enabled_by_policy(policy: &DiagnosticPolicy, id: &str) -> bool {
    diagnostic_inspection_enabled(policy, id)
}

pub(crate) fn diagnostic_rule_severity<'a>(
    policy: &'a DiagnosticPolicy,
    id: &DiagnosticId,
) -> Option<&'a Severity> {
    policy.rules.get(id.as_str())
}

pub(crate) fn diagnostic_severity_name(value: protocol::DiagnosticSeverity) -> &'static str {
    match value {
        protocol::DiagnosticSeverity::ERROR => "error",
        protocol::DiagnosticSeverity::INFORMATION => "information",
        protocol::DiagnosticSeverity::HINT => "hint",
        _ => "warning",
    }
}

pub(crate) fn protocol_diagnostic_severity(value: &Severity) -> protocol::DiagnosticSeverity {
    match value {
        Severity::Error => protocol::DiagnosticSeverity::ERROR,
        Severity::Information => protocol::DiagnosticSeverity::INFORMATION,
        Severity::Hint => protocol::DiagnosticSeverity::HINT,
        _ => protocol::DiagnosticSeverity::WARNING,
    }
}

fn validate_diagnostic_layer(
    value: &Partial,
    name: &str,
    inspection_ids: &BTreeSet<String>,
    rule_ids: &BTreeSet<String>,
) -> Option<String> {
    let diagnostics = value.diagnostics.as_ref()?;
    let mut errors = Vec::new();
    let mut validate = |inspections: &BTreeMap<String, bool>,
                        rules: &BTreeMap<String, Severity>| {
        for id in inspections.keys() {
            if !inspection_ids.contains(id) {
                errors.push(format!(
                    "{name} configuration has unknown inspection {}",
                    go_quote(id)
                ));
            }
        }
        for id in rules.keys() {
            if !rule_ids.contains(id) {
                errors.push(format!(
                    "{name} configuration has unknown diagnostic rule {}",
                    go_quote(id)
                ));
            }
        }
    };
    validate(&diagnostics.inspections, &diagnostics.rules);
    for entry in &diagnostics.overrides {
        validate(&entry.inspections, &entry.rules);
    }
    (!errors.is_empty()).then(|| errors.join("\n"))
}

impl Server {
    pub(crate) fn initialize_configuration(
        &self,
        root: &str,
        options: &protocol::InitializationOptions,
    ) -> Result<(), String> {
        let (project, mut load_error) = match projectconfig::load(root) {
            Ok(project) => (project.unwrap_or_default(), None),
            Err(error) if options.cli_mode => return Err(error.to_string()),
            Err(error) => {
                log::warn!("Invalid Shopware LSP configuration: {error}");
                (Partial::default(), Some(error.to_string()))
            }
        };
        let mut editor = editor_configuration(options);
        if let Err(error) = projectconfig::validate(&editor) {
            if options.cli_mode {
                return Err(format!("invalid editor configuration: {error}"));
            }
            load_error = join_errors(
                load_error,
                Some(format!("invalid editor configuration: {error}")),
            );
            editor = Partial::default();
        }
        let effective = projectconfig::resolve(&project, &editor);
        let mut issues = Vec::new();
        if load_error.is_some()
            && let Err(error) = projectconfig::load(root)
        {
            // Scope errors are added with their precise paths later; keep
            // the root issue only when the root file itself cannot load.
            issues.push(ConfigurationIssue {
                path: projectconfig::path(root),
                message: error.to_string(),
            });
        }
        let mut state = write(&self.configuration);
        state.project = project;
        state.editor = editor;
        state.effective = effective;
        state.error = load_error;
        state.issues = issues;
        Ok(())
    }

    pub(crate) fn install_initial_configuration_scopes(
        &self,
        loaded: Result<Vec<Scope>, String>,
    ) -> Result<(), String> {
        let (scopes, discovery_error) = match loaded {
            Ok(scopes) => (scopes, None),
            Err(error) => (Vec::new(), Some(error)),
        };
        let scope_error = join_errors(
            discovery_error.clone(),
            projectconfig::scope_errors(&scopes)
                .err()
                .map(|error| error.to_string()),
        );
        if let Some(error) = &scope_error
            && self.cli_mode()
        {
            return Err(error.clone());
        }
        let mut issues = scope_configuration_issues(&scopes);
        if let Some(error) = discovery_error {
            issues.push(ConfigurationIssue {
                path: projectconfig::path(&self.root_path()),
                message: error,
            });
        }
        let mut state = write(&self.configuration);
        state.scopes = scopes;
        state.error = join_errors(state.error.take(), scope_error);
        state.issues.extend(issues);
        Ok(())
    }

    pub(crate) fn join_configuration_error(&self, error: &str) {
        let mut state = write(&self.configuration);
        state.error = join_errors(state.error.take(), Some(error.to_owned()));
    }

    /// A copy of the effective configuration.
    pub fn effective_configuration(&self) -> Effective {
        read(&self.configuration).effective.clone()
    }

    pub(crate) fn feature_enabled(&self, id: &str) -> bool {
        read(&self.configuration).effective.feature_enabled(id)
    }

    /// The structural domain decision for workspace composition.
    pub fn domain_enabled(&self, id: &str) -> bool {
        read(&self.configuration).effective.domain_enabled(id)
    }

    pub(crate) fn diagnostics_enabled(&self) -> bool {
        read(&self.configuration).effective.diagnostics.enabled
    }

    /// The diagnostic policy for one document: project layer, matching
    /// nested extension scopes, then editor overrides.
    pub(crate) fn diagnostic_policy(&self, uri: &str) -> DiagnosticPolicy {
        let mut policy = DiagnosticPolicy::default();
        let document_path = swlsp_uriutil::path(uri)
            .ok()
            .and_then(|path| filepath::abs(&path).ok());
        let root = self.root_path();
        let state = read(&self.configuration);
        let mut workspace_relative = "\u{0}outside-workspace".to_owned();
        if let Some(path) = &document_path
            && projectconfig::contains(&root, path)
            && let Ok(relative) = filepath::rel(&root, path)
        {
            workspace_relative = filepath::to_slash(&relative);
        }
        projectconfig::apply_diagnostics(
            &mut policy,
            state.project.diagnostics.as_ref(),
            &workspace_relative,
        );
        if let Some(path) = &document_path {
            for scope in &state.scopes {
                let Some(diagnostics) = scope.configuration.diagnostics.as_ref() else {
                    continue;
                };
                if !projectconfig::contains(&scope.root, path) {
                    continue;
                }
                let Ok(relative) = filepath::rel(&scope.root, path) else {
                    continue;
                };
                projectconfig::apply_diagnostics(
                    &mut policy,
                    Some(diagnostics),
                    &filepath::to_slash(&relative),
                );
            }
        }
        projectconfig::apply_diagnostics(
            &mut policy,
            state.editor.diagnostics.as_ref(),
            &workspace_relative,
        );
        policy
    }

    pub(crate) fn configuration_error(&self) -> Option<String> {
        read(&self.configuration).error.clone()
    }

    pub(crate) fn notify_configuration_error(&self) {
        if let Some(error) = self.configuration_error() {
            self.notify(
                "window/showMessage",
                json!({"type": 1, "message": format!("Invalid Shopware LSP configuration: {error}")}),
            );
        }
    }

    pub(crate) fn configuration_catalog(&self) -> ConfigurationCatalog {
        let (effective, error, scopes, issues) = {
            let state = read(&self.configuration);
            (
                state.effective.clone(),
                state.error.clone(),
                state.scopes.clone(),
                state.issues.clone(),
            )
        };
        let registry = self.registry();
        let mut inspections: Vec<ConfigurationInspection> = registry
            .inspections
            .by_id
            .values()
            .map(|registered| {
                let mut rules: Vec<ConfigurationDiagnostic> = registered
                    .definition
                    .problems
                    .iter()
                    .map(|problem| ConfigurationDiagnostic {
                        id: problem.id.0.clone(),
                        source: problem.source.clone(),
                        default_severity: diagnostic_severity_name(problem.default_severity)
                            .to_owned(),
                        default_enabled: !problem.disabled_by_default,
                    })
                    .collect();
                rules.sort_by(|left, right| left.id.cmp(&right.id));
                ConfigurationInspection {
                    id: registered.definition.id.clone(),
                    languages: registered
                        .definition
                        .languages
                        .iter()
                        .map(|language| language.as_str().to_owned())
                        .collect(),
                    rules,
                }
            })
            .collect();
        inspections.sort_by(|left, right| left.id.cmp(&right.id));
        ConfigurationCatalog {
            path: projectconfig::path(&self.root_path()),
            effective,
            scopes,
            features: projectconfig::FEATURE_CATALOG.to_vec(),
            mcp_tools: projectconfig::MCP_TOOL_CATALOG.to_vec(),
            domains: projectconfig::DOMAIN_CATALOG.to_vec(),
            inspections,
            error: error.unwrap_or_default(),
            errors: issues,
        }
    }

    fn known_diagnostic_ids(&self) -> (BTreeSet<String>, BTreeSet<String>) {
        let registry = self.registry();
        let inspections = registry.inspections.by_id.keys().cloned().collect();
        let rules = registry
            .inspections
            .by_code
            .keys()
            .map(|id| id.0.clone())
            .collect();
        (inspections, rules)
    }

    pub(crate) fn validate_diagnostic_layer(&self, value: &Partial, name: &str) -> Option<String> {
        let (inspections, rules) = self.known_diagnostic_ids();
        validate_diagnostic_layer(value, name, &inspections, &rules)
    }

    /// Rejects unknown inspection and rule IDs in every layer. In editor
    /// mode invalid layers are reported as issues and invalid scopes are
    /// disabled.
    pub(crate) fn validate_configured_diagnostic_ids(&self) -> Result<(), String> {
        let (project, editor, scopes) = {
            let state = read(&self.configuration);
            (
                state.project.clone(),
                state.editor.clone(),
                state.scopes.clone(),
            )
        };
        let (inspection_ids, rule_ids) = self.known_diagnostic_ids();
        let cli = self.cli_mode();
        let root = self.root_path();
        let mut errors = Vec::new();
        for (name, value) in [("project", &project), ("editor", &editor)] {
            let layer_error = validate_diagnostic_layer(value, name, &inspection_ids, &rule_ids);
            if let Some(error) = &layer_error
                && name == "project"
                && !cli
            {
                write(&self.configuration).issues.push(ConfigurationIssue {
                    path: projectconfig::path(&root),
                    message: error.clone(),
                });
            }
            errors.push(layer_error);
        }
        for (index, scope) in scopes.iter().enumerate() {
            if !scope.error.is_empty() {
                continue;
            }
            let scope_error = validate_diagnostic_layer(
                &scope.configuration,
                &scope.path,
                &inspection_ids,
                &rule_ids,
            );
            if let Some(error) = &scope_error
                && !cli
            {
                let mut state = write(&self.configuration);
                if let Some(current) = state.scopes.get_mut(index)
                    && current.root == scope.root
                {
                    current.error = error.clone();
                    current.configuration = Partial::default();
                }
                state.issues.push(ConfigurationIssue {
                    path: scope.path.clone(),
                    message: error.clone(),
                });
            }
            errors.push(scope_error);
        }
        match join_all(errors) {
            Some(error) => Err(error),
            None => Ok(()),
        }
    }

    /// Re-reads the workspace and nested configuration files. Structural
    /// changes are reported as requiring a restart and are not applied.
    pub(crate) fn reload_project_configuration(&self) -> ConfigurationReloadResult {
        let root = self.root_path();
        let project = match projectconfig::load(&root) {
            Ok(project) => project.unwrap_or_default(),
            Err(error) => return self.reject_reload(&root, error.to_string()),
        };
        if let Some(error) = self.validate_diagnostic_layer(&project, "project") {
            return self.reject_reload(&root, error);
        }
        let (scopes, discovery_error) = match projectconfig::load_scopes(&root) {
            Ok(scopes) => (scopes, None),
            Err(error) => (Vec::new(), Some(error.to_string())),
        };
        let (old, editor, previous_scopes) = {
            let state = read(&self.configuration);
            (
                state.effective.clone(),
                state.editor.clone(),
                state.scopes.clone(),
            )
        };
        let scopes = self.validate_and_retain_scopes(scopes, &previous_scopes);
        let mut issues = scope_configuration_issues(&scopes);
        let configuration_error = join_errors(
            discovery_error.clone(),
            projectconfig::scope_errors(&scopes)
                .err()
                .map(|error| error.to_string()),
        );
        if let Some(error) = discovery_error {
            issues.push(ConfigurationIssue {
                path: projectconfig::path(&root),
                message: error,
            });
        }
        let next = projectconfig::resolve(&project, &editor);
        let (applied, restart_required, notify_restart) = {
            let mut state = write(&self.configuration);
            let (applied, restart_required, notify_restart) =
                apply_structural_guard(&mut state, &old, next);
            state.project = project;
            state.scopes = scopes;
            state.effective = applied.clone();
            state.error = configuration_error.clone();
            state.issues = issues.clone();
            (applied, restart_required, notify_restart)
        };
        self.refresh_configuration_effects(&old, &applied, true);
        if notify_restart {
            self.notify_configuration_restart_required();
        }
        ConfigurationReloadResult {
            applied: true,
            restart_required,
            error: configuration_error.unwrap_or_default(),
            errors: issues,
        }
    }

    fn reject_reload(&self, root: &str, error: String) -> ConfigurationReloadResult {
        let issues = vec![ConfigurationIssue {
            path: projectconfig::path(root),
            message: error.clone(),
        }];
        let mut state = write(&self.configuration);
        state.error = Some(error.clone());
        state.issues = issues.clone();
        ConfigurationReloadResult {
            error,
            errors: issues,
            ..ConfigurationReloadResult::default()
        }
    }

    fn validate_and_retain_scopes(&self, mut next: Vec<Scope>, previous: &[Scope]) -> Vec<Scope> {
        let previous_by_root: BTreeMap<&str, &Scope> = previous
            .iter()
            .filter(|scope| scope.configuration.diagnostics.is_some())
            .map(|scope| (scope.root.as_str(), scope))
            .collect();
        for scope in &mut next {
            if scope.error.is_empty()
                && let Some(error) =
                    self.validate_diagnostic_layer(&scope.configuration, &scope.path)
            {
                scope.error = error;
            }
            if scope.error.is_empty() {
                continue;
            }
            if let Some(old) = previous_by_root.get(scope.root.as_str()) {
                scope.configuration = old.configuration.clone();
            }
        }
        next
    }

    /// Replaces the editor overlay (`workspace/didChangeConfiguration`).
    pub(crate) fn replace_editor_configuration(
        &self,
        editor: Partial,
    ) -> ConfigurationReloadResult {
        if let Err(error) = projectconfig::validate(&editor) {
            write(&self.configuration).error = Some(error.to_string());
            return ConfigurationReloadResult {
                error: error.to_string(),
                ..ConfigurationReloadResult::default()
            };
        }
        if let Some(error) = self.validate_diagnostic_layer(&editor, "editor") {
            write(&self.configuration).error = Some(error.clone());
            return ConfigurationReloadResult {
                error,
                ..ConfigurationReloadResult::default()
            };
        }
        let (old, project, scopes, issues) = {
            let state = read(&self.configuration);
            (
                state.effective.clone(),
                state.project.clone(),
                state.scopes.clone(),
                state.issues.clone(),
            )
        };
        let next = projectconfig::resolve(&project, &editor);
        let scope_error = projectconfig::scope_errors(&scopes)
            .err()
            .map(|error| error.to_string());
        let (applied, restart_required, notify_restart) = {
            let mut state = write(&self.configuration);
            let result = apply_structural_guard(&mut state, &old, next);
            state.editor = editor;
            state.effective = result.0.clone();
            state.error = scope_error.clone();
            state.issues = issues.clone();
            result
        };
        self.refresh_configuration_effects(&old, &applied, false);
        if notify_restart {
            self.notify_configuration_restart_required();
        }
        ConfigurationReloadResult {
            applied: true,
            restart_required,
            error: scope_error.unwrap_or_default(),
            errors: issues,
        }
    }

    pub(crate) fn handle_did_change_configuration(&self, params: Value) -> Result<Value, RpcError> {
        #[derive(serde::Deserialize)]
        struct Params {
            #[serde(default)]
            settings: Option<Partial>,
        }
        let params: Params = decode_params(params)?;
        let result = self.replace_editor_configuration(params.settings.unwrap_or_default());
        if result.error.is_empty() {
            return Ok(Value::Null);
        }
        log::warn!("Invalid editor configuration: {}", result.error);
        self.notify(
            "window/showMessage",
            json!({"type": 1, "message": format!("Invalid Shopware LSP configuration: {}", result.error)}),
        );
        Ok(Value::Null)
    }

    fn refresh_configuration_effects(&self, old: &Effective, next: &Effective, force: bool) {
        if !force && old.features == next.features && old.diagnostics == next.diagnostics {
            return;
        }
        self.cancel_all_diagnostics();
        for document in self.documents.documents() {
            self.schedule_diagnostics(&document.uri, document.version, std::time::Duration::ZERO);
        }
    }

    fn notify_configuration_restart_required(&self) {
        self.notify(
            "shopware/configurationRestartRequired",
            json!({"message": "Shopware LSP configuration changes require a restart"}),
        );
    }
}

/// Keeps structural settings of the running workspace when they changed.
/// Returns the applied configuration, whether a restart is required and
/// whether the restart notification is new.
fn apply_structural_guard(
    state: &mut ConfigurationState,
    old: &Effective,
    next: Effective,
) -> (Effective, bool, bool) {
    let next_fingerprint = next.structural_fingerprint();
    let restart_required = old.structural_fingerprint() != next_fingerprint;
    let mut applied = next;
    if restart_required {
        applied.php = old.php.clone();
        applied.shopware = old.shopware.clone();
        applied.indexing = old.indexing.clone();
        applied.domains = old.domains.clone();
        applied.disabled_reason = old.disabled_reason.clone();
    }
    let notify = restart_required && state.pending_fingerprint != next_fingerprint;
    state.pending_fingerprint = if restart_required {
        next_fingerprint
    } else {
        String::new()
    };
    (applied, restart_required, notify)
}

/// The feature that gates a protocol method.
pub(crate) fn feature_for_method(method: &str) -> Option<&'static str> {
    Some(match method {
        "textDocument/completion" => "completion",
        "textDocument/definition" => "definition",
        "textDocument/implementation" => "implementation",
        "textDocument/prepareTypeHierarchy"
        | "typeHierarchy/supertypes"
        | "typeHierarchy/subtypes" => "typeHierarchy",
        "textDocument/prepareCallHierarchy"
        | "callHierarchy/incomingCalls"
        | "callHierarchy/outgoingCalls" => "callHierarchy",
        "textDocument/references" => "references",
        "textDocument/codeLens" | "codeLens/resolve" => "codeLens",
        "textDocument/hover" => "hover",
        "textDocument/signatureHelp" => "signatureHelp",
        "textDocument/rename" => "rename",
        "textDocument/inlayHint" => "inlayHints",
        "textDocument/documentLink" => "documentLinks",
        "textDocument/documentSymbol" => "documentSymbols",
        "textDocument/documentHighlight" => "documentHighlights",
        "textDocument/linkedEditingRange" => "linkedEditing",
        "textDocument/foldingRange" => "foldingRanges",
        "textDocument/formatting" => "formatting",
        "textDocument/selectionRange" => "selectionRanges",
        "textDocument/documentColor" | "textDocument/colorPresentation" => "documentColors",
        "textDocument/semanticTokens/full" => "semanticTokens",
        "textDocument/codeAction" | "codeAction/resolve" => "codeActions",
        "workspace/symbol" => "workspaceSymbols",
        "workspace/willRenameFiles" => "fileRename",
        "workspace/executeCommand" => "commands",
        _ => return None,
    })
}

/// A well-formed empty answer for a disabled feature.
pub(crate) fn disabled_feature_result(method: &str) -> Value {
    match method {
        "textDocument/completion" => json!({"isIncomplete": false, "items": []}),
        "textDocument/hover"
        | "textDocument/rename"
        | "textDocument/linkedEditingRange"
        | "codeLens/resolve"
        | "codeAction/resolve" => Value::Null,
        "textDocument/semanticTokens/full" => json!({"data": []}),
        _ => json!([]),
    }
}

pub(crate) fn is_configuration_method(method: &str) -> bool {
    method.starts_with("shopware/configuration")
}

/// The domain an inspection belongs to; empty when it is always available.
pub(crate) fn inspection_domain(id: &str) -> &'static str {
    match id {
        "php.semantic" | "php.imports" => return "php",
        "shopware.admin" => return "administration",
        "shopware.migration" => return "shopware.migrations",
        "shopware.snippet" => return "shopware.snippets",
        "shopware.app_script" => return "shopware.appScripts",
        "shopware.entity_snapshot" => return "shopware.entitySchema",
        "shopware.store_composer" => return "shopware.store",
        "shopware.theme" => return "shopware.theme",
        "shopware.twig_versioning" => return "shopware.twigVersioning",
        "shopware.dal" | "shopware.criteria" => return "shopware.dal",
        _ => {}
    }
    const PREFIXES: &[(&str, &str)] = &[
        ("symfony.doctrine", "symfony.doctrine"),
        ("symfony.console", "symfony.console"),
        ("symfony.event", "symfony.events"),
        ("symfony.messenger", "symfony.messenger"),
        ("symfony.serializer", "symfony.serializer"),
        ("symfony.validation", "symfony.validation"),
        ("symfony.asset", "symfony.assets"),
        ("symfony.service", "symfony.services"),
        ("symfony.container", "symfony.services"),
        ("symfony.duplicate", "symfony.services"),
        ("twig.component", "symfony.twigComponents"),
        ("twig.", "twig"),
        ("scss.", "scss"),
        ("symfony.", "symfony"),
        ("shopware.", "shopware"),
    ];
    PREFIXES
        .iter()
        .find(|(prefix, _)| id.starts_with(prefix))
        .map_or("", |(_, domain)| domain)
}
