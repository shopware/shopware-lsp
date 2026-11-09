//! The analyzer-backed inspection adapters.

use std::collections::HashSet;
use std::sync::Arc;

use serde_json::{Map, Value};
use swlsp_lsp::{
    BoundFix, BoxError, Context, DiagnosticId, FixContext, Inspection, InspectionDefinition,
    InspectionSpec, Problem, ProblemReporter, QuickFix, TextDocument,
};
use swlsp_lsp_diagnostics::{DeclaredAnalyzer, ProblemAnalyzer};
use swlsp_rewrite::HandleError;
use swlsp_syntax::{SyntaxElement, SyntaxNodeExt};

use crate::suggestion::{SuggestionFix, suggestion_bound_fixes};

/// An analyzer reported a problem its inspection does not declare.
#[derive(Debug, Clone, PartialEq, Eq, thiserror::Error)]
#[error("inspection {inspection:?} received undeclared problem {problem:?}")]
pub struct UndeclaredProblemError {
    pub inspection: String,
    pub problem: String,
}

/// Binds quick fixes to a reported problem from its code and normalized
/// payload.
pub type BindProblems =
    Arc<dyn Fn(&DiagnosticId, &Map<String, Value>) -> Vec<BoundFix> + Send + Sync>;

/// The inspection an analyzer declares, offering the suggestion quick fix.
pub fn analyzer_inspection<A: DeclaredAnalyzer + 'static>(analyzer: A) -> Arc<dyn Inspection> {
    new_analyzer_inspection(A::INSPECTION, vec![Arc::new(analyzer)])
}

/// An inspection running `analyzers` under `spec`, offering the suggestion
/// quick fix.
pub fn new_analyzer_inspection(
    spec: InspectionSpec,
    analyzers: Vec<Arc<dyn ProblemAnalyzer>>,
) -> Arc<dyn Inspection> {
    Arc::new(AnalyzerInspection {
        definition: spec.definition(),
        analyzers,
        fixes: vec![Arc::new(SuggestionFix)],
    })
}

pub struct AnalyzerInspection {
    definition: InspectionDefinition,
    analyzers: Vec<Arc<dyn ProblemAnalyzer>>,
    fixes: Vec<Arc<dyn QuickFix>>,
}

/// The files any analyzer may report on although they are not open.
fn workspace_paths(analyzers: &[Arc<dyn ProblemAnalyzer>], ctx: &Context) -> Vec<String> {
    let mut paths: Vec<String> = analyzers
        .iter()
        .flat_map(|analyzer| analyzer.workspace_paths(ctx))
        .collect();
    paths.sort();
    paths.dedup();
    paths
}

/// The sum of the analyzers' input revisions, `None` unless every analyzer
/// declares one.
fn combined_input_revision(
    analyzers: &[Arc<dyn ProblemAnalyzer>],
    document: &TextDocument,
) -> Option<u64> {
    analyzers
        .iter()
        .map(|analyzer| analyzer.input_revision(document))
        .sum()
}

impl Inspection for AnalyzerInspection {
    fn definition(&self) -> InspectionDefinition {
        self.definition.clone()
    }

    fn workspace_paths(&self, ctx: &Context) -> Vec<String> {
        workspace_paths(&self.analyzers, ctx)
    }

    fn input_revision(&self, document: &TextDocument) -> Option<u64> {
        combined_input_revision(&self.analyzers, document)
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
                    return Err(UndeclaredProblemError {
                        inspection: self.definition.id.clone(),
                        problem: problem.id.0.clone(),
                    }
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

/// Runs analyzers in order and binds fixes through `bind`. Its input revision
/// is its analyzers'; `bind` must read nothing they do not declare, since bound
/// fixes are part of the reused diagnostics.
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

    fn workspace_paths(&self, ctx: &Context) -> Vec<String> {
        workspace_paths(&self.analyzers, ctx)
    }

    fn input_revision(&self, document: &TextDocument) -> Option<u64> {
        combined_input_revision(&self.analyzers, document)
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
                return Err(UndeclaredProblemError {
                    inspection: self.definition.id.clone(),
                    problem: problem.id.0.clone(),
                }
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

/// The problem payload as a JSON object. Absent and `null` payloads become
/// `{}`, non-object payloads `{"payload": value}`.
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

/// Resolves the fix anchor against the fix document.
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
