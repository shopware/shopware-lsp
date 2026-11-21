//! On the main language file of a snippet directory: adds the keys the
//! other locale files lack to all of them at once, with the text of the
//! main file to translate.

use std::collections::BTreeMap;
use std::sync::Arc;

use swlsp_language::LanguageId;
use swlsp_lsp::{ActionProvider, CodeActionRequest, Context, DocumentResolver, protocol_range};
use swlsp_lsp_protocol::{self as protocol, CodeActionKind};
use swlsp_uriutil::{file_uri, filepath};

use crate::diagnostics::{is_snippet_file, translated_snippet_files};
use crate::inspections::snippet_files::missing_snippet_edits;

pub struct SnippetSyncCodeActionProvider {
    /// Reads the other locale files as the editor has them.
    documents: Arc<dyn DocumentResolver>,
}

impl SnippetSyncCodeActionProvider {
    pub fn new(documents: Arc<dyn DocumentResolver>) -> Self {
        Self { documents }
    }
}

impl ActionProvider for SnippetSyncCodeActionProvider {
    fn code_action_kinds(&self) -> Vec<CodeActionKind> {
        vec![CodeActionKind::REFACTOR_REWRITE]
    }

    fn get_code_actions(
        &self,
        ctx: &Context,
        request: &CodeActionRequest,
    ) -> Vec<protocol::CodeAction> {
        let Some(document) = request.document.as_ref() else {
            return Vec::new();
        };
        if document.language != Some(LanguageId::Json)
            || !is_snippet_file(&filepath::to_slash(&document.uri))
        {
            return Vec::new();
        }
        let Ok(path) = swlsp_uriutil::path(&document.uri) else {
            return Vec::new();
        };
        let path = filepath::clean(&path);
        let mut changes = BTreeMap::new();
        for file in translated_snippet_files(&path) {
            if ctx.is_cancelled() {
                return Vec::new();
            }
            let uri = file_uri(&file);
            let Ok(snapshot) = self.documents.resolve_document(ctx, &uri) else {
                continue;
            };
            let other = &snapshot.document;
            let Some(mut edits) = missing_snippet_edits(document.source(), other.source()) else {
                continue;
            };
            edits.sort_by_key(|(range, _)| range.start());
            let edits: Vec<protocol::TextEdit> = edits
                .into_iter()
                .map(|(range, text)| {
                    protocol::TextEdit::new(protocol_range(other.line_index(), range), text)
                })
                .collect();
            changes.insert(uri, edits);
        }
        if changes.is_empty() {
            return Vec::new();
        }
        let title = match changes.keys().next() {
            Some(uri) if changes.len() == 1 => {
                format!("Shopware: Add missing snippets to {}", filepath::base(uri))
            }
            _ => format!(
                "Shopware: Add missing snippets to {} locale files",
                changes.len()
            ),
        };
        vec![protocol::CodeAction {
            title,
            kind: Some(CodeActionKind::REFACTOR_REWRITE),
            edit: Some(protocol::WorkspaceEdit {
                changes,
                ..protocol::WorkspaceEdit::default()
            }),
            ..protocol::CodeAction::default()
        }]
    }
}

#[cfg(test)]
#[path = "tests/snippet_sync.rs"]
mod tests;
