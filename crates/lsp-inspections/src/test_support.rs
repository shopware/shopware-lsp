//! Shared test helpers (Go `inspection_test.go` helpers).

use std::collections::HashMap;
use std::sync::Arc;

use swlsp_lsp::{
    BoundFix, BoxError, Context, DocumentResolver, DocumentSnapshot, FixContext, FixPresentation,
    Inspection, Problem, ProblemReporter, QuickFix, TextDocument, fixture, protocol_range,
};
use swlsp_lsp_protocol as protocol;
use swlsp_rewrite::ElementHandle;
use swlsp_syntax::{SyntaxNodeExt, TextRange};

/// Collects reported problems unchanged (Go `problemCollector`).
#[derive(Default)]
pub struct ProblemCollector {
    pub problems: Vec<Problem>,
}

impl ProblemReporter for ProblemCollector {
    fn report(&mut self, problem: Problem) -> Result<(), BoxError> {
        self.problems.push(problem);
        Ok(())
    }
}

/// Go `staticDocumentResolver`.
#[derive(Default, Clone)]
pub struct StaticDocumentResolver(pub HashMap<String, DocumentSnapshot>);

impl DocumentResolver for StaticDocumentResolver {
    fn resolve_document(&self, _ctx: &Context, uri: &str) -> Result<DocumentSnapshot, BoxError> {
        self.0
            .get(uri)
            .cloned()
            .ok_or_else(|| format!("document {uri} not found").into())
    }
}

pub fn document(uri: &str, source: &str) -> Arc<TextDocument> {
    Arc::new(TextDocument::new(uri, source, 1))
}

pub fn inspect(inspection: &dyn Inspection, document: &Arc<TextDocument>) -> Vec<Problem> {
    let mut collector = ProblemCollector::default();
    inspection
        .inspect(&Context::background(), document, &mut collector)
        .expect("inspect");
    collector.problems
}

/// Go `quickFixWithID`.
pub fn quick_fix_with_id(inspection: &dyn Inspection, id: &str) -> Arc<dyn QuickFix> {
    let fix = inspection
        .quick_fixes()
        .into_iter()
        .find(|fix| fix.id().0 == id)
        .unwrap_or_else(|| panic!("quick fix {id:?} was not registered"));
    assert!(
        fix.as_rewrite().is_some(),
        "quick fix {id:?} is not a rewrite"
    );
    fix
}

/// Go `fixContext`.
pub fn fix_context(
    document: &Arc<TextDocument>,
    problem: &Problem,
    bound: &BoundFix,
    documents: Option<Arc<dyn DocumentResolver>>,
) -> FixContext {
    let element = problem
        .element
        .clone()
        .or_else(|| {
            document
                .root()
                .and_then(|root| root.descendant_for_range(problem.range))
        })
        .expect("problem element");
    let anchor = ElementHandle::new(
        document.uri.clone(),
        document.version,
        document.language.expect("language"),
        element,
    );
    FixContext {
        document: document.clone(),
        diagnostic: protocol::Diagnostic {
            range: wire_range(document, problem.range),
            code: Some(serde_json::Value::String(problem.id.0.clone())),
            message: problem.message.clone(),
            ..protocol::Diagnostic::default()
        },
        anchor,
        problem_payload: problem.payload.clone(),
        fix_payload: bound.payload.clone(),
        documents: documents.unwrap_or_else(|| Arc::new(StaticDocumentResolver::default())),
    }
}

/// Go `wireRange`.
pub fn wire_range(document: &TextDocument, range: TextRange) -> protocol::Range {
    protocol_range(document.line_index(), range)
}

/// Builds the fix and applies its single document plan.
pub fn apply_single(fix: &Arc<dyn QuickFix>, context: &FixContext) -> String {
    let plan = fix
        .as_rewrite()
        .expect("rewrite fix")
        .build(&Context::background(), context)
        .expect("build");
    assert_eq!(plan.documents.len(), 1, "plan documents");
    plan.documents[0].apply().expect("apply")
}

/// Asserts that `inspection` reports exactly the problems marked in
/// `fixture` (see [`swlsp_lsp::fixture`]) for the document `uri`, and returns
/// them in markup order.
pub fn check_diagnostics(
    inspection: &dyn Inspection,
    uri: &str,
    fixture: &str,
) -> (Arc<TextDocument>, Vec<Problem>) {
    fixture::check(uri, fixture, |document| inspect(inspection, document))
}

/// Applies the quick fix `fix_id` to the one problem marked in `before`
/// that has it bound, and asserts the result is `after`. `before` may mark
/// other problems, and the inspection may report unmarked ones; both are
/// ignored, so the fixture of [`check_diagnostics`] can be reused. Returns
/// the fix's presentation.
pub fn check_fix(
    inspection: &dyn Inspection,
    uri: &str,
    before: &str,
    fix_id: &str,
    after: &str,
) -> FixPresentation {
    check_fix_with(inspection, uri, before, fix_id, after, None)
}

/// [`check_fix`] with other open documents for cross-file fixes.
pub fn check_fix_with(
    inspection: &dyn Inspection,
    uri: &str,
    before: &str,
    fix_id: &str,
    after: &str,
    documents: Option<Arc<dyn DocumentResolver>>,
) -> FixPresentation {
    let (source, marked) = fixture::parse(before);
    let document = document(uri, &source);
    let problems = inspect(inspection, &document);
    let candidates: Vec<(&Problem, &BoundFix)> = problems
        .iter()
        .filter(|problem| {
            marked
                .iter()
                .any(|mark| mark.code == problem.id.as_str() && mark.range == problem.range)
        })
        .filter_map(|problem| {
            let bound = problem
                .fixes
                .iter()
                .find(|bound| bound.id.as_str() == fix_id)?;
            Some((problem, bound))
        })
        .collect();
    let [(problem, bound)] = candidates.as_slice() else {
        let reported: Vec<_> = problems
            .iter()
            .map(|problem| fixture::Marked::new(problem.id.as_str(), problem.range))
            .collect();
        panic!(
            "expected one marked problem with fix {fix_id:?}, found {}; reported:\n{}",
            candidates.len(),
            fixture::render(&source, &reported)
        );
    };
    let fix = quick_fix_with_id(inspection, fix_id);
    let context = fix_context(&document, problem, bound, documents);
    let presentation = fix
        .present(&Context::background(), &context)
        .expect("present")
        .unwrap_or_else(|| panic!("fix {fix_id:?} is not offered"));
    assert_eq!(apply_single(&fix, &context), after, "fix {fix_id:?}");
    presentation
}

/// Several inspections run as one, for fixtures spanning inspections that
/// were split from one (the Administration inspections).
pub struct Combined(pub Vec<Arc<dyn Inspection>>);

impl Inspection for Combined {
    fn definition(&self) -> swlsp_lsp::InspectionDefinition {
        let mut definition = self.0[0].definition();
        for inspection in &self.0[1..] {
            definition.problems.extend(inspection.definition().problems);
        }
        definition
    }

    fn inspect(
        &self,
        ctx: &Context,
        document: &Arc<TextDocument>,
        reporter: &mut dyn ProblemReporter,
    ) -> Result<(), BoxError> {
        for inspection in &self.0 {
            inspection.inspect(ctx, document, reporter)?;
        }
        Ok(())
    }

    fn quick_fixes(&self) -> Vec<Arc<dyn QuickFix>> {
        let mut fixes: Vec<Arc<dyn QuickFix>> = Vec::new();
        for fix in self
            .0
            .iter()
            .flat_map(|inspection| inspection.quick_fixes())
        {
            if !fixes.iter().any(|known| known.id() == fix.id()) {
                fixes.push(fix);
            }
        }
        fixes
    }
}
