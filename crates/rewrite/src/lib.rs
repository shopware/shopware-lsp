//! Validated, lossless source edits compiled from immutable syntax trees.
//!
//! Port of Go `internal/rewrite`. Trees are never mutated: callers resolve an
//! element in one document snapshot and compile the requested change to byte
//! edits for that same snapshot. Ranges are byte offsets; conversion to LSP
//! UTF-16 positions happens only in [`WorkspacePlan::workspace_edit`], which
//! emits `swlsp-lsp-protocol` types directly.

mod handle;

use std::collections::HashSet;
use std::fmt;
use std::sync::Arc;

use swlsp_lsp_protocol as protocol;
use swlsp_syntax::{LineIndex, SyntaxElement, SyntaxNode, SyntaxToken, TextRange, TextSize};

pub use handle::{ElementHandle, HandleError};

/// Errors produced while building or compiling rewrites.
#[derive(Debug, Clone, PartialEq, Eq, thiserror::Error)]
pub enum RewriteError {
    /// Go `ErrInvalidRange`.
    #[error("rewrite range is outside the source: {range:?} for source length {source_len}")]
    InvalidRange { range: TextRange, source_len: u32 },
    /// Go `ErrOverlap`.
    #[error("rewrite edits overlap: {left:?} and {right:?}")]
    Overlap { left: TextRange, right: TextRange },
    #[error("rewrite {what} URI is empty")]
    EmptyUri { what: &'static str },
    #[error("rewrite contains duplicate document {0:?}")]
    DuplicateDocument(String),
    /// An error scoped to one document of a workspace plan.
    #[error("rewrite {uri}: {source}")]
    InDocument {
        uri: String,
        source: Box<RewriteError>,
    },
}

impl RewriteError {
    /// The innermost error, unwrapping document scopes (Go `errors.Is`).
    pub fn root(&self) -> &RewriteError {
        match self {
            RewriteError::InDocument { source, .. } => source.root(),
            other => other,
        }
    }

    pub fn is_invalid_range(&self) -> bool {
        matches!(self.root(), RewriteError::InvalidRange { .. })
    }

    pub fn is_overlap(&self) -> bool {
        matches!(self.root(), RewriteError::Overlap { .. })
    }
}

pub type Result<T, E = RewriteError> = std::result::Result<T, E>;

/// Anything that covers an exact byte range of a source snapshot.
pub trait SyntaxRange {
    fn syntax_range(&self) -> TextRange;
}

impl SyntaxRange for TextRange {
    fn syntax_range(&self) -> TextRange {
        *self
    }
}

impl SyntaxRange for SyntaxNode {
    fn syntax_range(&self) -> TextRange {
        self.text_range()
    }
}

impl SyntaxRange for SyntaxToken {
    fn syntax_range(&self) -> TextRange {
        self.text_range()
    }
}

impl SyntaxRange for SyntaxElement {
    fn syntax_range(&self) -> TextRange {
        self.text_range()
    }
}

/// Replaces `range` with `new_text`. The range uses byte offsets into the
/// source snapshot supplied to [`Builder`].
#[derive(Clone)]
pub struct Edit {
    pub range: TextRange,
    pub new_text: String,
    order: usize,
}

impl Edit {
    pub fn new(range: TextRange, new_text: impl Into<String>) -> Self {
        Self {
            range,
            new_text: new_text.into(),
            order: 0,
        }
    }
}

impl PartialEq for Edit {
    fn eq(&self, other: &Self) -> bool {
        self.range == other.range && self.new_text == other.new_text
    }
}

impl Eq for Edit {}

impl fmt::Debug for Edit {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        f.debug_struct("Edit")
            .field("range", &self.range)
            .field("new_text", &self.new_text)
            .finish()
    }
}

/// Collects edits for one immutable source snapshot.
#[derive(Debug, Clone)]
pub struct Builder {
    source_len: u32,
    edits: Vec<Edit>,
    next_order: usize,
}

impl Builder {
    pub fn new(source: &str) -> Self {
        Self {
            source_len: source.len() as u32,
            edits: Vec::new(),
            next_order: 0,
        }
    }

    pub fn replace(&mut self, element: &impl SyntaxRange, text: impl Into<String>) -> Result<()> {
        self.replace_range(element.syntax_range(), text)
    }

    pub fn delete(&mut self, element: &impl SyntaxRange) -> Result<()> {
        self.replace(element, String::new())
    }

    pub fn insert_before(
        &mut self,
        element: &impl SyntaxRange,
        text: impl Into<String>,
    ) -> Result<()> {
        self.insert(element.syntax_range().start().into(), text)
    }

    pub fn insert_after(
        &mut self,
        element: &impl SyntaxRange,
        text: impl Into<String>,
    ) -> Result<()> {
        self.insert(element.syntax_range().end().into(), text)
    }

    pub fn insert(&mut self, offset: u32, text: impl Into<String>) -> Result<()> {
        let offset = TextSize::from(offset);
        self.replace_range(TextRange::new(offset, offset), text)
    }

    pub fn replace_range(&mut self, range: TextRange, text: impl Into<String>) -> Result<()> {
        if u32::from(range.end()) > self.source_len {
            return Err(RewriteError::InvalidRange {
                range,
                source_len: self.source_len,
            });
        }
        self.edits.push(Edit {
            range,
            new_text: text.into(),
            order: self.next_order,
        });
        self.next_order += 1;
        Ok(())
    }

    /// Validates conflicts and returns edits in ascending source order.
    /// Insertions at the same offset are coalesced in declaration order.
    pub fn finish(&self) -> Result<Vec<Edit>> {
        let mut edits = self.edits.clone();
        edits.sort_by(|left, right| {
            left.range
                .start()
                .cmp(&right.range.start())
                .then(left.range.end().cmp(&right.range.end()))
                .then(left.order.cmp(&right.order))
        });
        let mut coalesced: Vec<Edit> = Vec::with_capacity(edits.len());
        for edit in edits {
            if let Some(previous) = coalesced.last_mut() {
                if previous.range.is_empty()
                    && edit.range.is_empty()
                    && previous.range.start() == edit.range.start()
                {
                    previous.new_text.push_str(&edit.new_text);
                    continue;
                }
                if edits_conflict(previous, &edit) {
                    return Err(RewriteError::Overlap {
                        left: previous.range,
                        right: edit.range,
                    });
                }
            }
            coalesced.push(edit);
        }
        Ok(coalesced)
    }
}

fn edits_conflict(left: &Edit, right: &Edit) -> bool {
    let (left, right) = (left.range, right.range);
    if left.is_empty() {
        return left.start() > right.start() && left.start() < right.end();
    }
    if right.is_empty() {
        return right.start() > left.start() && right.start() < left.end();
    }
    left.start() < right.end() && right.start() < left.end()
}

/// Applies validated edits without mutating the input snapshot.
pub fn apply(source: &str, edits: &[Edit]) -> Result<String> {
    let mut builder = Builder::new(source);
    for edit in edits {
        builder.replace_range(edit.range, edit.new_text.clone())?;
    }
    let validated = builder.finish()?;
    // Go slices bytes and would emit invalid UTF-8 for a range inside a
    // character; Rust strings cannot, so such a range is rejected.
    if let Some(edit) = validated.iter().find(|edit| {
        !source.is_char_boundary(usize::from(edit.range.start()))
            || !source.is_char_boundary(usize::from(edit.range.end()))
    }) {
        return Err(RewriteError::InvalidRange {
            range: edit.range,
            source_len: source.len() as u32,
        });
    }
    let mut result = String::with_capacity(
        source.len()
            + validated
                .iter()
                .map(|edit| edit.new_text.len())
                .sum::<usize>(),
    );
    let mut cursor = 0usize;
    for edit in &validated {
        let start = usize::from(edit.range.start());
        result.push_str(&source[cursor..start]);
        result.push_str(&edit.new_text);
        cursor = usize::from(edit.range.end());
    }
    result.push_str(&source[cursor..]);
    Ok(result)
}

/// The complete edit set for one source snapshot.
#[derive(Debug, Clone)]
pub struct DocumentPlan {
    pub uri: String,
    pub version: Option<i32>,
    pub source: Arc<str>,
    /// Optional precomputed line index for `source`; built on demand otherwise.
    pub line_index: Option<Arc<LineIndex>>,
    pub edits: Vec<Edit>,
}

impl DocumentPlan {
    pub fn new(
        uri: impl Into<String>,
        version: Option<i32>,
        source: impl Into<Arc<str>>,
        edits: Vec<Edit>,
    ) -> Self {
        Self {
            uri: uri.into(),
            version,
            source: source.into(),
            line_index: None,
            edits,
        }
    }

    /// Reuses an existing line index of the same source snapshot.
    pub fn with_line_index(mut self, line_index: Arc<LineIndex>) -> Self {
        self.line_index = Some(line_index);
        self
    }

    pub fn apply(&self) -> Result<String> {
        apply(&self.source, &self.edits)
    }
}

/// Creates a new workspace file before any document edits are applied.
/// Non-empty content is inserted as a versionless text-document edit
/// immediately after creation.
#[derive(Debug, Clone, Default, PartialEq, Eq)]
pub struct CreateFilePlan {
    pub uri: String,
    pub content: String,
}

/// Removes a file only after the server has verified that the current
/// document still matches the snapshot used to prepare the rewrite.
#[derive(Debug, Clone, Default, PartialEq, Eq)]
pub struct DeleteFilePlan {
    pub uri: String,
    pub version: Option<i32>,
    pub source: String,
}

/// Edits that the client must apply as one workspace edit.
#[derive(Debug, Clone, Default)]
pub struct WorkspacePlan {
    pub documents: Vec<DocumentPlan>,
    pub creates: Vec<CreateFilePlan>,
    pub deletes: Vec<DeleteFilePlan>,
}

impl WorkspacePlan {
    /// Compiles the plan to an LSP workspace edit: creates first, then
    /// document edits, then deletes. Every operation is emitted as an ordered
    /// `documentChanges` entry; `changes` stays empty.
    pub fn workspace_edit(&self) -> Result<protocol::WorkspaceEdit> {
        let mut result = protocol::WorkspaceEdit::default();
        let mut seen: HashSet<&str> =
            HashSet::with_capacity(self.creates.len() + self.documents.len() + self.deletes.len());
        for created in &self.creates {
            check_uri(&mut seen, &created.uri, "create-file")?;
            result.document_changes.push(resource_change(
                protocol::CREATE_FILE_OPERATION,
                &created.uri,
            ));
            if !created.content.is_empty() {
                result.document_changes.push(text_document_change(
                    &created.uri,
                    None,
                    vec![protocol::TextEdit::new(
                        protocol::Range::default(),
                        created.content.clone(),
                    )],
                ));
            }
        }
        for document in &self.documents {
            check_uri(&mut seen, &document.uri, "document")?;
            let in_document = |error: RewriteError| RewriteError::InDocument {
                uri: document.uri.clone(),
                source: Box::new(error),
            };
            let mut builder = Builder::new(&document.source);
            for edit in &document.edits {
                builder
                    .replace_range(edit.range, edit.new_text.clone())
                    .map_err(in_document)?;
            }
            let edits = builder.finish().map_err(in_document)?;
            let owned;
            let line_index = match &document.line_index {
                Some(index) => index.as_ref(),
                None => {
                    owned = LineIndex::new(document.source.clone());
                    &owned
                }
            };
            let protocol_edits = edits
                .into_iter()
                .map(|edit| {
                    protocol::TextEdit::new(protocol_range(line_index, edit.range), edit.new_text)
                })
                .collect();
            result.document_changes.push(text_document_change(
                &document.uri,
                document.version,
                protocol_edits,
            ));
        }
        for deleted in &self.deletes {
            check_uri(&mut seen, &deleted.uri, "delete-file")?;
            result.document_changes.push(resource_change(
                protocol::DELETE_FILE_OPERATION,
                &deleted.uri,
            ));
        }
        Ok(result)
    }
}

/// A `documentChanges` text-document edit. `version` serializes as `null`
/// when absent, as LSP requires for versionless edits.
fn text_document_change(
    uri: &str,
    version: Option<i32>,
    edits: Vec<protocol::TextEdit>,
) -> protocol::DocumentChange {
    protocol::DocumentChange {
        text_document: Some(protocol::OptionalVersionedTextDocumentIdentifier {
            uri: uri.to_owned(),
            version,
        }),
        edits,
        ..protocol::DocumentChange::default()
    }
}

/// A `documentChanges` resource operation with default options.
fn resource_change(kind: &str, uri: &str) -> protocol::DocumentChange {
    protocol::DocumentChange {
        kind: kind.to_owned(),
        uri: uri.to_owned(),
        options: Some(protocol::ResourceOperationOptions::default()),
        ..protocol::DocumentChange::default()
    }
}

fn check_uri<'a>(seen: &mut HashSet<&'a str>, uri: &'a str, what: &'static str) -> Result<()> {
    if uri.is_empty() {
        return Err(RewriteError::EmptyUri { what });
    }
    if !seen.insert(uri) {
        return Err(RewriteError::DuplicateDocument(uri.to_owned()));
    }
    Ok(())
}

/// Converts a byte range to an LSP UTF-16 range.
pub fn protocol_range(line_index: &LineIndex, range: TextRange) -> protocol::Range {
    let (start_line, start_character) = line_index.position_utf16(range.start().into());
    let (end_line, end_character) = line_index.position_utf16(range.end().into());
    protocol::Range {
        start: protocol::Position {
            line: start_line,
            character: start_character,
        },
        end: protocol::Position {
            line: end_line,
            character: end_character,
        },
    }
}

#[cfg(test)]
mod tests;
