//! Applying LSP workspace edits from the CLI and MCP (Go `edit.go`): a diff
//! preview by default, files written only in write mode.

use std::collections::HashMap;
use std::io::Write;

use swlsp_lsp_protocol::{
    CREATE_FILE_OPERATION, DELETE_FILE_OPERATION, DocumentChange, TextEdit, WorkspaceEdit,
};

use crate::CliError;
use crate::difflib::{split_lines, unified_diff};
use crate::gofmt::path_error;

#[derive(Debug, Clone, Copy, Default, PartialEq, Eq)]
pub struct EditMode {
    pub write: bool,
    pub diff: bool,
}

#[derive(Debug, Clone)]
struct PendingDocumentEdit {
    path: String,
    before: String,
    after: String,
    mode: u32,
    exists: bool,
    created: bool,
    modified: bool,
    deleted: bool,
}

/// Applies (or previews) `edit` (Go `applyWorkspaceEdit`).
pub fn apply_workspace_edit(
    writer: &mut dyn Write,
    edit: Option<&WorkspaceEdit>,
    mode: EditMode,
) -> Result<(), CliError> {
    let Some(edit) = edit else {
        return Err(CliError::fail("command returned no workspace edit"));
    };
    let mut applier = Applier {
        mode,
        documents: HashMap::new(),
        order: Vec::new(),
    };
    for (uri, edits) in &edit.changes {
        let document = applier.load(uri, false)?;
        let updated = apply_protocol_text_edits(&document.after, edits).map_err(|error| {
            CliError::fail(format!("apply edits to {}: {error}", document.path))
        })?;
        document.modified = updated != document.before;
        document.after = updated;
    }
    for change in &edit.document_changes {
        applier.apply_document_change(change)?;
    }
    applier.finish(writer)
}

struct Applier {
    mode: EditMode,
    documents: HashMap<String, PendingDocumentEdit>,
    order: Vec<String>,
}

impl Applier {
    fn load(&mut self, uri: &str, create: bool) -> Result<&mut PendingDocumentEdit, CliError> {
        if !self.documents.contains_key(uri) {
            let path = swlsp_uriutil::path(uri).map_err(|error| {
                CliError::fail(format!(
                    "resolve edit URI {}: {error}",
                    crate::gofmt::go_quote(uri)
                ))
            })?;
            let mut document = PendingDocumentEdit {
                path: path.clone(),
                before: String::new(),
                after: String::new(),
                mode: 0o644,
                exists: false,
                created: create,
                modified: false,
                deleted: false,
            };
            match std::fs::read(&path) {
                Ok(content) => {
                    document.exists = true;
                    document.before = String::from_utf8_lossy(&content).into_owned();
                    document.after = document.before.clone();
                    #[cfg(unix)]
                    if let Ok(metadata) = std::fs::metadata(&path) {
                        use std::os::unix::fs::PermissionsExt;
                        document.mode = metadata.permissions().mode() & 0o777;
                    }
                }
                Err(error) if !create && error.kind() != std::io::ErrorKind::NotFound => {
                    return Err(CliError::fail(format!(
                        "read edit target {path}: {}",
                        path_error("open", &path, &error)
                    )));
                }
                Err(_) if !create => {
                    return Err(CliError::fail(format!(
                        "edit target does not exist: {path}"
                    )));
                }
                Err(_) => {}
            }
            self.documents.insert(uri.to_owned(), document);
            self.order.push(uri.to_owned());
        }
        Ok(self.documents.get_mut(uri).expect("document was inserted"))
    }

    fn apply_document_change(&mut self, change: &DocumentChange) -> Result<(), CliError> {
        if change.kind == CREATE_FILE_OPERATION {
            return self.apply_create(change);
        }
        if change.kind == DELETE_FILE_OPERATION {
            return self.apply_delete(change);
        }
        let Some(text_document) = &change.text_document else {
            return Err(CliError::fail(format!(
                "unsupported workspace edit change kind {}",
                crate::gofmt::go_quote(&change.kind)
            )));
        };
        let document = self.load(&text_document.uri, false)?;
        let updated =
            apply_protocol_text_edits(&document.after, &change.edits).map_err(|error| {
                CliError::fail(format!("apply edits to {}: {error}", document.path))
            })?;
        document.modified = updated != document.before;
        document.after = updated;
        Ok(())
    }

    fn apply_delete(&mut self, change: &DocumentChange) -> Result<(), CliError> {
        let options = change.options.unwrap_or_default();
        if change.options.is_some() && options.recursive {
            return Err(CliError::fail(
                "recursive workspace deletion is not supported",
            ));
        }
        if change.options.is_some() && options.ignore_if_not_exists {
            let path = swlsp_uriutil::path(&change.uri).map_err(|error| {
                CliError::fail(format!(
                    "resolve delete URI {}: {error}",
                    crate::gofmt::go_quote(&change.uri)
                ))
            })?;
            if matches!(std::fs::metadata(&path), Err(error) if error.kind() == std::io::ErrorKind::NotFound)
            {
                return Ok(());
            }
        }
        let document = self.load(&change.uri, false)?;
        document.after = String::new();
        document.modified = !document.before.is_empty();
        document.deleted = true;
        Ok(())
    }

    fn apply_create(&mut self, change: &DocumentChange) -> Result<(), CliError> {
        let options = change.options;
        let document = self.load(&change.uri, true)?;
        let overwrite = options.is_some_and(|options| options.overwrite);
        if document.exists && !overwrite {
            if options.is_some_and(|options| options.ignore_if_exists) {
                return Ok(());
            }
            return Err(CliError::fail(format!(
                "create target already exists: {}",
                document.path
            )));
        }
        if document.exists && overwrite {
            document.after = String::new();
            document.modified = !document.before.is_empty();
        }
        Ok(())
    }

    fn finish(mut self, writer: &mut dyn Write) -> Result<(), CliError> {
        let documents = &self.documents;
        self.order
            .sort_by(|left, right| documents[left].path.cmp(&documents[right].path));
        for uri in &self.order {
            let document = &self.documents[uri];
            if !document.deleted
                && !document.modified
                && (!document.created || !document.before.is_empty())
            {
                continue;
            }
            finish_document(writer, self.mode, document)?;
        }
        Ok(())
    }
}

fn finish_document(
    writer: &mut dyn Write,
    mode: EditMode,
    document: &PendingDocumentEdit,
) -> Result<(), CliError> {
    if mode.diff || !mode.write {
        let to_file = if document.deleted {
            "/dev/null"
        } else {
            document.path.as_str()
        };
        let diff = unified_diff(
            &split_lines(&document.before),
            &split_lines(&document.after),
            &document.path,
            to_file,
            3,
        );
        writer.write_all(diff.as_bytes()).map_err(|error| {
            CliError::fail(format!("write diff for {}: {error}", document.path))
        })?;
    }
    if mode.write {
        if document.deleted {
            std::fs::remove_file(&document.path).map_err(|error| {
                CliError::fail(format!(
                    "delete {}: {}",
                    document.path,
                    path_error("remove", &document.path, &error)
                ))
            })?;
            return Ok(());
        }
        let directory = std::path::Path::new(&document.path)
            .parent()
            .map(|parent| parent.to_string_lossy().into_owned())
            .unwrap_or_else(|| ".".into());
        std::fs::create_dir_all(&directory).map_err(|error| {
            CliError::fail(format!(
                "create edit directory: {}",
                path_error("mkdir", &directory, &error)
            ))
        })?;
        write_file(&document.path, document.after.as_bytes(), document.mode).map_err(|error| {
            CliError::fail(format!(
                "write {}: {}",
                document.path,
                path_error("open", &document.path, &error)
            ))
        })?;
    }
    Ok(())
}

/// Go `os.WriteFile`: create with `mode` (before umask) or truncate.
fn write_file(path: &str, content: &[u8], mode: u32) -> std::io::Result<()> {
    let mut options = std::fs::OpenOptions::new();
    options.write(true).create(true).truncate(true);
    #[cfg(unix)]
    {
        use std::os::unix::fs::OpenOptionsExt;
        options.mode(mode);
    }
    #[cfg(not(unix))]
    let _ = mode;
    let mut file = options.open(path)?;
    file.write_all(content)
}

/// Applies UTF-16 positioned edits to `source` (Go `applyProtocolTextEdits`).
pub fn apply_protocol_text_edits(source: &str, edits: &[TextEdit]) -> Result<String, String> {
    let line_index = swlsp_syntax::LineIndex::new(source);
    let mut offsets: Vec<(u32, u32, &str)> = Vec::with_capacity(edits.len());
    for edit in edits {
        let start = line_index.offset_utf16(edit.range.start.line, edit.range.start.character);
        let end = line_index.offset_utf16(edit.range.end.line, edit.range.end.character);
        if start > end || end as usize > source.len() {
            return Err(format!(
                "invalid edit range {{{{{} {}}} {{{} {}}}}}",
                edit.range.start.line,
                edit.range.start.character,
                edit.range.end.line,
                edit.range.end.character
            ));
        }
        offsets.push((start, end, edit.new_text.as_str()));
    }
    // Stable sort descending by start, then by end (longest first).
    offsets.sort_by(|left, right| {
        if left.0 == right.0 {
            right.1.cmp(&left.1)
        } else {
            right.0.cmp(&left.0)
        }
    });
    let mut last_start = source.len() as u32;
    let mut result = source.as_bytes().to_vec();
    for (start, end, text) in offsets {
        if end > last_start {
            return Err("overlapping text edits".into());
        }
        result.splice(start as usize..end as usize, text.bytes());
        last_start = start;
    }
    Ok(String::from_utf8_lossy(&result).into_owned())
}
