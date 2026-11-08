//! The analyzer-backed inspection adapters (Go `inspection.go`).

use std::collections::HashSet;
use std::sync::Arc;

use serde_json::{Map, Value};
use swlsp_language::LanguageId;
use swlsp_lsp::{
    BoundFix, BoxError, Context, DiagnosticId, FixContext, Inspection, InspectionDefinition,
    Problem, ProblemDefinition, ProblemReporter, QuickFix, TextDocument,
};
use swlsp_lsp_diagnostics::ProblemAnalyzer;
use swlsp_lsp_protocol as protocol;
use swlsp_rewrite::HandleError;
use swlsp_syntax::{SyntaxElement, SyntaxNodeExt};

use crate::suggestion::{SuggestionFix, suggestion_bound_fixes};

/// Binds quick fixes to a reported problem from its code and normalized
/// payload (Go `bindProblems`).
pub type BindProblems =
    Arc<dyn Fn(&DiagnosticId, &Map<String, Value>) -> Vec<BoundFix> + Send + Sync>;

/// Go `NewAnalyzerInspection`: an inspection whose problems are declared as
/// warnings with one source and offer the suggestion quick fix.
pub fn new_analyzer_inspection(
    id: &str,
    languages: &[LanguageId],
    source: &str,
    codes: &[&str],
    analyzers: Vec<Arc<dyn ProblemAnalyzer>>,
) -> Arc<dyn Inspection> {
    let problems = codes
        .iter()
        .map(|code| ProblemDefinition {
            id: DiagnosticId::from(*code),
            source: source.to_owned(),
            default_severity: protocol::DiagnosticSeverity::WARNING,
            disabled_by_default: false,
        })
        .collect();
    Arc::new(AnalyzerInspection {
        definition: InspectionDefinition {
            id: id.to_owned(),
            languages: languages.to_vec(),
            problems,
        },
        analyzers,
        fixes: vec![Arc::new(SuggestionFix)],
    })
}

pub struct AnalyzerInspection {
    definition: InspectionDefinition,
    analyzers: Vec<Arc<dyn ProblemAnalyzer>>,
    fixes: Vec<Arc<dyn QuickFix>>,
}

impl Inspection for AnalyzerInspection {
    fn definition(&self) -> InspectionDefinition {
        self.definition.clone()
    }

    fn quick_fixes(&self) -> Vec<Arc<dyn QuickFix>> {
        self.fixes.clone()
    }

    fn inspect(
        &self,
        ctx: &Context,
        document: &Arc<TextDocument>,
        reporter: &mut dyn ProblemReporter,
    ) -> Result<(), BoxError> {
        let declared: HashSet<&DiagnosticId> = self
            .definition
            .problems
            .iter()
            .map(|problem| &problem.id)
            .collect();
        for analyzer in &self.analyzers {
            let problems = analyzer.analyze(ctx, document)?;
            for mut problem in problems {
                if !declared.contains(&problem.id) {
                    return Err(format!(
                        "inspection {:?} received undeclared problem {:?}",
                        self.definition.id, problem.id.0
                    )
                    .into());
                }
                let payload = diagnostic_payload(problem.payload.take());
                problem.fixes.extend(suggestion_bound_fixes(&payload));
                problem.payload = Some(Value::Object(payload));
                reporter.report(problem)?;
            }
        }
        Ok(())
    }
}

/// Go `boundInspection`: runs analyzers in order and binds fixes through
/// `bind`.
pub struct BoundInspection {
    pub definition: InspectionDefinition,
    pub analyzers: Vec<Arc<dyn ProblemAnalyzer>>,
    pub fixes: Vec<Arc<dyn QuickFix>>,
    pub bind: Option<BindProblems>,
}

impl Inspection for BoundInspection {
    fn definition(&self) -> InspectionDefinition {
        self.definition.clone()
    }

    fn quick_fixes(&self) -> Vec<Arc<dyn QuickFix>> {
        self.fixes.clone()
    }

    fn inspect(
        &self,
        ctx: &Context,
        document: &Arc<TextDocument>,
        reporter: &mut dyn ProblemReporter,
    ) -> Result<(), BoxError> {
        for analyzer in &self.analyzers {
            let problems = analyzer.analyze(ctx, document)?;
            self.report_problems(ctx, document, reporter, problems)?;
        }
        Ok(())
    }
}

impl BoundInspection {
    fn report_problems(
        &self,
        ctx: &Context,
        document: &TextDocument,
        reporter: &mut dyn ProblemReporter,
        problems: Vec<Problem>,
    ) -> Result<(), BoxError> {
        let declared: HashSet<&DiagnosticId> = self
            .definition
            .problems
            .iter()
            .map(|problem| &problem.id)
            .collect();
        let root = document.root();
        for mut problem in problems {
            ctx.check()?;
            if !declared.contains(&problem.id) {
                return Err(format!(
                    "inspection {:?} received undeclared problem {:?}",
                    self.definition.id, problem.id.0
                )
                .into());
            }
            if problem.element.is_none()
                && let Some(root) = &root
            {
                problem.element = root.descendant_for_range(problem.range);
            }
            let payload = diagnostic_payload(problem.payload.take());
            if let Some(bind) = &self.bind {
                let bound = bind(&problem.id, &payload);
                problem.fixes.extend(bound);
            }
            problem.payload = Some(Value::Object(payload));
            reporter.report(problem)?;
        }
        Ok(())
    }
}

/// Go `diagnosticPayload`: the problem payload as a JSON object. Absent and
/// `null` payloads become `{}`, non-object payloads `{"payload": value}`.
pub fn diagnostic_payload(value: Option<Value>) -> Map<String, Value> {
    match value {
        None | Some(Value::Null) => Map::new(),
        Some(Value::Object(map)) => map,
        Some(other) => {
            let mut map = Map::new();
            map.insert("payload".to_owned(), other);
            map
        }
    }
}

/// Resolves the fix anchor against the fix document (Go
/// `fixContext.Anchor.Resolve(...)`).
pub fn resolve_anchor(fix: &FixContext) -> Result<SyntaxElement, BoxError> {
    let document = &fix.document;
    let Some(language) = document.language else {
        return Err(Box::new(HandleError::Stale));
    };
    let Some(tree) = document.tree() else {
        return Err(Box::new(HandleError::Missing));
    };
    Ok(fix
        .anchor
        .resolve(&document.uri, document.version, language, tree)?)
}
