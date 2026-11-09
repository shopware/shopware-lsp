//! Custom `shopware/...` workspace commands.
//!
//! Command IDs and JSON shapes are client contracts used by the editors.
//! Commands never write files: edits are compiled from a validated
//! [`WorkspacePlan`](swlsp_rewrite::WorkspacePlan) through the host and
//! returned to the client.

mod console;
mod extension;
mod json_edit;
mod pretty;
mod snippet;
mod twig;

#[cfg(test)]
mod tests;

use serde::Serialize;
use serde_json::Value;
use swlsp_lsp::{
    BoxError, Context, DocumentSnapshot, WorkspaceEditHost, add_document_replacement,
    resolve_optional_document,
};
use swlsp_lsp_protocol as protocol;
use swlsp_rewrite::WorkspacePlan;

pub use console::ConsoleCatalogProvider;
pub use extension::ExtensionCommandProvider;
pub use snippet::{SnippetCommandProvider, SnippetFile};
pub use twig::TwigCommandProvider;

/// Resolves snapshots, lists tracked resources and validates the edits
/// commands produce.
pub trait EditHost: WorkspaceEditHost {
    fn resource_paths(
        &self,
        ctx: &Context,
        directory: &str,
        limit: usize,
    ) -> Result<Vec<String>, BoxError>;
}

impl EditHost for swlsp_lsp::Server {
    fn resource_paths(
        &self,
        _ctx: &Context,
        directory: &str,
        limit: usize,
    ) -> Result<Vec<String>, BoxError> {
        swlsp_lsp::Server::resource_paths(self, directory, limit)
    }
}

impl EditHost for swlsp_lsp::WeakServer {
    fn resource_paths(
        &self,
        _ctx: &Context,
        directory: &str,
        limit: usize,
    ) -> Result<Vec<String>, BoxError> {
        let server = self
            .upgrade()
            .ok_or_else(|| BoxError::from("language server has shut down"))?;
        swlsp_lsp::Server::resource_paths(&server, directory, limit)
    }
}

/// `{ "edit": WorkspaceEdit }`.
#[derive(Debug, Clone, Serialize)]
pub struct EditResponse {
    pub edit: Option<protocol::WorkspaceEdit>,
}

fn target_snapshot(
    ctx: &Context,
    host: &dyn EditHost,
    uri: &str,
) -> Result<Option<DocumentSnapshot>, BoxError> {
    resolve_optional_document(ctx, as_workspace_host(host), uri)
}

fn as_workspace_host(host: &dyn EditHost) -> &dyn WorkspaceEditHost {
    host
}

fn add_replacement(
    plan: &mut WorkspacePlan,
    uri: &str,
    snapshot: Option<&DocumentSnapshot>,
    content: &str,
) -> Result<(), BoxError> {
    add_document_replacement(plan, uri, snapshot, content)
}

fn to_value<T: Serialize>(value: &T) -> Result<Value, BoxError> {
    Ok(serde_json::to_value(value)?)
}

/// Decodes command arguments; absent or `null` arguments decode as the
/// parameters' defaults.
fn decode_args<T: serde::de::DeserializeOwned + Default>(
    args: Option<&Value>,
) -> Result<T, serde_json::Error> {
    match args {
        None | Some(Value::Null) => Ok(T::default()),
        Some(value) => serde_json::from_value(value.clone()),
    }
}
