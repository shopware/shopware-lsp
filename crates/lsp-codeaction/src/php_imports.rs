//! Go `php_imports.go`.

use swlsp_lsp::{ActionProvider, CodeActionRequest, Context};
use swlsp_lsp_protocol as protocol;
use swlsp_lsp_protocol::CodeActionKind;
use swlsp_php::PhpIndex;
use swlsp_rewrite::{DocumentPlan, WorkspacePlan};

pub struct PhpImportsProvider {
    index: Option<PhpIndex>,
}

impl PhpImportsProvider {
    pub fn new(index: Option<PhpIndex>) -> Self {
        Self { index }
    }
}

impl ActionProvider for PhpImportsProvider {
    fn code_action_kinds(&self) -> Vec<CodeActionKind> {
        vec![CodeActionKind::SOURCE_ORGANIZE_IMPORTS]
    }

    fn get_code_actions(
        &self,
        ctx: &Context,
        request: &CodeActionRequest,
    ) -> Vec<protocol::CodeAction> {
        let (Some(index), Some(document)) = (self.index.as_ref(), request.document.as_ref()) else {
            return Vec::new();
        };
        let Ok(analysis) = swlsp_lsp_phpanalysis::imports(ctx, index, document) else {
            return Vec::new();
        };
        let Ok(edits) =
            swlsp_php_imports::organize(&|| ctx.is_cancelled(), document.source(), &analysis)
        else {
            return Vec::new();
        };
        if edits.is_empty() {
            return Vec::new();
        }
        let plan = WorkspacePlan {
            documents: vec![
                DocumentPlan::new(
                    document.uri.clone(),
                    Some(document.version),
                    document.source_arc().clone(),
                    edits,
                )
                .with_line_index(document.line_index_arc().clone()),
            ],
            ..WorkspacePlan::default()
        };
        let Ok(edit) = plan.workspace_edit() else {
            return Vec::new();
        };
        vec![protocol::CodeAction {
            title: "Organize Imports".to_owned(),
            kind: Some(CodeActionKind::SOURCE_ORGANIZE_IMPORTS),
            edit: Some(edit),
            ..protocol::CodeAction::default()
        }]
    }
}
