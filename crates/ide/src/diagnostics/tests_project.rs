use std::sync::Arc;
use std::sync::atomic::{AtomicU64, AtomicUsize, Ordering};

use swlsp_language::LanguageId;
use swlsp_lsp::{
    BoxError, CancellationToken, Context, InspectionScope, InspectionSpec, Problem, TextDocument,
};

use crate::diagnostics::test_support::document;
use crate::diagnostics::{ProblemAnalyzer, Project, ProjectAnalysis, SummaryHandle};

/// Counts summaries; documents containing `needs` ask for one.
#[derive(Default)]
struct Counting {
    revision: AtomicU64,
    summaries: AtomicUsize,
}

impl ProjectAnalysis for Counting {
    type Summary = u64;

    const INSPECTION: InspectionSpec = InspectionSpec {
        id: "test.project",
        description: "Test inspection.",
        domain: "",
        scope: InspectionScope::Framework,
        languages: &[LanguageId::Php],
        source: "test",
        codes: &[],
    };

    fn revision(&self) -> u64 {
        self.revision.load(Ordering::SeqCst)
    }

    fn summarize(&self, ctx: &Context) -> Result<u64, BoxError> {
        self.summaries.fetch_add(1, Ordering::SeqCst);
        if ctx.is_cancelled() {
            return Err(swlsp_lsp::Cancelled.into());
        }
        Ok(self.revision())
    }

    fn analyze(
        &self,
        _ctx: &Context,
        document: &Arc<TextDocument>,
        summary: &SummaryHandle<'_, Self>,
    ) -> Result<Vec<Problem>, BoxError> {
        if document.source().contains("needs") {
            summary.get()?;
        }
        Ok(Vec::new())
    }
}

fn run(project: &Project<Counting>, ctx: &Context, source: &str) -> Result<(), BoxError> {
    project
        .analyze(ctx, &document("file:///a.php", source))
        .map(|_| ())
}

#[test]
fn summaries_are_lazy_and_memoized_per_revision() {
    let project = Project::new(Counting::default());
    let ctx = Context::background();
    let summaries = || project.analysis().summaries.load(Ordering::SeqCst);

    run(&project, &ctx, "<?php other").unwrap();
    assert_eq!(summaries(), 0, "documents that do not ask never summarize");

    run(&project, &ctx, "<?php needs").unwrap();
    run(&project, &ctx, "<?php needs").unwrap();
    assert_eq!(summaries(), 1);

    project.analysis().revision.store(1, Ordering::SeqCst);
    run(&project, &ctx, "<?php needs").unwrap();
    assert_eq!(summaries(), 2);

    // A cancelled summary is not memoized.
    project.analysis().revision.store(2, Ordering::SeqCst);
    let token = CancellationToken::new();
    token.cancel();
    assert!(run(&project, &Context::with_token(token), "<?php needs").is_err());
    run(&project, &ctx, "<?php needs").unwrap();
    assert_eq!(summaries(), 4);
}
