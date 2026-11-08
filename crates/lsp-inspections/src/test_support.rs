//! Shared test helpers (Go `inspection_test.go` helpers).

use std::collections::HashMap;
use std::sync::Arc;

use swlsp_lsp::{
    BoundFix, BoxError, Context, DocumentResolver, DocumentSnapshot, FixContext, Inspection,
    Problem, ProblemReporter, QuickFix, TextDocument, protocol_range,
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
