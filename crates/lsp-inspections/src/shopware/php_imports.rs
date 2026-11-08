//! Port of Go `php_imports.go`: unused PHP imports and their removal.

use std::sync::Arc;

use serde::{Deserialize, Serialize};
use swlsp_language::LanguageId;
use swlsp_lsp::{
    BoxError, Context, DiagnosticId, FixContext, FixId, FixPresentation, FixResolution, Inspection,
    InspectionDefinition, Problem, ProblemDefinition, ProblemReporter, QuickFix, RewriteQuickFix,
    TextDocument, bind_fix,
};
use swlsp_lsp_protocol as protocol;
use swlsp_php::PhpIndex;
use swlsp_rewrite::{DocumentPlan, WorkspacePlan};
use swlsp_syntax::{TextRange, TextSize};

use crate::resolve_anchor;

pub const UNUSED_IMPORT_FIX_ID: &str = "php.remove-unused-import";

/// Go `cst.TextRange` JSON (`{"Start":..,"End":..}`).
#[derive(Debug, Clone, Copy, Default, PartialEq, Eq, Serialize, Deserialize)]
struct RangeJson {
    #[serde(rename = "Start", default)]
    start: u32,
    #[serde(rename = "End", default)]
    end: u32,
}

impl From<TextRange> for RangeJson {
    fn from(range: TextRange) -> Self {
        Self {
            start: range.start().into(),
            end: range.end().into(),
        }
    }
}

impl RangeJson {
    fn matches(self, range: TextRange) -> bool {
        self.start <= self.end
            && range == TextRange::new(TextSize::from(self.start), TextSize::from(self.end))
    }
}

#[derive(Debug, Clone, Default, Serialize, Deserialize)]
struct UnusedImportPayload {
    #[serde(default)]
    range: RangeJson,
    #[serde(default)]
    alias: String,
}

/// Go `NewPHPImports`.
pub fn new_php_imports(index: Option<PhpIndex>) -> Arc<dyn Inspection> {
    Arc::new(PhpImportsInspection { index })
}

struct PhpImportsInspection {
    index: Option<PhpIndex>,
}

impl Inspection for PhpImportsInspection {
    fn definition(&self) -> InspectionDefinition {
        InspectionDefinition {
            id: "php.imports".to_owned(),
            languages: vec![LanguageId::Php],
            problems: vec![ProblemDefinition {
                id: DiagnosticId::from("php.unusedImport"),
                source: "shopware-php".to_owned(),
                default_severity: protocol::DiagnosticSeverity::HINT,
                disabled_by_default: false,
            }],
        }
    }

    fn quick_fixes(&self) -> Vec<Arc<dyn QuickFix>> {
        vec![Arc::new(UnusedImportFix {
            index: self.index.clone(),
        })]
    }

    fn inspect(
        &self,
        ctx: &Context,
        document: &Arc<TextDocument>,
        reporter: &mut dyn ProblemReporter,
    ) -> Result<(), BoxError> {
        let Some(index) = self.index.as_ref() else {
            return Ok(());
        };
        let analysis = swlsp_lsp_phpanalysis::imports(ctx, index, document)?;
        for scope in &analysis.scopes {
            if scope.unsafe_scope {
                continue;
            }
            for declaration in &scope.declarations {
                for item in &declaration.items {
                    ctx.check()?;
                    if item.used {
                        continue;
                    }
                    let payload = UnusedImportPayload {
                        range: item.range.into(),
                        alias: item.import.alias.clone(),
                    };
                    reporter.report(Problem {
                        id: DiagnosticId::from("php.unusedImport"),
                        range: item.name_range,
                        element: Some(declaration.node.clone().into()),
                        message: format!("Import {} is never used", item.import.alias),
                        severity: Some(protocol::DiagnosticSeverity::HINT),
                        tags: vec![protocol::DiagnosticTag::UNNECESSARY],
                        payload: Some(serde_json::to_value(&payload)?),
                        fixes: vec![bind_fix(UNUSED_IMPORT_FIX_ID, &payload)],
                        ..Problem::default()
                    })?;
                }
            }
        }
        Ok(())
    }
}

struct UnusedImportFix {
    index: Option<PhpIndex>,
}

impl QuickFix for UnusedImportFix {
    fn id(&self) -> FixId {
        FixId::from(UNUSED_IMPORT_FIX_ID)
    }

    fn present(
        &self,
        _ctx: &Context,
        fix: &FixContext,
    ) -> Result<Option<FixPresentation>, BoxError> {
        let payload: UnusedImportPayload = fix.fix_payload()?;
        if payload.alias.is_empty() {
            return Ok(None);
        }
        Ok(Some(FixPresentation {
            title: format!("Remove unused import '{}'", payload.alias),
            kind: Some(protocol::CodeActionKind::QUICKFIX),
            preferred: true,
            resolution: FixResolution::Lazy,
        }))
    }

    fn as_rewrite(&self) -> Option<&dyn RewriteQuickFix> {
        Some(self)
    }
}

impl RewriteQuickFix for UnusedImportFix {
    fn build(&self, ctx: &Context, fix: &FixContext) -> Result<WorkspacePlan, BoxError> {
        resolve_anchor(fix)?;
        let payload: UnusedImportPayload = fix.fix_payload()?;
        let index = self.index.as_ref().ok_or("php index is not available")?;
        let analysis = swlsp_lsp_phpanalysis::imports(ctx, index, &fix.document)?;
        for scope in &analysis.scopes {
            if scope.unsafe_scope {
                continue;
            }
            for declaration in &scope.declarations {
                for item in &declaration.items {
                    if !payload.range.matches(item.range) || item.used {
                        continue;
                    }
                    let edits = swlsp_php_imports::remove(
                        fix.document.source(),
                        declaration,
                        |candidate| payload.range.matches(candidate.range),
                    )?;
                    return Ok(WorkspacePlan {
                        documents: vec![DocumentPlan::new(
                            fix.document.uri.clone(),
                            Some(fix.document.version),
                            fix.document.source_arc().clone(),
                            edits,
                        )],
                        ..WorkspacePlan::default()
                    });
                }
            }
        }
        Err("unused import is no longer available".into())
    }
}
