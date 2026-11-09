//! Workspace diagnostics (`workspace/diagnostic`): problems in files that
//! are not open.
//!
//! Inspections name the files they may report on although nobody opened
//! them (`Inspection::workspace_paths`, from project rules' summaries). Those
//! files are loaded from disk and diagnosed by the whole pipeline, so a file
//! shows the same problems in the workspace view as when it is opened. Open
//! documents are left to document pulls. The report is computed once per
//! index and configuration state and served with result IDs, so a client
//! polling for it gets `unchanged` for files whose problems did not change;
//! files that no longer have problems get an empty report once, so clients
//! clear them.

use std::collections::{BTreeMap, HashMap};
use std::hash::{DefaultHasher, Hash, Hasher};
use std::sync::Arc;

use serde::Deserialize;
use serde_json::{Value, json};
use swlsp_lsp_protocol as protocol;

use crate::context::{Context, lock};
use crate::document::TextDocument;
use crate::server::Server;

#[derive(Debug, Default, Deserialize)]
#[serde(rename_all = "camelCase", default)]
pub(crate) struct WorkspaceDiagnosticParams {
    pub previous_result_ids: Vec<PreviousResultId>,
}

#[derive(Debug, Default, Deserialize)]
#[serde(default)]
pub(crate) struct PreviousResultId {
    pub uri: String,
    pub value: String,
}

/// The computed workspace report of one index and configuration state.
#[derive(Default)]
pub(crate) struct WorkspaceReport {
    pub generation: u64,
    /// URI to (result ID, diagnostics).
    pub files: BTreeMap<String, (String, Vec<protocol::Diagnostic>)>,
}

impl Server {
    /// `workspace/diagnostic`: the problems of files that are not open.
    pub(crate) fn workspace_diagnostic(
        &self,
        ctx: &Context,
        params: &WorkspaceDiagnosticParams,
    ) -> Value {
        let report = self.workspace_report(ctx);
        let previous: HashMap<&str, &str> = params
            .previous_result_ids
            .iter()
            .map(|previous| (previous.uri.as_str(), previous.value.as_str()))
            .collect();
        let mut items = Vec::new();
        for (uri, (result_id, diagnostics)) in &report.files {
            // Open documents are reported through document pulls.
            if self.documents.get_document(uri).is_some() {
                continue;
            }
            if previous.get(uri.as_str()) == Some(&result_id.as_str()) {
                items.push(json!({
                    "kind": "unchanged",
                    "uri": uri,
                    "version": Value::Null,
                    "resultId": result_id,
                }));
            } else {
                items.push(json!({
                    "kind": "full",
                    "uri": uri,
                    "version": Value::Null,
                    "resultId": result_id,
                    "items": diagnostics,
                }));
            }
        }
        // Files the client still shows that no longer have problems.
        for previous in &params.previous_result_ids {
            if !report.files.contains_key(&previous.uri)
                && self.documents.get_document(&previous.uri).is_none()
            {
                items.push(json!({
                    "kind": "full",
                    "uri": previous.uri,
                    "version": Value::Null,
                    "items": [],
                }));
            }
        }
        json!({"items": items})
    }

    fn workspace_report(&self, ctx: &Context) -> Arc<WorkspaceReport> {
        let generation = {
            let state = lock(&self.diagnostics);
            if let Some(report) = &state.workspace_report
                && report.generation == state.workspace_generation
            {
                return report.clone();
            }
            state.workspace_generation
        };
        let mut report = WorkspaceReport {
            generation,
            ..WorkspaceReport::default()
        };
        if self.diagnostics_enabled() {
            let mut paths: Vec<String> = self
                .registry()
                .inspections
                .by_id
                .values()
                .flat_map(|inspection| inspection.inspection.workspace_paths(ctx))
                .collect();
            paths.sort();
            paths.dedup();
            for path in paths {
                if ctx.is_cancelled() {
                    return Arc::new(report);
                }
                let uri = swlsp_uriutil::file_uri(&path);
                let Ok(source) = std::fs::read(&path) else {
                    continue;
                };
                let document = Arc::new(TextDocument::new(
                    uri.clone(),
                    &String::from_utf8_lossy(&source),
                    0,
                ));
                let diagnostics = self.collect_diagnostics_with_progress(ctx, &document, None);
                if diagnostics.is_empty() {
                    continue;
                }
                let mut hasher = DefaultHasher::new();
                serde_json::to_string(&diagnostics)
                    .unwrap_or_default()
                    .hash(&mut hasher);
                let result_id = format!("{:x}", hasher.finish());
                report.files.insert(uri, (result_id, diagnostics));
            }
        }
        let report = Arc::new(report);
        if !ctx.is_cancelled() {
            let mut state = lock(&self.diagnostics);
            if state.workspace_generation == generation {
                state.workspace_report = Some(report.clone());
            }
        }
        report
    }
}
