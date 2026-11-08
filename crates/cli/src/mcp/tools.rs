//! Analysis tools (Go `mcp.go` handlers): diagnostics, code actions, hover,
//! definitions, references and workspace symbols. Paths are validated
//! against the workspace root before any session work; positions are
//! one-based and converted here.

use serde::de::DeserializeOwned;
use serde::{Deserialize, Serialize};
use serde_json::{Value, json};
use swlsp_lsp_protocol::{
    CodeAction, DiagnosticResult, Hover, Location, Position, Range, SymbolInformation,
    WorkspaceEdit,
};
use swlsp_uriutil::filepath;

use super::{McpRuntime, output, scaffold};
use crate::CliError;
use crate::edit::{EditMode, apply_workspace_edit};
use crate::features_write::{action_kind, matches_code_action_kind};
use crate::session::{
    CliDocument, CliSession, DiagnosticOutput, position_params, text_document_params,
};
use crate::target::code_action_selection;
use crate::workspace_commands::{
    code_text, diagnostic_severity, resolve_check_files_with_exclusions, severity_label,
};

const DEFAULT_DIAGNOSTIC_LIMIT: i64 = 200;
const MAXIMUM_DIAGNOSTIC_LIMIT: i64 = 1000;
const DEFAULT_SYMBOL_LIMIT: i64 = 50;
const MAXIMUM_SYMBOL_LIMIT: i64 = 200;

pub(crate) fn decode<T: DeserializeOwned>(arguments: Value) -> Result<T, CliError> {
    serde_json::from_value(arguments).map_err(|error| CliError::fail(error.to_string()))
}

pub(crate) fn dispatch(
    runtime: &McpRuntime<'_>,
    name: &str,
    arguments: Value,
) -> Result<Value, CliError> {
    match name {
        "shopware_diagnostics" => output(&diagnostics(runtime, decode(arguments)?)?),
        "shopware_code_actions" => output(&code_actions(runtime, decode(arguments)?)?),
        "shopware_apply_code_action" => output(&apply_code_action(runtime, decode(arguments)?)?),
        "shopware_hover" => output(&hover(runtime, decode(arguments)?)?),
        "shopware_definition" => output(&locations(
            runtime,
            decode(arguments)?,
            "textDocument/definition",
            false,
        )?),
        "shopware_references" => output(&locations(
            runtime,
            decode(arguments)?,
            "textDocument/references",
            true,
        )?),
        "shopware_workspace_symbols" => output(&workspace_symbols(runtime, decode(arguments)?)?),
        _ => scaffold::dispatch(runtime, name, arguments),
    }
}

#[derive(Deserialize, Default)]
#[serde(default, rename_all = "camelCase")]
struct DiagnosticsInput {
    path: String,
    severity: String,
    max_results: i64,
}

#[derive(Deserialize, Default)]
#[serde(default, rename_all = "camelCase")]
struct PositionInput {
    path: String,
    line: i64,
    column: i64,
}

#[derive(Deserialize, Default)]
#[serde(default, rename_all = "camelCase")]
struct CodeActionsInput {
    end_line: i64,
    end_column: i64,
    path: String,
    line: i64,
    column: i64,
    kind: String,
}

#[derive(Deserialize, Default)]
#[serde(default, rename_all = "camelCase")]
struct ApplyCodeActionInput {
    end_line: i64,
    end_column: i64,
    path: String,
    line: i64,
    column: i64,
    title: String,
    kind: String,
}

#[derive(Deserialize, Default)]
#[serde(default)]
struct WorkspaceSymbolsInput {
    query: String,
    limit: i64,
}

#[derive(Serialize, Clone, Copy)]
pub(crate) struct McpPosition {
    line: i64,
    column: i64,
}

#[derive(Serialize, Clone, Copy)]
pub(crate) struct McpRange {
    start: McpPosition,
    end: McpPosition,
}

#[derive(Serialize)]
pub(crate) struct McpLocation {
    path: String,
    range: McpRange,
}

#[derive(Serialize)]
struct McpRelatedDiagnostic {
    location: McpLocation,
    message: String,
}

#[derive(Serialize)]
struct McpDiagnostic {
    path: String,
    range: McpRange,
    severity: String,
    #[serde(skip_serializing_if = "String::is_empty")]
    code: String,
    #[serde(skip_serializing_if = "String::is_empty")]
    source: String,
    message: String,
    #[serde(skip_serializing_if = "Vec::is_empty")]
    tags: Vec<String>,
    #[serde(skip_serializing_if = "Vec::is_empty")]
    related: Vec<McpRelatedDiagnostic>,
}

#[derive(Serialize)]
#[serde(rename_all = "camelCase")]
struct McpCodeAction {
    title: String,
    #[serde(skip_serializing_if = "String::is_empty")]
    kind: String,
    #[serde(skip_serializing_if = "is_false")]
    preferred: bool,
    #[serde(skip_serializing_if = "String::is_empty")]
    disabled_reason: String,
    has_edit: bool,
    resolvable: bool,
    #[serde(skip_serializing_if = "String::is_empty")]
    command: String,
}

fn is_false(value: &bool) -> bool {
    !*value
}

#[derive(Serialize)]
#[serde(rename_all = "camelCase")]
struct DiagnosticsOutput {
    root: String,
    path: String,
    files_checked: usize,
    severity: String,
    total: usize,
    diagnostics: Vec<McpDiagnostic>,
    truncated: bool,
}

#[derive(Serialize)]
struct CodeActionsOutput {
    path: String,
    position: McpPosition,
    actions: Vec<McpCodeAction>,
}

#[derive(Serialize)]
struct ApplyCodeActionOutput {
    applied: bool,
    title: String,
    files: Vec<String>,
    diff: String,
}

#[derive(Serialize)]
struct McpHover {
    kind: String,
    value: String,
    #[serde(skip_serializing_if = "Option::is_none")]
    range: Option<McpRange>,
}

#[derive(Serialize)]
struct HoverOutput {
    path: String,
    hover: Option<McpHover>,
}

#[derive(Serialize)]
struct LocationsOutput {
    path: String,
    position: McpPosition,
    locations: Vec<McpLocation>,
}

#[derive(Serialize)]
struct McpWorkspaceSymbol {
    name: String,
    kind: String,
    container: String,
    location: McpLocation,
}

#[derive(Serialize)]
struct WorkspaceSymbolsOutput {
    query: String,
    total: usize,
    symbols: Vec<McpWorkspaceSymbol>,
    truncated: bool,
}

pub(crate) fn bounded_limit(
    value: i64,
    default: i64,
    maximum: i64,
    name: &str,
) -> Result<i64, CliError> {
    if value == 0 {
        return Ok(default);
    }
    if value < 1 || value > maximum {
        return Err(CliError::fail(format!(
            "{name} must be between 1 and {maximum}"
        )));
    }
    Ok(value)
}

fn diagnostics(
    runtime: &McpRuntime<'_>,
    input: DiagnosticsInput,
) -> Result<DiagnosticsOutput, CliError> {
    let target = runtime.resolve_path(&input.path)?;
    let limit = bounded_limit(
        input.max_results,
        DEFAULT_DIAGNOSTIC_LIMIT,
        MAXIMUM_DIAGNOSTIC_LIMIT,
        "maxResults",
    )? as usize;
    runtime.with_session(|session| {
        let mut severity = input.severity.trim().to_owned();
        if severity.is_empty() {
            severity = session.configuration.severity.clone();
        }
        let cutoff = diagnostic_severity(&severity)?;
        let paths = resolve_check_files_with_exclusions(
            &session.cancel,
            &runtime.root,
            std::slice::from_ref(&target),
            &session.configuration.exclude,
        )?;
        let mut findings = Vec::new();
        let mut total = 0;
        for path in &paths {
            for finding in session.check_document(path, cutoff)? {
                total += 1;
                if findings.len() < limit {
                    findings.push(runtime.convert_diagnostic(&finding));
                }
            }
        }
        Ok(DiagnosticsOutput {
            root: runtime.root.clone(),
            path: runtime.output_path(&target),
            files_checked: paths.len(),
            severity: severity.to_lowercase(),
            total,
            truncated: total > findings.len(),
            diagnostics: findings,
        })
    })
}

fn code_actions(
    runtime: &McpRuntime<'_>,
    input: CodeActionsInput,
) -> Result<CodeActionsOutput, CliError> {
    let (path, position) = runtime.position(&input.path, input.line, input.column)?;
    let selection = code_action_selection(position, input.end_line, input.end_column)?;
    runtime.with_session(|session| {
        with_code_actions(session, &path, selection, &input.kind, |_, actions| {
            let actions = actions
                .into_iter()
                .map(|action| McpCodeAction {
                    kind: action_kind(&action),
                    preferred: action.is_preferred,
                    has_edit: action.edit.is_some(),
                    resolvable: action.edit.is_none() && action.data.is_some(),
                    disabled_reason: action
                        .disabled
                        .as_ref()
                        .map(|disabled| disabled.reason.clone())
                        .unwrap_or_default(),
                    command: action
                        .command
                        .as_ref()
                        .map(|command| command.command.clone())
                        .unwrap_or_default(),
                    title: action.title,
                })
                .collect();
            Ok(CodeActionsOutput {
                path: runtime.output_path(&path),
                position: to_mcp_position(position),
                actions,
            })
        })
    })
}

fn apply_code_action(
    runtime: &McpRuntime<'_>,
    input: ApplyCodeActionInput,
) -> Result<ApplyCodeActionOutput, CliError> {
    if input.title.trim().is_empty() {
        return Err(CliError::fail("title is required"));
    }
    let (path, position) = runtime.position(&input.path, input.line, input.column)?;
    let selection = code_action_selection(position, input.end_line, input.end_column)?;
    runtime.with_session(|session| {
        with_code_actions(session, &path, selection, &input.kind, |_, actions| {
            let mut matches: Vec<CodeAction> = actions
                .into_iter()
                .filter(|action| action.title == input.title)
                .collect();
            if matches.is_empty() {
                return Err(CliError::fail(format!(
                    "no code action with exact title {:?}",
                    input.title
                )));
            }
            if matches.len() > 1 {
                return Err(CliError::fail(format!(
                    "code action title {:?} is ambiguous; also provide kind",
                    input.title
                )));
            }
            let mut action = matches.remove(0);
            let disabled = |action: &CodeAction| {
                action.disabled.as_ref().map(|disabled| {
                    CliError::fail(format!(
                        "code action {:?} is disabled: {}",
                        action.title, disabled.reason
                    ))
                })
            };
            if let Some(error) = disabled(&action) {
                return Err(error);
            }
            if action.edit.is_none() && action.data.is_some() {
                let resolved: Option<CodeAction> = session.call(
                    "codeAction/resolve",
                    serde_json::to_value(&action).unwrap_or(Value::Null),
                )?;
                action = resolved.unwrap_or_default();
                if let Some(error) = disabled(&action) {
                    return Err(error);
                }
            }
            let Some(edit) = action.edit.as_ref() else {
                if let Some(command) = &action.command {
                    return Err(CliError::fail(format!(
                        "code action {:?} requires unsupported editor command {:?}",
                        action.title, command.command
                    )));
                }
                return Err(CliError::fail(format!(
                    "code action {:?} returned no edits",
                    action.title
                )));
            };
            runtime.validate_workspace_edit(edit)?;
            let mut diff = Vec::new();
            apply_workspace_edit(
                &mut diff,
                Some(edit),
                EditMode {
                    write: true,
                    diff: true,
                },
            )?;
            Ok(ApplyCodeActionOutput {
                applied: true,
                title: action.title.clone(),
                files: runtime.workspace_edit_paths(edit),
                diff: String::from_utf8_lossy(&diff).into_owned(),
            })
        })
    })
}

fn hover(runtime: &McpRuntime<'_>, input: PositionInput) -> Result<HoverOutput, CliError> {
    let (path, position) = runtime.position(&input.path, input.line, input.column)?;
    runtime.with_session(|session| {
        with_document(session, &path, |document| {
            let hover: Option<Hover> = session.call(
                "textDocument/hover",
                position_params(&document.uri, position),
            )?;
            let hover = hover.map(|hover| McpHover {
                kind: serde_json::to_value(&hover.contents.kind)
                    .ok()
                    .and_then(|kind| kind.as_str().map(str::to_owned))
                    .unwrap_or_default(),
                value: hover.contents.value,
                range: hover.range.map(to_mcp_range),
            });
            Ok(HoverOutput {
                path: runtime.output_path(&path),
                hover,
            })
        })
    })
}

fn locations(
    runtime: &McpRuntime<'_>,
    input: PositionInput,
    method: &str,
    include_declaration: bool,
) -> Result<LocationsOutput, CliError> {
    let (path, position) = runtime.position(&input.path, input.line, input.column)?;
    runtime.with_session(|session| {
        with_document(session, &path, |document| {
            let mut params = position_params(&document.uri, position);
            if include_declaration {
                params["context"] = json!({"includeDeclaration": true});
            }
            let locations: Option<Vec<Location>> = session.call(method, params)?;
            Ok(LocationsOutput {
                path: runtime.output_path(&path),
                position: to_mcp_position(position),
                locations: locations
                    .unwrap_or_default()
                    .iter()
                    .map(|location| runtime.convert_location(location))
                    .collect(),
            })
        })
    })
}

fn workspace_symbols(
    runtime: &McpRuntime<'_>,
    input: WorkspaceSymbolsInput,
) -> Result<WorkspaceSymbolsOutput, CliError> {
    if input.query.trim().is_empty() {
        return Err(CliError::fail("query is required"));
    }
    let limit = bounded_limit(
        input.limit,
        DEFAULT_SYMBOL_LIMIT,
        MAXIMUM_SYMBOL_LIMIT,
        "limit",
    )? as usize;
    runtime.with_session(|session| {
        let symbols: Option<Vec<SymbolInformation>> =
            session.call("workspace/symbol", json!({"query": input.query}))?;
        let symbols = symbols.unwrap_or_default();
        let total = symbols.len();
        let result: Vec<McpWorkspaceSymbol> = symbols
            .iter()
            .take(limit)
            .map(|symbol| McpWorkspaceSymbol {
                name: symbol.name.clone(),
                kind: symbol_kind_label(symbol_kind_number(symbol)),
                container: symbol.container_name.clone(),
                location: runtime.convert_location(&symbol.location),
            })
            .collect();
        Ok(WorkspaceSymbolsOutput {
            query: input.query.clone(),
            total,
            truncated: total > result.len(),
            symbols: result,
        })
    })
}

fn symbol_kind_number(symbol: &SymbolInformation) -> i64 {
    serde_json::to_value(symbol.kind)
        .ok()
        .and_then(|value| value.as_i64())
        .unwrap_or(0)
}

fn symbol_kind_label(kind: i64) -> String {
    const LABELS: [&str; 27] = [
        "",
        "file",
        "module",
        "namespace",
        "package",
        "class",
        "method",
        "property",
        "field",
        "constructor",
        "enum",
        "interface",
        "function",
        "variable",
        "constant",
        "string",
        "number",
        "boolean",
        "array",
        "object",
        "key",
        "null",
        "enumMember",
        "struct",
        "event",
        "operator",
        "typeParameter",
    ];
    if kind > 0 && (kind as usize) < LABELS.len() {
        return LABELS[kind as usize].to_owned();
    }
    format!("kind-{kind}")
}

pub(crate) fn with_document<T>(
    session: &CliSession,
    path: &str,
    operation: impl FnOnce(&CliDocument) -> Result<T, CliError>,
) -> Result<T, CliError> {
    let document = session.open_document(path)?;
    let result = operation(&document);
    let closed = session.close_document(&document);
    match (result, closed) {
        (Ok(value), Ok(())) => Ok(value),
        (Err(error), Ok(())) | (Ok(_), Err(error)) => Err(error),
        (Err(first), Err(second)) => Err(CliError::fail(format!(
            "{}\n{}",
            first.message, second.message
        ))),
    }
}

fn with_code_actions<T>(
    session: &CliSession,
    path: &str,
    selection: Range,
    kind: &str,
    operation: impl FnOnce(&CliDocument, Vec<CodeAction>) -> Result<T, CliError>,
) -> Result<T, CliError> {
    with_document(session, path, |document| {
        let diagnostics: Option<DiagnosticResult> = session.call(
            "textDocument/diagnostic",
            text_document_params(&document.uri),
        )?;
        let mut params = position_params(&document.uri, selection.start);
        params["range"] = json!(selection);
        let mut context = json!({"diagnostics": diagnostics.unwrap_or_default().items});
        if !kind.is_empty() {
            context["only"] = json!([kind]);
        }
        params["context"] = context;
        let actions: Option<Vec<CodeAction>> = session.call("textDocument/codeAction", params)?;
        let mut actions = actions.unwrap_or_default();
        if !kind.is_empty() {
            actions.retain(|action| matches_code_action_kind(&action_kind(action), kind));
        }
        operation(document, actions)
    })
}

pub(crate) fn to_mcp_position(position: Position) -> McpPosition {
    McpPosition {
        line: i64::from(position.line) + 1,
        column: i64::from(position.character) + 1,
    }
}

pub(crate) fn to_mcp_range(range: Range) -> McpRange {
    McpRange {
        start: to_mcp_position(range.start),
        end: to_mcp_position(range.end),
    }
}

impl McpRuntime<'_> {
    /// Go `position`: a workspace path plus a one-based position.
    pub(crate) fn position(
        &self,
        path: &str,
        line: i64,
        column: i64,
    ) -> Result<(String, Position), CliError> {
        let resolved = self.resolve_path(path)?;
        if line < 1 {
            return Err(CliError::fail("line must be at least 1"));
        }
        let column = if column == 0 { 1 } else { column };
        if column < 1 {
            return Err(CliError::fail("column must be at least 1"));
        }
        Ok((
            resolved,
            Position {
                line: (line - 1) as u32,
                character: (column - 1) as u32,
            },
        ))
    }

    /// Go `resolvePath`: workspace-relative or absolute, never outside the
    /// root.
    pub(crate) fn resolve_path(&self, value: &str) -> Result<String, CliError> {
        let mut value = value.trim().to_owned();
        if value.is_empty() {
            return Err(CliError::fail("path is required"));
        }
        if value.starts_with("file://") {
            value = swlsp_uriutil::path(&value)
                .map_err(|error| CliError::fail(format!("resolve path URI: {error}")))?;
        }
        if !filepath::is_abs(&value) {
            value = filepath::join(&[self.root.as_str(), value.as_str()]);
        }
        let absolute = filepath::abs(&value)
            .map_err(|error| CliError::fail(format!("resolve path: {error}")))?;
        let absolute = filepath::clean(&absolute);
        if !path_within_root(&self.root, &absolute) {
            return Err(CliError::fail(format!(
                "path is outside workspace root: {absolute}"
            )));
        }
        Ok(absolute)
    }

    pub(crate) fn output_path(&self, path: &str) -> String {
        let path = filepath::clean(path);
        if path_within_root(&self.root, &path)
            && let Ok(relative) = filepath::rel(&self.root, &path)
        {
            if relative == "." {
                return ".".into();
            }
            return filepath::to_slash(&relative);
        }
        path
    }

    pub(crate) fn output_uri(&self, uri: &str) -> String {
        match swlsp_uriutil::path(uri) {
            Ok(path) => self.output_path(&path),
            Err(_) => uri.to_owned(),
        }
    }

    pub(crate) fn convert_location(&self, location: &Location) -> McpLocation {
        McpLocation {
            path: self.output_uri(&location.uri),
            range: to_mcp_range(location.range),
        }
    }

    fn convert_diagnostic(&self, finding: &DiagnosticOutput) -> McpDiagnostic {
        let diagnostic = &finding.diagnostic;
        let severity = crate::session::diagnostic_severity_value(diagnostic);
        let tags = diagnostic
            .tags
            .iter()
            .map(|tag| {
                let number = serde_json::to_value(tag)
                    .ok()
                    .and_then(|value| value.as_i64())
                    .unwrap_or(0);
                match number {
                    1 => "unnecessary".to_owned(),
                    2 => "deprecated".to_owned(),
                    other => format!("tag-{other}"),
                }
            })
            .collect();
        McpDiagnostic {
            path: self.output_uri(&finding.uri),
            range: to_mcp_range(diagnostic.range),
            severity: severity_label(severity).to_lowercase(),
            code: match &diagnostic.code {
                None | Some(Value::Null) => String::new(),
                Some(code) => code_text(code),
            },
            source: diagnostic.source.clone(),
            message: diagnostic.message.clone(),
            tags,
            related: finding
                .related
                .iter()
                .flatten()
                .map(|related| McpRelatedDiagnostic {
                    location: self.convert_location(&related.location),
                    message: related.message.clone(),
                })
                .collect(),
        }
    }

    /// Rejects edits that touch paths outside the workspace or use
    /// unsupported resource operations (Go `validateWorkspaceEdit`).
    pub(crate) fn validate_workspace_edit(&self, edit: &WorkspaceEdit) -> Result<(), CliError> {
        let reject = |uri: &str, error: CliError| {
            CliError::fail(format!(
                "reject workspace edit target {:?}: {}",
                uri, error.message
            ))
        };
        for uri in edit.changes.keys() {
            self.resolve_path(uri).map_err(|error| reject(uri, error))?;
        }
        for change in &edit.document_changes {
            if !change.kind.is_empty()
                && change.kind != swlsp_lsp_protocol::CREATE_FILE_OPERATION
                && change.kind != swlsp_lsp_protocol::DELETE_FILE_OPERATION
            {
                return Err(CliError::fail(format!(
                    "reject unsupported workspace resource operation {:?}",
                    change.kind
                )));
            }
            let uri = change
                .text_document
                .as_ref()
                .map(|document| document.uri.as_str())
                .unwrap_or(change.uri.as_str());
            if uri.is_empty() {
                continue;
            }
            self.resolve_path(uri).map_err(|error| reject(uri, error))?;
        }
        Ok(())
    }

    pub(crate) fn workspace_edit_paths(&self, edit: &WorkspaceEdit) -> Vec<String> {
        let mut result: Vec<String> = Vec::new();
        let mut add = |uri: &str| {
            let path = self.output_uri(uri);
            if !result.contains(&path) {
                result.push(path);
            }
        };
        for uri in edit.changes.keys() {
            add(uri);
        }
        for change in &edit.document_changes {
            if let Some(document) = &change.text_document {
                add(&document.uri);
            } else if !change.uri.is_empty() {
                add(&change.uri);
            }
        }
        result.sort();
        result
    }
}

/// Go `pathWithinRoot`.
pub(crate) fn path_within_root(root: &str, path: &str) -> bool {
    match filepath::rel(root, path) {
        Ok(relative) => {
            !filepath::is_abs(&relative) && relative != ".." && !relative.starts_with("../")
        }
        Err(_) => false,
    }
}
