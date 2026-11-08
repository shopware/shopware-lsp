//! Go `snippet_codeaction.go` and `snippet_copy_codeaction.go`.

use serde_json::Value;
use swlsp_language::LanguageId;
use swlsp_lsp::{ActionProvider, CodeActionRequest, Context};
use swlsp_lsp_protocol::{self as protocol, CodeActionKind};
use swlsp_parser_json::syntax::{JSON_OBJECT, JSON_PAIR, JSON_STRING};
use swlsp_php_types::go_to_lower;
use swlsp_syntax::{LineIndex, SyntaxNode};

pub const COPY_SNIPPET_USAGE_ACTION: &str = "shopware.copySnippetUsage";

/// Snippet insertion and creation commands in Twig templates.
pub struct SnippetCodeActionProvider {
    _snippets: Option<std::sync::Arc<swlsp_snippet::SnippetIndexer>>,
}

impl SnippetCodeActionProvider {
    pub fn new(snippets: Option<std::sync::Arc<swlsp_snippet::SnippetIndexer>>) -> Self {
        Self {
            _snippets: snippets,
        }
    }
}

pub(crate) fn command_action(
    title: &str,
    kind: CodeActionKind,
    command_title: &str,
    command: &str,
    arguments: Vec<Value>,
) -> protocol::CodeAction {
    protocol::CodeAction {
        title: title.to_owned(),
        kind: Some(kind),
        command: Some(protocol::CommandAction {
            title: command_title.to_owned(),
            command: command.to_owned(),
            arguments,
        }),
        ..protocol::CodeAction::default()
    }
}

impl ActionProvider for SnippetCodeActionProvider {
    fn code_action_kinds(&self) -> Vec<CodeActionKind> {
        vec![CodeActionKind::QUICKFIX]
    }

    fn get_code_actions(
        &self,
        _ctx: &Context,
        request: &CodeActionRequest,
    ) -> Vec<protocol::CodeAction> {
        let uri = &request.params.text_document.uri;
        if !go_to_lower(swlsp_uriutil::filepath::ext(uri)).ends_with(".twig") {
            return Vec::new();
        }
        let is_admin_file = uri.contains("/Resources/app/administration/");
        let range = request.params.range;
        if range.start == range.end {
            return vec![command_action(
                "Insert Snippet",
                CodeActionKind::QUICKFIX,
                "Insert Snippet",
                "shopware.insertSnippet",
                Vec::new(),
            )];
        }
        let selected = text_for_range(request.source(), range, request.line_index());
        if selected.is_empty() {
            return Vec::new();
        }
        let arguments = vec![Value::from(uri.clone()), Value::from(selected)];
        if is_admin_file {
            vec![command_action(
                "Create admin snippet from selection",
                CodeActionKind::QUICKFIX,
                "Create Admin Snippet from Selection",
                "shopware.createAdminSnippetFromSelection",
                arguments,
            )]
        } else {
            vec![command_action(
                "Create snippet from selection",
                CodeActionKind::QUICKFIX,
                "Create Snippet from Selection",
                "shopware.createSnippetFromSelection",
                arguments,
            )]
        }
    }
}

/// Go `textForRange`.
fn text_for_range(content: &str, range: protocol::Range, line_index: Option<&LineIndex>) -> String {
    let owned;
    let line_index = match line_index {
        Some(index) => index,
        None => {
            owned = LineIndex::new(content.to_owned());
            &owned
        }
    };
    let start = line_index.offset_utf16(range.start.line, range.start.character) as usize;
    let end = line_index.offset_utf16(range.end.line, range.end.character) as usize;
    if start > end || end > content.len() {
        return String::new();
    }
    content
        .get(start..end)
        .map(str::to_owned)
        .unwrap_or_else(|| String::from_utf8_lossy(&content.as_bytes()[start..end]).into_owned())
}

/// Copies the dotted key of the snippet under the cursor.
#[derive(Default)]
pub struct SnippetCopyCodeActionProvider;

impl SnippetCopyCodeActionProvider {
    pub fn new() -> Self {
        Self
    }
}

impl ActionProvider for SnippetCopyCodeActionProvider {
    fn code_action_kinds(&self) -> Vec<CodeActionKind> {
        vec![CodeActionKind::REFACTOR_EXTRACT]
    }

    fn get_code_actions(
        &self,
        ctx: &Context,
        request: &CodeActionRequest,
    ) -> Vec<protocol::CodeAction> {
        if ctx.is_cancelled() {
            return Vec::new();
        }
        let (Some(document), Some(node)) = (request.document.as_ref(), request.node.as_ref())
        else {
            return Vec::new();
        };
        if document.language != Some(LanguageId::Json) || !is_shopware_snippet_json(&document.uri) {
            return Vec::new();
        }
        let key = json_snippet_key(node);
        if key.is_empty() {
            return Vec::new();
        }
        vec![command_action(
            &format!("Shopware: Copy snippet usage for '{key}'"),
            CodeActionKind::REFACTOR_EXTRACT,
            "Copy Shopware snippet usage",
            COPY_SNIPPET_USAGE_ACTION,
            vec![Value::from(key)],
        )]
    }
}

fn is_shopware_snippet_json(uri: &str) -> bool {
    let path = go_to_lower(&swlsp_uriutil::filepath::to_slash(uri));
    path.contains("/resources/snippet/")
        || path.contains("/resources/app/administration/") && path.contains("/snippet/")
}

fn json_pair_key(pair: &SyntaxNode) -> String {
    let Some(key) = pair.children().find(|child| child.kind() == JSON_STRING) else {
        return String::new();
    };
    let text = key.text().to_string();
    let text = text.trim();
    let text = text.strip_prefix('"').unwrap_or(text);
    text.strip_suffix('"').unwrap_or(text).to_owned()
}

fn json_snippet_key(node: &SyntaxNode) -> String {
    let Some(pair) = node.ancestors().find(|current| current.kind() == JSON_PAIR) else {
        return String::new();
    };
    let mut parts: Vec<String> = Vec::new();
    let mut current = pair;
    loop {
        let key = json_pair_key(&current);
        if !key.is_empty() {
            parts.insert(0, key);
        }
        let Some(parent_object) = current
            .parent()
            .filter(|parent| parent.kind() == JSON_OBJECT)
        else {
            break;
        };
        let Some(parent_pair) = parent_object
            .parent()
            .filter(|parent| parent.kind() == JSON_PAIR)
        else {
            break;
        };
        current = parent_pair;
    }
    parts.join(".")
}

#[cfg(test)]
#[path = "tests/snippet.rs"]
mod tests;
