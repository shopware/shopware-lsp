//! Go `php_extract_variable.go`.

use swlsp_language::LanguageId;
use swlsp_lsp::{ActionProvider, CodeActionRequest, Context};
use swlsp_lsp_protocol as protocol;
use swlsp_lsp_protocol::CodeActionKind;
use swlsp_php::PhpIndex;
use swlsp_php_refactoring::{VariableOptions, extract_variable};
use swlsp_rewrite::{DocumentPlan, WorkspacePlan};
use swlsp_syntax::{SyntaxNode, TextRange, TextSize};

#[derive(Default)]
pub struct PhpExtractVariableProvider {
    index: Option<PhpIndex>,
}

impl PhpExtractVariableProvider {
    pub fn new(index: Option<PhpIndex>) -> Self {
        Self { index }
    }
}

impl ActionProvider for PhpExtractVariableProvider {
    fn code_action_kinds(&self) -> Vec<CodeActionKind> {
        vec![CodeActionKind::REFACTOR_EXTRACT]
    }

    fn get_code_actions(
        &self,
        ctx: &Context,
        request: &CodeActionRequest,
    ) -> Vec<protocol::CodeAction> {
        let Some(document) = request.document.as_ref() else {
            return Vec::new();
        };
        if document.language != Some(LanguageId::Php) || !document.parse_errors().is_empty() {
            return Vec::new();
        }
        let Some(root) = document.root() else {
            return Vec::new();
        };
        let range = request.params.range;
        let line_index = document.line_index();
        let selection = TextRange::new(
            TextSize::from(line_index.offset_utf16(range.start.line, range.start.character)),
            TextSize::from(line_index.offset_utf16(range.end.line, range.end.character)),
        );
        if selection.start() > selection.end() {
            return Vec::new();
        }
        let call_returns_value = |call: &SyntaxNode| -> bool {
            if ctx.is_cancelled() {
                return false;
            }
            let Some(index) = self.index.as_ref() else {
                return false;
            };
            matches!(
                swlsp_lsp_phpanalysis::for_document(index, document),
                Ok(Some(state)) if state.call_returns_value(call)
            )
        };
        let cancelled = || ctx.is_cancelled();
        let mut actions = Vec::new();
        for all in [false, true] {
            let options = VariableOptions {
                call_returns_value: Some(&call_returns_value),
                all_occurrences: all,
            };
            let Ok(Some(result)) =
                extract_variable(&root, document.source(), selection, options, &cancelled)
            else {
                continue;
            };
            let plan = WorkspacePlan {
                documents: vec![
                    DocumentPlan::new(
                        document.uri.clone(),
                        Some(document.version),
                        document.source_arc().clone(),
                        result.edits,
                    )
                    .with_line_index(document.line_index_arc().clone()),
                ],
                ..WorkspacePlan::default()
            };
            let Ok(edit) = plan.workspace_edit() else {
                continue;
            };
            let mut title = format!("Extract variable '{}'", result.name);
            if all {
                title.push_str(&format!(" (all {} occurrences)", result.occurrences));
            }
            actions.push(protocol::CodeAction {
                title,
                kind: Some(CodeActionKind::REFACTOR_EXTRACT),
                edit: Some(edit),
                ..protocol::CodeAction::default()
            });
        }
        actions
    }
}
