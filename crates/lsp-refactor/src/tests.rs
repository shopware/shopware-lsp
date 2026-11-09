mod admin;
mod php_twig;
mod twig_template;

use std::path::Path;
use std::sync::Arc;

use swlsp_lsp::{RenameRequest, SyntaxContext, TextDocument};
use swlsp_lsp_protocol as protocol;
use swlsp_syntax::{LineIndex, SyntaxNodeExt, TextSize};
use swlsp_uriutil::file_uri;

pub(crate) fn path_string(path: &Path) -> String {
    path.to_str().expect("utf-8 path").to_owned()
}

/// A rename request for `path` with the cursor at `offset`.
pub(crate) fn rename_request(
    path: &str,
    source: &str,
    offset: u32,
    new_name: &str,
) -> RenameRequest {
    let document = Arc::new(TextDocument::new(file_uri(path), source, 1));
    let (line, character) = document.line_index().position_utf16(offset);
    let root = document.root().expect("root");
    let node = root.node_at(TextSize::from(offset));
    let token = root.token_at(TextSize::from(offset));
    RenameRequest {
        params: protocol::RenameParams {
            text_document: protocol::TextDocumentIdentifier {
                uri: document.uri.clone(),
            },
            position: protocol::Position { line, character },
            new_name: new_name.to_owned(),
        },
        syntax: SyntaxContext {
            language: document.language,
            document: Some(document),
            root: Some(root),
            token,
            node,
        },
    }
}

/// Applies edits in their listed order .
pub(crate) fn apply_edits(source: &str, edits: Option<&Vec<protocol::TextEdit>>) -> String {
    let line_index = LineIndex::new(source.to_owned());
    let mut result = source.to_owned();
    for edit in edits.into_iter().flatten() {
        let start =
            line_index.offset_utf16(edit.range.start.line, edit.range.start.character) as usize;
        let end = line_index.offset_utf16(edit.range.end.line, edit.range.end.character) as usize;
        result = format!("{}{}{}", &result[..start], edit.new_text, &result[end..]);
    }
    result
}

pub(crate) fn edit_count(edit: &protocol::WorkspaceEdit) -> usize {
    edit.changes.values().map(Vec::len).sum()
}
