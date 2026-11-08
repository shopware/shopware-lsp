//! Shared test helpers (Go `codeaction_test_helpers_test.go`).

use std::collections::HashMap;
use std::sync::{Arc, Mutex};

use swlsp_lsp::{
    BoxError, CodeActionRequest, Context, DocumentError, DocumentResolver, DocumentSnapshot,
    SyntaxContext, TextDocument, WorkspaceEditHost,
};
use swlsp_lsp_protocol as protocol;
use swlsp_rewrite::WorkspacePlan;
use swlsp_syntax::LineIndex;

pub(crate) fn path_string(path: &std::path::Path) -> String {
    path.to_str().expect("utf-8 path").to_owned()
}

/// A code-action request whose selection spans the byte offsets
/// `start..end`; the syntax context is taken at `start` (like the server).
pub(crate) fn request_at(uri: &str, source: &str, start: u32, end: u32) -> CodeActionRequest {
    request_for_document(Arc::new(TextDocument::new(uri, source, 1)), start, end)
}

pub(crate) fn request_for_document(
    document: Arc<TextDocument>,
    start: u32,
    end: u32,
) -> CodeActionRequest {
    let (start_line, start_character) = document.line_index().position_utf16(start);
    let (end_line, end_character) = document.line_index().position_utf16(end);
    request_for_range(
        document,
        protocol::Range {
            start: protocol::Position {
                line: start_line,
                character: start_character,
            },
            end: protocol::Position {
                line: end_line,
                character: end_character,
            },
        },
    )
}

pub(crate) fn request_for_range(
    document: Arc<TextDocument>,
    range: protocol::Range,
) -> CodeActionRequest {
    let uri = document.uri.clone();
    let syntax = SyntaxContext::new(document, range.start.line, range.start.character);
    CodeActionRequest {
        params: protocol::CodeActionParams {
            text_document: protocol::TextDocumentIdentifier { uri },
            range,
            context: protocol::CodeActionContext::default(),
        },
        syntax,
    }
}

/// The edits of `edit` that target `uri`, from `changes` or
/// `documentChanges`.
pub(crate) fn edits_for_uri(edit: &protocol::WorkspaceEdit, uri: &str) -> Vec<protocol::TextEdit> {
    let mut result: Vec<protocol::TextEdit> = edit.changes.get(uri).cloned().unwrap_or_default();
    for change in &edit.document_changes {
        if change
            .text_document
            .as_ref()
            .is_some_and(|document| document.uri == uri)
        {
            result.extend(change.edits.iter().cloned());
        }
    }
    result
}

/// Go `applyCodeActionEdit`: applies the action's edits for `uri` back to
/// front.
pub(crate) fn apply_code_action_edit(
    source: &str,
    action: &protocol::CodeAction,
    uri: &str,
) -> String {
    let edit = action.edit.as_ref().expect("action edit");
    let edits = edits_for_uri(edit, uri);
    assert!(!edits.is_empty(), "no edits for {uri}");
    apply_text_edits(source, &edits)
}

pub(crate) fn apply_text_edits(source: &str, edits: &[protocol::TextEdit]) -> String {
    let line_index = LineIndex::new(source.to_owned());
    let mut offsets: Vec<(u32, u32, &str)> = edits
        .iter()
        .map(|edit| {
            (
                line_index.offset_utf16(edit.range.start.line, edit.range.start.character),
                line_index.offset_utf16(edit.range.end.line, edit.range.end.character),
                edit.new_text.as_str(),
            )
        })
        .collect();
    offsets.sort_by(|left, right| {
        if left.0 == right.0 {
            right.1.cmp(&left.1)
        } else {
            right.0.cmp(&left.0)
        }
    });
    let mut updated = source.to_owned();
    for (start, end, text) in offsets {
        assert!(start <= end);
        assert!(end as usize <= updated.len());
        updated.replace_range(start as usize..end as usize, text);
    }
    updated
}

/// Go `applyGeneratedWorkspaceEdit`: writes the edit to disk.
pub(crate) fn apply_generated_workspace_edit(edit: &protocol::WorkspaceEdit) {
    for change in &edit.document_changes {
        if change.kind == "create" {
            let path = swlsp_uriutil::path(&change.uri).unwrap();
            std::fs::create_dir_all(std::path::Path::new(&path).parent().unwrap()).unwrap();
            std::fs::write(&path, "").unwrap();
            continue;
        }
        let document = change.text_document.as_ref().expect("text document");
        let path = swlsp_uriutil::path(&document.uri).unwrap();
        let source = std::fs::read_to_string(&path).unwrap();
        let updated = apply_text_edits(&source, &change.edits);
        std::fs::write(&path, updated).unwrap();
    }
}

/// Go `generationTestHost`: resolves registered snapshots, then disk.
#[derive(Default)]
pub(crate) struct GenerationTestHost {
    pub snapshots: HashMap<String, DocumentSnapshot>,
    pub plan: Mutex<Option<WorkspacePlan>>,
}

impl DocumentResolver for GenerationTestHost {
    fn resolve_document(&self, _ctx: &Context, uri: &str) -> Result<DocumentSnapshot, BoxError> {
        if let Some(snapshot) = self.snapshots.get(uri) {
            return Ok(snapshot.clone());
        }
        let path = swlsp_uriutil::path(uri)?;
        let source = std::fs::read_to_string(&path).map_err(|source| {
            Box::new(DocumentError::Read {
                uri: uri.to_owned(),
                source,
            }) as BoxError
        })?;
        Ok(DocumentSnapshot {
            document: Arc::new(TextDocument::new(uri, &source, 0)),
            version: None,
        })
    }
}

impl WorkspaceEditHost for GenerationTestHost {
    fn workspace_edit(
        &self,
        _ctx: &Context,
        plan: &WorkspacePlan,
    ) -> Result<protocol::WorkspaceEdit, BoxError> {
        *self.plan.lock().unwrap() = Some(plan.clone());
        Ok(plan.workspace_edit()?)
    }
}

pub(crate) fn snapshot(uri: &str, source: &str, version: i32) -> DocumentSnapshot {
    DocumentSnapshot {
        document: Arc::new(TextDocument::new(uri, source, version)),
        version: Some(version),
    }
}
