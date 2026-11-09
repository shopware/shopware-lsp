//! Missing Symfony translation inspection with a fix inserting the key into a
//! writable resource.

use std::sync::Arc;

use serde::{Deserialize, Serialize};
use swlsp_language::LanguageId;
use swlsp_lsp::{
    BoxError, Context, FixContext, FixId, FixPresentation, FixResolution, Inspection,
    InspectionDefinition, InspectionScope, ProblemDefinition, QuickFix, RewriteQuickFix, bind_fix,
};
use swlsp_lsp_diagnostics::TranslationAnalyzer;
use swlsp_lsp_protocol as protocol;
use swlsp_php::PhpIndex;
use swlsp_rewrite::{Builder, DocumentPlan, WorkspacePlan};
use swlsp_snippet::SnippetIndexer;
use swlsp_translation::Index;

use super::{base_name, clean_path, map_string};
use crate::{BoundInspection, SuggestionFix, resolve_anchor, suggestion_bound_fixes};

pub(crate) const ADD_TRANSLATION_FIX_ID: &str = "add-missing-translation";

#[derive(Debug, Clone, Default, Serialize, Deserialize)]
pub(crate) struct AddTranslationPayload {
    #[serde(default)]
    pub(crate) domain: String,
    #[serde(default)]
    pub(crate) key: String,
    #[serde(default)]
    pub(crate) file: String,
}

pub fn new_translation(
    index: Option<Arc<Index>>,
    php_index: Option<PhpIndex>,
    snippet_index: Option<Arc<SnippetIndexer>>,
) -> Arc<dyn Inspection> {
    let problem = |id: &str| ProblemDefinition {
        id: id.into(),
        source: "symfony".into(),
        default_severity: protocol::DiagnosticSeverity::WARNING,
        disabled_by_default: false,
    };
    let bind_index = index.clone();
    Arc::new(BoundInspection {
        definition: InspectionDefinition {
            id: "symfony.translation".into(),
            description: "Translation keys and domains no translation file defines, with a fix adding the key.",
            domain: "symfony",
            scope: InspectionScope::Framework,
            languages: vec![LanguageId::Php, LanguageId::Twig],
            problems: vec![
                problem("symfony.translation.domain.missing"),
                problem("symfony.translation.key.missing"),
            ],
        },
        analyzers: vec![Arc::new(TranslationAnalyzer::new(
            index.clone(),
            php_index,
            snippet_index,
        ))],
        fixes: vec![
            Arc::new(SuggestionFix),
            Arc::new(AddTranslationFix { index }),
        ],
        bind: Some(Arc::new(move |code, payload| {
            let mut bound = suggestion_bound_fixes(payload);
            let Some(index) = &bind_index else {
                return bound;
            };
            if code.as_str() != "symfony.translation.key.missing" {
                return bound;
            }
            let domain = map_string(payload, "domain");
            let key = map_string(payload, "key");
            let Ok(insertions) = index.insertion_targets(&domain) else {
                return bound;
            };
            for insertion in insertions {
                bound.push(bind_fix(
                    ADD_TRANSLATION_FIX_ID,
                    AddTranslationPayload {
                        domain: domain.clone(),
                        key: key.clone(),
                        file: insertion.file,
                    },
                ));
            }
            bound
        })),
    })
}

pub(crate) struct AddTranslationFix {
    pub(crate) index: Option<Arc<Index>>,
}

impl QuickFix for AddTranslationFix {
    fn id(&self) -> FixId {
        FixId::from(ADD_TRANSLATION_FIX_ID)
    }

    fn present(
        &self,
        _ctx: &Context,
        fix: &FixContext,
    ) -> Result<Option<FixPresentation>, BoxError> {
        let payload: AddTranslationPayload = fix.fix_payload()?;
        if payload.domain.is_empty() || payload.key.is_empty() || payload.file.is_empty() {
            return Ok(None);
        }
        Ok(Some(FixPresentation {
            title: format!(
                "Symfony: Add translation '{}' to {}",
                payload.key,
                base_name(&payload.file)
            ),
            kind: Some(protocol::CodeActionKind::QUICKFIX),
            preferred: false,
            resolution: FixResolution::Lazy,
        }))
    }

    fn as_rewrite(&self) -> Option<&dyn RewriteQuickFix> {
        Some(self)
    }
}

impl RewriteQuickFix for AddTranslationFix {
    fn build(&self, ctx: &Context, fix: &FixContext) -> Result<WorkspacePlan, BoxError> {
        let payload: AddTranslationPayload = fix.fix_payload()?;
        resolve_anchor(fix)?;
        let Some(index) = &self.index else {
            return Err("translation target is no longer writable".into());
        };
        let wanted = clean_path(&payload.file);
        let Some(selected) = index
            .insertion_targets(&payload.domain)?
            .into_iter()
            .find(|insertion| clean_path(&insertion.file) == wanted)
        else {
            return Err("translation target is no longer writable".into());
        };
        let target_uri = swlsp_uriutil::file_uri(&selected.file);
        let target = fix.documents.resolve_document(ctx, &target_uri)?;
        let source = target.document.source();
        let Some(insertion) = swlsp_translation::insertion_for_source(
            &selected.file,
            source,
            &payload.key,
            &payload.key,
        ) else {
            return Err("translation target does not support insertion".into());
        };
        let offset = target
            .document
            .line_index()
            .offset_utf16(insertion.line, insertion.character);
        let mut builder = Builder::new(source);
        builder.insert(offset, insertion.new_text)?;
        let edits = builder.finish()?;
        Ok(WorkspacePlan {
            documents: vec![
                DocumentPlan::new(
                    target_uri,
                    target.version,
                    target.document.source_arc().clone(),
                    edits,
                )
                .with_line_index(target.document.line_index_arc().clone()),
            ],
            ..WorkspacePlan::default()
        })
    }
}
