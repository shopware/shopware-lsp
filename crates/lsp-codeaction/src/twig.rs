//! Go `twig_codeaction.go`: block override, version-comment and block-diff
//! actions in Storefront Twig templates.

use std::sync::Arc;

use serde_json::Value;
use swlsp_lsp::{ActionProvider, CodeActionRequest, Context};
use swlsp_lsp_protocol::{self as protocol, CodeActionKind};
use swlsp_parser_twig::query as twigquery;
use swlsp_parser_twig::syntax::TWIG_COMMENT;
use swlsp_syntax::{LineIndex, SyntaxNode, SyntaxNodeExt, SyntaxToken, TextSize, Tree};
use swlsp_twig::{
    TwigFile, VERSION_COMMENT_PREFIX, VersioningService, is_storefront_template,
    is_upstream_template, parse_twig, parse_twig_tree,
};

use crate::shared::is_administration_twig_path;
use crate::snippet::command_action;

pub struct TwigCodeActionProvider {
    versioning: Option<Arc<VersioningService>>,
}

impl TwigCodeActionProvider {
    pub fn new(versioning: Option<Arc<VersioningService>>) -> Self {
        Self { versioning }
    }
}

/// The request's syntax, reparsed as Twig when the request has no node.
struct TwigRequest<'a> {
    request: &'a CodeActionRequest,
    uri: &'a str,
    source: &'a str,
    tree: Option<Tree>,
    line_index: Option<Arc<LineIndex>>,
}

impl TwigRequest<'_> {
    fn line_index(&self) -> LineIndexRef<'_> {
        if let Some(index) = self.line_index.as_deref() {
            return LineIndexRef::Borrowed(index);
        }
        if let Some(index) = self.request.line_index() {
            return LineIndexRef::Borrowed(index);
        }
        LineIndexRef::Owned(LineIndex::new(self.source.to_owned()))
    }

    fn tree(&self) -> Option<&Tree> {
        self.tree.as_ref().or_else(|| self.request.tree())
    }
}

enum LineIndexRef<'a> {
    Borrowed(&'a LineIndex),
    Owned(LineIndex),
}

impl std::ops::Deref for LineIndexRef<'_> {
    type Target = LineIndex;

    fn deref(&self) -> &LineIndex {
        match self {
            LineIndexRef::Borrowed(index) => index,
            LineIndexRef::Owned(index) => index,
        }
    }
}

impl ActionProvider for TwigCodeActionProvider {
    fn code_action_kinds(&self) -> Vec<CodeActionKind> {
        vec![CodeActionKind::REFACTOR_EXTRACT, CodeActionKind::QUICKFIX]
    }

    fn get_code_actions(
        &self,
        _ctx: &Context,
        request: &CodeActionRequest,
    ) -> Vec<protocol::CodeAction> {
        let uri = request.params.text_document.uri.as_str();
        if let Ok(path) = swlsp_uriutil::path(uri)
            && is_administration_twig_path(&path)
        {
            return Vec::new();
        }
        let source = request.source();
        let mut context = TwigRequest {
            request,
            uri,
            source,
            tree: None,
            line_index: None,
        };
        let (token, node): (Option<SyntaxToken>, Option<SyntaxNode>) =
            if request.node.is_none() && !source.is_empty() {
                let parse = swlsp_parser_twig::parse(source);
                let line_index = Arc::new(LineIndex::new(source.to_owned()));
                let offset = TextSize::from(line_index.offset_utf16(
                    request.params.range.start.line,
                    request.params.range.start.character,
                ));
                let root = parse.root();
                let token = root.token_at(offset);
                let node = root.node_at(offset);
                context.tree = Some(parse.tree);
                context.line_index = Some(line_index);
                (token, node)
            } else {
                (request.token.clone(), request.node.clone())
            };
        let Some(node) = node else {
            return Vec::new();
        };

        let mut actions = Vec::new();
        let block_node = twigquery::block_at(&node);
        let block_name = block_node
            .as_ref()
            .and_then(twigquery::block_name)
            .unwrap_or_default();
        if block_node.is_some()
            && token
                .as_ref()
                .is_some_and(|token| token.text() == block_name)
        {
            if uri.contains("Resources/views/storefront") {
                actions.push(command_action(
                    "Overwrite this block in Extension",
                    CodeActionKind::REFACTOR_EXTRACT,
                    "Overwrite Block",
                    "shopware.twig.extendBlock",
                    vec![Value::from(uri), Value::from(block_name.clone())],
                ));
            }
            self.push_block_actions(&context, &block_name, &mut actions);
        }

        if let Some(comment) = twigquery::closest_node_of_kind(&node, &[TWIG_COMMENT])
            && comment.text().to_string().contains(VERSION_COMMENT_PREFIX)
        {
            let name = versioned_block_at_comment(&context, &comment);
            if !name.is_empty() {
                self.push_block_actions(&context, &name, &mut actions);
            }
        }
        actions
    }
}

impl TwigCodeActionProvider {
    fn push_block_actions(
        &self,
        context: &TwigRequest<'_>,
        block_name: &str,
        actions: &mut Vec<protocol::CodeAction>,
    ) {
        if !has_diagnostic_code(
            context.request,
            &[
                "twig.versioning.comment_missing",
                "twig.versioning.outdated",
            ],
        ) && let Some(action) = self.version_comment_action(context, block_name)
        {
            actions.push(action);
        }
        if !has_diagnostic_code(context.request, &["twig.versioning.outdated"])
            && let Some(action) = self.show_diff_action(context, block_name)
        {
            actions.push(action);
        }
    }

    fn version_comment_action(
        &self,
        context: &TwigRequest<'_>,
        block_name: &str,
    ) -> Option<protocol::CodeAction> {
        let versioning = self.versioning.as_ref()?;
        if block_name.is_empty() {
            return None;
        }
        let path = swlsp_uriutil::path(context.uri).ok()?;
        if is_upstream_template(&path) {
            return None;
        }
        let (_, block, resolution) = versioning
            .resolve_document(&path, context.source, block_name)
            .ok()?;
        if resolution.candidates.is_empty() {
            return None;
        }
        let update = block.version_comment.is_some();
        if let Some(comment) = &block.version_comment
            && resolution
                .candidates
                .iter()
                .any(|candidate| candidate.hash == comment.hash)
        {
            return None;
        }
        let (range, replacement) = versioning
            .version_comment_edit(&path, context.source, block_name)
            .ok()?;
        let title = if update {
            "Shopware: Update Twig block version comment"
        } else {
            "Shopware: Add Twig block version comment"
        };
        let line_index = context.line_index();
        let (start_line, start_character) = line_index.position_utf16(range.start().into());
        let (end_line, end_character) = line_index.position_utf16(range.end().into());
        let edit = protocol::TextEdit::new(
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
            replacement,
        );
        Some(protocol::CodeAction {
            title: title.to_owned(),
            kind: Some(CodeActionKind::QUICKFIX),
            edit: Some(protocol::WorkspaceEdit {
                changes: [(context.uri.to_owned(), vec![edit])].into_iter().collect(),
                ..protocol::WorkspaceEdit::default()
            }),
            ..protocol::CodeAction::default()
        })
    }

    fn show_diff_action(
        &self,
        context: &TwigRequest<'_>,
        block_name: &str,
    ) -> Option<protocol::CodeAction> {
        let versioning = self.versioning.as_ref()?;
        if block_name.is_empty() {
            return None;
        }
        let path = swlsp_uriutil::path(context.uri).ok()?;
        if is_upstream_template(&path) {
            return None;
        }
        let (_, block, resolution) = versioning
            .resolve_document(&path, context.source, block_name)
            .ok()?;
        let comment = block.version_comment.as_ref()?;
        if comment.version.is_empty()
            || resolution.candidates.is_empty()
            || !is_storefront_template(&resolution.candidates[0].absolute_path)
        {
            return None;
        }
        if resolution
            .candidates
            .iter()
            .any(|candidate| candidate.hash == comment.hash)
        {
            return None;
        }
        Some(command_action(
            "Shopware: Show Twig block difference",
            CodeActionKind::QUICKFIX,
            "Show Twig Block Difference",
            "shopware.twig.showBlockDiff",
            vec![Value::from(context.uri), Value::from(block_name)],
        ))
    }
}

fn versioned_block_at_comment(context: &TwigRequest<'_>, comment: &SyntaxNode) -> String {
    let Some(file) = parse_twig_document(context) else {
        return String::new();
    };
    let start = comment.range_trimmed_trivia().start();
    file.blocks
        .values()
        .find(|block| {
            block
                .version_comment_range
                .is_some_and(|range| range.start() == start)
        })
        .map(|block| block.name.clone())
        .unwrap_or_default()
}

fn parse_twig_document(context: &TwigRequest<'_>) -> Option<TwigFile> {
    let path = swlsp_uriutil::path(context.uri).ok()?;
    match context.tree() {
        Some(tree) => Some(parse_twig_tree(&path, Some(tree), &context.line_index())),
        None => Some(parse_twig(&path, context.source)),
    }
}

/// Go `hasDiagnosticCode` (codes compared through `fmt.Sprint`).
fn has_diagnostic_code(request: &CodeActionRequest, codes: &[&str]) -> bool {
    request.params.context.diagnostics.iter().any(|diagnostic| {
        let current = match &diagnostic.code {
            None | Some(Value::Null) => "<nil>".to_owned(),
            Some(Value::String(value)) => value.clone(),
            Some(other) => other.to_string(),
        };
        codes.contains(&current.as_str())
    })
}

#[cfg(test)]
#[path = "tests/twig.rs"]
mod tests;
