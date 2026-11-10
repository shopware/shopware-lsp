//! Missing snippet inspection.

use std::sync::Arc;

use crate::diagnostics::SnippetAnalyzer;
use serde::{Deserialize, Serialize};
use serde_json::Value;
use swlsp_language::LanguageId;
use swlsp_lsp::{
    BoxError, CommandQuickFix, Context, FixContext, FixId, FixPresentation, FixResolution,
    Inspection, InspectionDefinition, InspectionScope, ProblemDefinition, QuickFix, bind_fix,
};
use swlsp_lsp_protocol as protocol;
use swlsp_snippet::SnippetIndexer;
use swlsp_translation::Index as TranslationIndex;

use super::map_string;
use crate::inspections::BoundInspection;

const CREATE_SNIPPET_FIX_ID: &str = "create-snippet";

#[derive(Debug, Clone, Default, Serialize, Deserialize)]
struct CreateSnippetPayload {
    #[serde(default)]
    key: String,
    #[serde(default, skip_serializing_if = "std::ops::Not::not")]
    admin: bool,
}

pub fn new_snippet(
    snippet_index: Option<Arc<SnippetIndexer>>,
    translation_index: Option<Arc<TranslationIndex>>,
) -> Arc<dyn Inspection> {
    let problem = |id: &str| ProblemDefinition {
        id: id.into(),
        source: "shopware-lsp".into(),
        default_severity: protocol::DiagnosticSeverity::WARNING,
        disabled_by_default: false,
    };
    Arc::new(BoundInspection {
        definition: InspectionDefinition {
            id: "shopware.snippet".into(),
            description: "Storefront and Administration snippet keys no snippet file defines.",
            domain: "shopware.snippets",
            scope: InspectionScope::Framework,
            languages: vec![LanguageId::JavaScript, LanguageId::Twig],
            problems: vec![
                problem("admin.snippet.missing"),
                problem("frontend.snippet.missing"),
            ],
        },
        analyzers: vec![Arc::new(SnippetAnalyzer::new(
            snippet_index,
            translation_index,
        ))],
        fixes: vec![Arc::new(CreateSnippetFix)],
        bind: Some(Arc::new(|code, payload| {
            let key = map_string(payload, "snippetText");
            if key.is_empty() {
                return Vec::new();
            }
            vec![bind_fix(
                CREATE_SNIPPET_FIX_ID,
                CreateSnippetPayload {
                    key,
                    admin: code.as_str() == "admin.snippet.missing",
                },
            )]
        })),
    })
}

struct CreateSnippetFix;

impl QuickFix for CreateSnippetFix {
    fn id(&self) -> FixId {
        FixId::from(CREATE_SNIPPET_FIX_ID)
    }

    fn present(
        &self,
        _ctx: &Context,
        fix: &FixContext,
    ) -> Result<Option<FixPresentation>, BoxError> {
        let payload: CreateSnippetPayload = fix.fix_payload()?;
        if payload.key.is_empty() {
            return Ok(None);
        }
        let title = if payload.admin {
            format!("Create admin snippet '{}'", payload.key)
        } else {
            format!("Create snippet '{}'", payload.key)
        };
        Ok(Some(FixPresentation {
            title,
            kind: Some(protocol::CodeActionKind::QUICKFIX),
            preferred: false,
            resolution: FixResolution::Eager,
        }))
    }

    fn as_command(&self) -> Option<&dyn CommandQuickFix> {
        Some(self)
    }
}

impl CommandQuickFix for CreateSnippetFix {
    fn build_command(
        &self,
        _ctx: &Context,
        fix: &FixContext,
    ) -> Result<Option<protocol::CommandAction>, BoxError> {
        let payload: CreateSnippetPayload = fix.fix_payload()?;
        let (command, title) = if payload.admin {
            ("shopware.createAdminSnippet", "Create Admin Snippet")
        } else {
            ("shopware.createSnippet", "Create Snippet")
        };
        Ok(Some(protocol::CommandAction {
            title: title.into(),
            command: command.into(),
            arguments: vec![
                Value::String(payload.key),
                Value::String(fix.document.uri.clone()),
            ],
        }))
    }
}
