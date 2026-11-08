//! Code actions: inspection quick fixes (eager and lazy), legacy provider
//! actions, workspace-plan validation and validated command edits.

use std::collections::BTreeMap;
use std::fmt;
use std::sync::Arc;

use serde::{Deserialize, Serialize};
use serde_json::Value;
use sha2::{Digest, Sha256};
use swlsp_lsp_protocol as protocol;
use swlsp_rewrite::{self as rewrite, DocumentPlan, HandleError, WorkspacePlan};
use swlsp_syntax::{LineIndex, TextRange, TextSize};
use swlsp_uriutil::{filepath, go_quote};

use crate::configuration::{
    diagnostic_rule_severity, inspection_domain, inspection_enabled_by_policy,
};
use crate::context::{BoxError, Context};
use crate::document::{TextDocument, parse_path};
use crate::inspection::{
    BoundFixEnvelope, DiagnosticId, DocumentResolver, DocumentSnapshot, FixContext, FixId,
    FixResolution, QuickFix, RegisteredInspection, code_string, decode_diagnostic_envelope,
    supports_language,
};
use crate::providers::{ActionProvider, CodeActionRequest};
use crate::server::{Server, read};

const CODE_ACTION_ENVELOPE_SCHEMA: i64 = 2;
const LEGACY_ACTION_SCHEMA: &str = "validated-workspace-edit-v1";

#[derive(Debug, Clone, Default, Serialize, Deserialize)]
#[serde(default)]
struct CodeActionEnvelope {
    schema: i64,
    inspection: String,
    fix: FixId,
    binding: i64,
    diagnostic: protocol::Diagnostic,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
struct LegacyActionSnapshot {
    uri: String,
    version: Option<i32>,
    digest: String,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
struct LegacyActionEnvelope {
    #[serde(rename = "type")]
    kind: String,
    snapshots: Vec<LegacyActionSnapshot>,
    edit: Option<protocol::WorkspaceEdit>,
}

/// Failure to resolve a document snapshot.
#[derive(Debug)]
pub enum DocumentError {
    Cancelled,
    InvalidUri(String),
    OutsideWorkspace(String),
    Read { uri: String, source: std::io::Error },
}

impl DocumentError {
    /// Whether the document does not exist on disk (Go `os.ErrNotExist`).
    pub fn is_not_found(&self) -> bool {
        matches!(self, DocumentError::Read { source, .. } if source.kind() == std::io::ErrorKind::NotFound)
    }
}

impl fmt::Display for DocumentError {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        match self {
            DocumentError::Cancelled => f.write_str("context canceled"),
            DocumentError::InvalidUri(error) => write!(f, "resolve document URI: {error}"),
            DocumentError::OutsideWorkspace(uri) => {
                write!(f, "document {} is outside the workspace", go_quote(uri))
            }
            DocumentError::Read { uri, source } => {
                write!(f, "read document {}: {source}", go_quote(uri))
            }
        }
    }
}

impl std::error::Error for DocumentError {
    fn source(&self) -> Option<&(dyn std::error::Error + 'static)> {
        match self {
            DocumentError::Read { source, .. } => Some(source),
            _ => None,
        }
    }
}

/// Generators and command adapters use this host to resolve snapshots and
/// validate the edits they produce.
pub trait WorkspaceEditHost: DocumentResolver {
    fn workspace_edit(
        &self,
        ctx: &Context,
        plan: &WorkspacePlan,
    ) -> Result<protocol::WorkspaceEdit, BoxError>;
}

/// Resolves a document, treating a missing in-workspace file as a create
/// target (`Ok(None)`).
pub fn resolve_optional_document(
    ctx: &Context,
    host: &dyn WorkspaceEditHost,
    uri: &str,
) -> Result<Option<DocumentSnapshot>, BoxError> {
    match host.resolve_document(ctx, uri) {
        Ok(snapshot) => Ok(Some(snapshot)),
        Err(error)
            if error
                .downcast_ref::<DocumentError>()
                .is_some_and(DocumentError::is_not_found) =>
        {
            Ok(None)
        }
        Err(error) => Err(error),
    }
}

/// Adds a create (no snapshot) or a versioned full replacement to `plan`.
pub fn add_document_replacement(
    plan: &mut WorkspacePlan,
    uri: &str,
    snapshot: Option<&DocumentSnapshot>,
    content: &str,
) -> Result<(), BoxError> {
    let Some(snapshot) = snapshot else {
        plan.creates.push(rewrite::CreateFilePlan {
            uri: uri.to_owned(),
            content: content.to_owned(),
        });
        return Ok(());
    };
    let document = &snapshot.document;
    let mut builder = rewrite::Builder::new(document.source());
    builder.replace_range(TextRange::up_to(TextSize::of(document.source())), content)?;
    let edits = builder.finish()?;
    plan.documents.push(
        DocumentPlan::new(uri, snapshot.version, document.source_arc().clone(), edits)
            .with_line_index(document.line_index_arc().clone()),
    );
    Ok(())
}

/// Reports whether `path` lies inside `root` (lexically, after making both
/// absolute).
pub(crate) fn path_within_root(root: &str, path: &str) -> bool {
    if root.is_empty() {
        return false;
    }
    let (Ok(root), Ok(path)) = (filepath::abs(root), filepath::abs(path)) else {
        return false;
    };
    let Ok(relative) = filepath::rel(&filepath::clean(&root), &filepath::clean(&path)) else {
        return false;
    };
    relative != ".." && !relative.starts_with(&format!("..{}", filepath::SEPARATOR))
}

fn same_optional_version(left: Option<i32>, right: Option<i32>) -> bool {
    left == right
}

fn stale() -> BoxError {
    Box::new(HandleError::Stale)
}

/// Whether `error` reports a stale or missing element handle.
pub fn is_stale_error(error: &(dyn std::error::Error + 'static)) -> bool {
    error.downcast_ref::<HandleError>().is_some()
}

fn source_digest(source: &str) -> String {
    let digest = Sha256::digest(source.as_bytes());
    digest.iter().map(|byte| format!("{byte:02x}")).collect()
}

fn strict_position_offset(
    index: &LineIndex,
    position: protocol::Position,
) -> Result<u32, BoxError> {
    let offset = index.offset_utf16(position.line, position.character);
    let (line, character) = index.position_utf16(offset);
    if line != position.line || character != position.character {
        return Err("invalid UTF-16 edit position".into());
    }
    Ok(offset)
}

fn kind_matches_only(kind: Option<&protocol::CodeActionKind>, only: &[String]) -> bool {
    if only.is_empty() {
        return true;
    }
    let value = kind.map_or("", |kind| kind.as_str());
    only.iter()
        .any(|requested| value == requested || value.starts_with(&format!("{requested}.")))
}

fn provider_matches_only(provider: &dyn ActionProvider, only: &[String]) -> bool {
    only.is_empty()
        || provider
            .code_action_kinds()
            .iter()
            .any(|kind| kind_matches_only(Some(kind), only))
}

fn bound_fix_at(values: &[BoundFixEnvelope], id: &FixId, binding: i64) -> Option<BoundFixEnvelope> {
    let index = usize::try_from(binding).ok()?;
    values.get(index).filter(|value| &value.id == id).cloned()
}

fn disabled_code_action(mut action: protocol::CodeAction, reason: &str) -> protocol::CodeAction {
    action.disabled = Some(protocol::CodeActionDisabled {
        reason: reason.to_owned(),
    });
    action.edit = None;
    action.command = None;
    action
}

/// Resolves documents for quick fixes from the server's snapshots.
struct ServerDocumentResolver(Server);

impl DocumentResolver for ServerDocumentResolver {
    fn resolve_document(&self, ctx: &Context, uri: &str) -> Result<DocumentSnapshot, BoxError> {
        self.0.resolve_snapshot(ctx, uri).map_err(Into::into)
    }
}

impl DocumentResolver for Server {
    /// Supplies command adapters with the same snapshots as fixes.
    fn resolve_document(&self, ctx: &Context, uri: &str) -> Result<DocumentSnapshot, BoxError> {
        match swlsp_uriutil::path(uri) {
            Ok(path) if path_within_root(&self.root_path(), &path) => {}
            _ => return Err(Box::new(DocumentError::OutsideWorkspace(uri.to_owned()))),
        }
        self.resolve_snapshot(ctx, uri).map_err(Into::into)
    }
}

impl WorkspaceEditHost for Server {
    fn workspace_edit(
        &self,
        ctx: &Context,
        plan: &WorkspacePlan,
    ) -> Result<protocol::WorkspaceEdit, BoxError> {
        self.validate_workspace_plan(ctx, plan)?;
        Ok(plan.workspace_edit()?)
    }
}

fn server_gone() -> BoxError {
    "language server has shut down".into()
}

impl DocumentResolver for crate::server::WeakServer {
    fn resolve_document(&self, ctx: &Context, uri: &str) -> Result<DocumentSnapshot, BoxError> {
        self.upgrade()
            .ok_or_else(server_gone)?
            .resolve_document(ctx, uri)
    }
}

impl WorkspaceEditHost for crate::server::WeakServer {
    fn workspace_edit(
        &self,
        ctx: &Context,
        plan: &WorkspacePlan,
    ) -> Result<protocol::WorkspaceEdit, BoxError> {
        self.upgrade()
            .ok_or_else(server_gone)?
            .workspace_edit(ctx, plan)
    }
}

impl Server {
    /// The open snapshot or the disk content of an in-workspace document.
    pub(crate) fn resolve_snapshot(
        &self,
        ctx: &Context,
        uri: &str,
    ) -> Result<DocumentSnapshot, DocumentError> {
        if ctx.is_cancelled() {
            return Err(DocumentError::Cancelled);
        }
        let path = swlsp_uriutil::path(uri)
            .map_err(|error| DocumentError::InvalidUri(error.to_string()))?;
        if !path_within_root(&self.root_path(), &path) {
            return Err(DocumentError::OutsideWorkspace(uri.to_owned()));
        }
        if let Some(document) = self.documents.get_document(uri) {
            let version = document.version;
            return Ok(DocumentSnapshot {
                document,
                version: Some(version),
            });
        }
        let content = std::fs::read(&path).map_err(|source| DocumentError::Read {
            uri: uri.to_owned(),
            source,
        })?;
        let document = TextDocument::new(uri, &String::from_utf8_lossy(&content), 0);
        Ok(DocumentSnapshot {
            document: Arc::new(document),
            version: None,
        })
    }

    fn document_resolver(&self) -> Arc<dyn DocumentResolver> {
        Arc::new(ServerDocumentResolver(self.clone()))
    }

    /// Validates a plan against the current snapshots: creates and deletes
    /// stay inside the workspace, edited sources and versions are unchanged
    /// and rewrites do not introduce parser errors.
    pub(crate) fn validate_workspace_plan(
        &self,
        ctx: &Context,
        plan: &WorkspacePlan,
    ) -> Result<(), BoxError> {
        let root = self.root_path();
        for created in &plan.creates {
            ctx.check()?;
            let path = swlsp_uriutil::path(&created.uri)
                .ok()
                .filter(|path| path_within_root(&root, path));
            let Some(path) = path else {
                return Err(format!(
                    "created document {} is outside the workspace",
                    go_quote(&created.uri)
                )
                .into());
            };
            if self.documents.get_document(&created.uri).is_some() {
                return Err(format!(
                    "created document {} is already open",
                    go_quote(&created.uri)
                )
                .into());
            }
            match std::fs::metadata(&path) {
                Err(error) if error.kind() == std::io::ErrorKind::NotFound => {}
                _ => {
                    return Err(format!(
                        "created document {} already exists or cannot be checked",
                        go_quote(&created.uri)
                    )
                    .into());
                }
            }
            if let Some((_, parse)) = parse_path(&created.uri, &created.content)
                && !parse.errors.is_empty()
            {
                return Err(
                    format!("created document has {} parser errors", parse.errors.len()).into(),
                );
            }
        }
        for planned in &plan.documents {
            ctx.check()?;
            let current = self.resolve_snapshot(ctx, &planned.uri)?;
            if current.document.source() != &*planned.source
                || !same_optional_version(current.version, planned.version)
            {
                return Err(stale());
            }
            let updated = planned.apply()?;
            if let Some((_, parse)) = parse_path(&planned.uri, &updated)
                && parse.errors.len() > current.document.parse_errors().len()
            {
                return Err(format!(
                    "rewrite introduces {} parser errors",
                    parse.errors.len() - current.document.parse_errors().len()
                )
                .into());
            }
        }
        for deleted in &plan.deletes {
            ctx.check()?;
            let path = swlsp_uriutil::path(&deleted.uri)
                .ok()
                .filter(|path| path_within_root(&root, path));
            let Some(path) = path else {
                return Err(format!(
                    "deleted document {} is outside the workspace",
                    go_quote(&deleted.uri)
                )
                .into());
            };
            if !std::fs::metadata(&path).is_ok_and(|metadata| metadata.is_file()) {
                return Err(format!(
                    "deleted document {} does not exist or is not a file",
                    go_quote(&deleted.uri)
                )
                .into());
            }
            let current = self.resolve_snapshot(ctx, &deleted.uri)?;
            if current.document.source() != deleted.source
                || !same_optional_version(current.version, deleted.version)
            {
                return Err(stale());
            }
        }
        Ok(())
    }

    /// `textDocument/codeAction`.
    pub(crate) fn code_action(
        &self,
        ctx: &Context,
        params: &protocol::CodeActionParams,
    ) -> Vec<protocol::CodeAction> {
        let syntax = self
            .documents
            .syntax_context(
                &params.text_document.uri,
                params.range.start.line,
                params.range.start.character,
            )
            .unwrap_or_default();
        let request = CodeActionRequest {
            params: params.clone(),
            syntax,
        };
        let mut actions = self.inspection_code_actions(ctx, &request);
        let registry = self.registry();
        for provider in &registry.actions {
            if ctx.is_cancelled() {
                break;
            }
            if !provider_matches_only(provider.as_ref(), &params.context.only) {
                continue;
            }
            for action in provider.get_code_actions(ctx, &request) {
                actions.push(self.normalize_provider_action(ctx, action));
            }
        }
        self.filter_code_actions_for_client(actions)
    }

    fn inspection_code_actions(
        &self,
        ctx: &Context,
        request: &CodeActionRequest,
    ) -> Vec<protocol::CodeAction> {
        let Some(document) = request.document.clone() else {
            return Vec::new();
        };
        let (Some(language), Some(tree)) = (document.language, document.tree()) else {
            return Vec::new();
        };
        let registry = self.registry();
        let resolve_support = read(&self.session).code_action_resolve_support;
        let mut result = Vec::new();
        for diagnostic in &request.params.context.diagnostics {
            if ctx.is_cancelled() {
                return result;
            }
            let Ok(envelope) = decode_diagnostic_envelope(diagnostic.data.as_ref()) else {
                continue;
            };
            if envelope.uri != document.uri
                || envelope.document_version != document.version
                || code_string(diagnostic.code.as_ref()) != envelope.code.0
            {
                continue;
            }
            let Some(registered) = registry.inspections.inspection(&envelope.inspection) else {
                continue;
            };
            let owner = registry.inspections.by_code.get(&envelope.code);
            if !owner.is_some_and(|owner| Arc::ptr_eq(owner, registered))
                || !supports_language(&registered.definition, document.language)
                || !self.inspection_action_enabled(&document.uri, registered, &envelope.code)
            {
                continue;
            }
            if envelope
                .anchor
                .resolve(&document.uri, document.version, language, tree)
                .is_err()
            {
                continue;
            }
            for (binding, bound) in envelope.fixes.iter().enumerate() {
                let Some(fix) = registered.fixes.get(&bound.id) else {
                    continue;
                };
                let fix_context = FixContext {
                    document: document.clone(),
                    diagnostic: diagnostic.clone(),
                    anchor: envelope.anchor.clone(),
                    problem_payload: envelope.payload.clone(),
                    fix_payload: bound.payload.clone(),
                    documents: self.document_resolver(),
                };
                let Ok(Some(presentation)) = fix.present(ctx, &fix_context) else {
                    continue;
                };
                if !kind_matches_only(presentation.kind.as_ref(), &request.params.context.only) {
                    continue;
                }
                let data = CodeActionEnvelope {
                    schema: CODE_ACTION_ENVELOPE_SCHEMA,
                    inspection: envelope.inspection.clone(),
                    fix: bound.id.clone(),
                    binding: binding as i64,
                    diagnostic: diagnostic.clone(),
                };
                let mut action = protocol::CodeAction {
                    title: presentation.title,
                    kind: presentation.kind,
                    diagnostics: vec![diagnostic.clone()],
                    is_preferred: presentation.preferred,
                    data: serde_json::to_value(data).ok(),
                    ..protocol::CodeAction::default()
                };
                if presentation.resolution == FixResolution::Eager || !resolve_support {
                    self.populate_inspection_edit(ctx, &mut action, fix.as_ref(), &fix_context);
                }
                result.push(action);
            }
        }
        result
    }

    /// `codeAction/resolve`: computes lazy edits after rechecking policy,
    /// document version and the diagnostic anchor.
    pub(crate) fn resolve_code_action(
        &self,
        ctx: &Context,
        action: protocol::CodeAction,
    ) -> protocol::CodeAction {
        let data = action.data.clone().unwrap_or(Value::Null);
        if let Ok(legacy) = LegacyActionEnvelope::deserialize(&data)
            && legacy.kind == LEGACY_ACTION_SCHEMA
        {
            let resolved = self.resolve_provider_action(ctx, action, legacy);
            return self.filter_resolved_code_action_for_client(resolved);
        }
        let Ok(data) = CodeActionEnvelope::deserialize(&data) else {
            return action;
        };
        if data.schema != CODE_ACTION_ENVELOPE_SCHEMA {
            return action;
        }
        let registry = self.registry();
        let Some(registered) = registry.inspections.inspection(&data.inspection) else {
            return disabled_code_action(
                action,
                "The inspection that provided this fix is no longer available",
            );
        };
        let Some(fix) = registered.fixes.get(&data.fix) else {
            return disabled_code_action(action, "The quick fix is no longer available");
        };
        let envelope = match decode_diagnostic_envelope(data.diagnostic.data.as_ref()) {
            Ok(envelope) if envelope.inspection == data.inspection => envelope,
            _ => return disabled_code_action(action, "The diagnostic data is no longer valid"),
        };
        if !self.inspection_action_enabled(&envelope.uri, registered, &envelope.code) {
            return disabled_code_action(
                action,
                "The diagnostic is disabled by the current configuration",
            );
        }
        let document = match self.documents.get_document(&envelope.uri) {
            Some(document) if document.version == envelope.document_version => document,
            _ => {
                return disabled_code_action(
                    action,
                    "The document changed; request code actions again",
                );
            }
        };
        let Some(bound) = bound_fix_at(&envelope.fixes, &data.fix, data.binding) else {
            return disabled_code_action(action, "The diagnostic no longer offers this fix");
        };
        let resolved = match (document.language, document.tree()) {
            (Some(language), Some(tree)) => envelope
                .anchor
                .resolve(&document.uri, document.version, language, tree)
                .is_ok(),
            _ => false,
        };
        if !resolved {
            return disabled_code_action(
                action,
                "The diagnostic target changed; request code actions again",
            );
        }
        let fix_context = FixContext {
            document,
            diagnostic: data.diagnostic.clone(),
            anchor: envelope.anchor.clone(),
            problem_payload: envelope.payload.clone(),
            fix_payload: bound.payload.clone(),
            documents: self.document_resolver(),
        };
        let Ok(Some(presentation)) = fix.present(ctx, &fix_context) else {
            return disabled_code_action(action, "The quick fix is no longer applicable");
        };
        let mut action = action;
        action.title = presentation.title;
        action.kind = presentation.kind;
        action.is_preferred = presentation.preferred;
        self.populate_inspection_edit(ctx, &mut action, fix.as_ref(), &fix_context);
        self.filter_resolved_code_action_for_client(action)
    }

    fn populate_inspection_edit(
        &self,
        ctx: &Context,
        action: &mut protocol::CodeAction,
        fix: &dyn QuickFix,
        fix_context: &FixContext,
    ) {
        action.edit = None;
        action.command = None;
        let disable = |action: &mut protocol::CodeAction, reason: &str| {
            action.disabled = Some(protocol::CodeActionDisabled {
                reason: reason.to_owned(),
            });
            action.edit = None;
            action.command = None;
        };
        if let Some(command_fix) = fix.as_command() {
            match command_fix.build_command(ctx, fix_context) {
                Ok(Some(command)) => {
                    action.disabled = None;
                    action.command = Some(command);
                }
                _ => disable(action, "The quick fix could not be prepared"),
            }
            return;
        }
        let Some(rewrite_fix) = fix.as_rewrite() else {
            disable(action, "The quick fix has no implementation");
            return;
        };
        let plan = match rewrite_fix.build(ctx, fix_context) {
            Ok(plan) => plan,
            Err(error) => {
                let reason = if is_stale_error(error.as_ref()) {
                    "The document changed; request code actions again"
                } else {
                    "The quick fix could not be prepared"
                };
                disable(action, reason);
                return;
            }
        };
        if self.validate_workspace_plan(ctx, &plan).is_err() {
            disable(action, "The generated edit is no longer valid");
            return;
        }
        match plan.workspace_edit() {
            Ok(edit) => {
                action.disabled = None;
                action.edit = Some(edit);
            }
            Err(_) => disable(action, "The generated edit is invalid"),
        }
    }

    fn inspection_action_enabled(
        &self,
        uri: &str,
        inspection: &RegisteredInspection,
        code: &DiagnosticId,
    ) -> bool {
        let id = inspection.definition.id.as_str();
        let policy = self.diagnostic_policy(uri);
        let domain = inspection_domain(id);
        if !self.inspection_presented_to_client(id)
            || (!domain.is_empty() && !self.domain_enabled(domain))
            || !inspection_enabled_by_policy(&policy, id)
        {
            return false;
        }
        let Some(definition) = inspection.problems.get(code) else {
            return false;
        };
        match diagnostic_rule_severity(&policy, code) {
            Some(severity) => *severity != swlsp_projectconfig::Severity::Off,
            None => !definition.disabled_by_default,
        }
    }

    /// Brings legacy text-edit providers through the same snapshot and
    /// validation boundary as typed inspection fixes.
    pub(crate) fn normalize_provider_action(
        &self,
        ctx: &Context,
        mut action: protocol::CodeAction,
    ) -> protocol::CodeAction {
        if let Some(disabled) = action.disabled.clone() {
            return disabled_code_action(action, &disabled.reason);
        }
        let Some(edit) = action.edit.clone() else {
            return action;
        };
        let (plan, snapshots) = match self.provider_edit_plan(ctx, &edit) {
            Ok(result) => result,
            Err(error) => {
                return disabled_code_action(
                    action,
                    &format!("The generated edit is invalid: {error}"),
                );
            }
        };
        let Ok(edit) = self.workspace_edit(ctx, &plan) else {
            return disabled_code_action(action, "The generated edit is no longer valid");
        };
        let envelope = LegacyActionEnvelope {
            kind: LEGACY_ACTION_SCHEMA.to_owned(),
            snapshots,
            edit: Some(edit.clone()),
        };
        action.edit = Some(edit);
        action.data = serde_json::to_value(envelope).ok();
        action
    }

    fn resolve_provider_action(
        &self,
        ctx: &Context,
        mut action: protocol::CodeAction,
        data: LegacyActionEnvelope,
    ) -> protocol::CodeAction {
        for expected in &data.snapshots {
            let current = self.resolve_document(ctx, &expected.uri);
            let unchanged = current.is_ok_and(|snapshot| {
                same_optional_version(snapshot.version, expected.version)
                    && source_digest(snapshot.document.source()) == expected.digest
            });
            if !unchanged {
                return disabled_code_action(
                    action,
                    "The target document changed; request code actions again",
                );
            }
        }
        action.edit = data.edit;
        self.normalize_provider_action(ctx, action)
    }

    fn provider_edit_plan(
        &self,
        ctx: &Context,
        edit: &protocol::WorkspaceEdit,
    ) -> Result<(WorkspacePlan, Vec<LegacyActionSnapshot>), BoxError> {
        let mut changes: BTreeMap<String, Vec<protocol::TextEdit>> = edit.changes.clone();
        let mut versions: BTreeMap<String, Option<i32>> = BTreeMap::new();
        for change in &edit.document_changes {
            // Resource operations must be emitted through the typed rewrite
            // or command API.
            let Some(document) = change
                .text_document
                .as_ref()
                .filter(|_| change.kind.is_empty())
            else {
                return Err("resource operation requires a workspace plan".into());
            };
            if changes.contains_key(&document.uri) {
                return Err("duplicate document edit".into());
            }
            changes.insert(document.uri.clone(), change.edits.clone());
            versions.insert(document.uri.clone(), document.version);
        }
        let mut plan = WorkspacePlan::default();
        let mut snapshots = Vec::with_capacity(changes.len());
        for (uri, edits) in changes {
            let snapshot = self.resolve_document(ctx, &uri)?;
            if let Some(version) = versions.get(&uri)
                && !same_optional_version(*version, snapshot.version)
            {
                return Err(stale());
            }
            let document = &snapshot.document;
            let mut builder = rewrite::Builder::new(document.source());
            for edit in &edits {
                let start = strict_position_offset(document.line_index(), edit.range.start)?;
                let end = strict_position_offset(document.line_index(), edit.range.end)?;
                if start > end {
                    return Err(
                        format!("rewrite range is outside the source: {start}..{end}").into(),
                    );
                }
                builder.replace_range(
                    TextRange::new(TextSize::from(start), TextSize::from(end)),
                    edit.new_text.clone(),
                )?;
            }
            let edits = builder.finish()?;
            snapshots.push(LegacyActionSnapshot {
                uri: uri.clone(),
                version: snapshot.version,
                digest: source_digest(document.source()),
            });
            plan.documents.push(
                DocumentPlan::new(uri, snapshot.version, document.source_arc().clone(), edits)
                    .with_line_index(document.line_index_arc().clone()),
            );
        }
        Ok((plan, snapshots))
    }
}
