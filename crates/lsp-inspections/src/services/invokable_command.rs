//! Go `invokable_command.go`: the `symfony.console.invoke` inspection.

use std::sync::Arc;

use serde_json::json;
use swlsp_language::LanguageId;
use swlsp_lsp::{
    BoxError, Context, FixContext, FixId, FixPresentation, FixResolution, Inspection,
    InspectionDefinition, QuickFix, RewriteQuickFix, bind_fix,
};
use swlsp_lsp_diagnostics::InvokableCommandAnalyzer;
use swlsp_lsp_protocol::{self as protocol, DiagnosticSeverity};
use swlsp_parser_php::query as phpquery;
use swlsp_parser_php::syntax::{
    PHP_INTERSECTION_TYPE, PHP_METHOD_DECLARATION, PHP_NULLABLE_TYPE, PHP_PARAMETER_LIST, PHP_TYPE,
    PHP_UNION_TYPE,
};
use swlsp_php::PhpIndex;
use swlsp_rewrite::{Builder, DocumentPlan, WorkspacePlan};
use swlsp_syntax::{SyntaxNode, SyntaxNodeExt, TextRange};

use super::{ancestor_node, definition};
use crate::BoundInspection;
use crate::inspection::resolve_anchor;

pub const INVOKABLE_RETURN_TYPE_FIX_ID: &str = "add-invokable-command-return-type";

/// Go `NewInvokableCommand`.
pub fn new_invokable_command(php_index: Option<PhpIndex>) -> Arc<dyn Inspection> {
    Arc::new(BoundInspection {
        definition: InspectionDefinition {
            id: "symfony.console.invoke".to_owned(),
            languages: vec![LanguageId::Php],
            problems: vec![
                definition(
                    "symfony.console.invoke.return_type",
                    DiagnosticSeverity::HINT,
                ),
                definition(
                    "symfony.console.invoke.return_value",
                    DiagnosticSeverity::WARNING,
                ),
            ],
        },
        analyzers: vec![Arc::new(InvokableCommandAnalyzer::new(php_index))],
        fixes: vec![Arc::new(InvokableReturnTypeFix)],
        bind: Some(Arc::new(|code, _| {
            if code.as_str() != "symfony.console.invoke.return_type" {
                return Vec::new();
            }
            vec![bind_fix(INVOKABLE_RETURN_TYPE_FIX_ID, json!({}))]
        })),
    })
}

struct InvokableReturnTypeFix;

impl QuickFix for InvokableReturnTypeFix {
    fn id(&self) -> FixId {
        FixId::from(INVOKABLE_RETURN_TYPE_FIX_ID)
    }

    fn present(
        &self,
        _ctx: &Context,
        _fix: &FixContext,
    ) -> Result<Option<FixPresentation>, BoxError> {
        Ok(Some(FixPresentation {
            title: "Symfony: Use int return type for command __invoke()".to_owned(),
            kind: Some(protocol::CodeActionKind::QUICKFIX),
            preferred: true,
            resolution: FixResolution::Eager,
        }))
    }

    fn as_rewrite(&self) -> Option<&dyn RewriteQuickFix> {
        Some(self)
    }
}

impl RewriteQuickFix for InvokableReturnTypeFix {
    fn build(&self, _ctx: &Context, fix: &FixContext) -> Result<WorkspacePlan, BoxError> {
        let element = resolve_anchor(fix)?;
        let Some(method) = ancestor_node(&element, PHP_METHOD_DECLARATION)
            .filter(|method| phpquery::method_name(method) == "__invoke")
        else {
            return Err("command __invoke() method is no longer available".into());
        };
        let Some((range, new_text)) = invokable_return_type_edit(&method) else {
            return Err("command return type cannot be edited".into());
        };
        let document = &fix.document;
        let mut builder = Builder::new(document.source());
        builder.replace_range(range, new_text)?;
        let edits = builder.finish()?;
        Ok(WorkspacePlan {
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
        })
    }
}

fn invokable_return_type_edit(method: &SyntaxNode) -> Option<(TextRange, &'static str)> {
    for child in method.children() {
        if matches!(
            child.kind(),
            PHP_TYPE | PHP_NULLABLE_TYPE | PHP_UNION_TYPE | PHP_INTERSECTION_TYPE
        ) {
            return Some((child.range_trimmed_trivia(), "int"));
        }
    }
    let parameters = phpquery::direct_child(method, PHP_PARAMETER_LIST)?;
    let offset = parameters.range_trimmed_trivia().end();
    Some((TextRange::new(offset, offset), ": int"))
}
