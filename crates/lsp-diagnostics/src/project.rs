//! Project analyses: rules about the project as a whole.
//!
//! A per-document analyzer sees one file. A rule such as "this route name is
//! also defined in another file" needs facts from every file, which only the
//! indexes have. A [`ProjectAnalysis`] splits such a rule in two phases, like
//! PHPStan's collectors and IntelliJ's global inspections:
//!
//! 1. [`ProjectAnalysis::summarize`] derives a summary from index data. It
//!    runs at most once per [`ProjectAnalysis::revision`], the combined
//!    revision of every index it reads, and only when a document asks for it,
//!    so documents the rule cannot apply to never pay for it.
//! 2. [`ProjectAnalysis::analyze`] checks one document against the summary.
//!    It anchors problems in the document's live syntax tree, so unsaved
//!    edits are reflected for the document itself and ranges never come from
//!    stale index data.
//!
//! [`Project`] adapts an analysis to a [`DeclaredAnalyzer`], so it becomes an
//! ordinary inspection with gating, suppression, configuration and quick
//! fixes. Diagnostics of open documents are refreshed after every committed
//! index change, which picks up a changed summary.

use std::sync::{Arc, Mutex, PoisonError};

use swlsp_lsp::{BoxError, Context, InspectionSpec, Problem, TextDocument};

use crate::{DeclaredAnalyzer, ProblemAnalyzer};

/// A rule checked against a summary of the whole project.
pub trait ProjectAnalysis: Sized + Send + Sync + 'static {
    type Summary: Send + Sync + 'static;

    const INSPECTION: InspectionSpec;

    /// The combined revision of every input of [`Self::summarize`]; a change
    /// of any input must change it. Read before summarizing, so a change
    /// committed meanwhile is not memoized under the old revision.
    fn revision(&self) -> u64;

    /// Derives the summary from index data.
    fn summarize(&self, ctx: &Context) -> Result<Self::Summary, BoxError>;

    /// The files the summary says may have problems, reported in workspace
    /// diagnostics even when they are not open. Empty by default.
    fn affected_paths(&self, _summary: &Self::Summary) -> Vec<String> {
        Vec::new()
    }

    /// Checks `document`, asking `summary` for the project summary only when
    /// the document can be affected.
    fn analyze(
        &self,
        ctx: &Context,
        document: &Arc<TextDocument>,
        summary: &SummaryHandle<'_, Self>,
    ) -> Result<Vec<Problem>, BoxError>;
}

/// Lazy access to the memoized summary of one [`Project`].
pub struct SummaryHandle<'a, P: ProjectAnalysis> {
    project: &'a Project<P>,
    ctx: &'a Context,
}

impl<P: ProjectAnalysis> SummaryHandle<'_, P> {
    /// The summary for the current revision, derived on first use.
    pub fn get(&self) -> Result<Arc<P::Summary>, BoxError> {
        self.project.summary(self.ctx)
    }
}

struct Memo<S> {
    revision: u64,
    summary: Arc<S>,
}

/// Runs a [`ProjectAnalysis`] as a per-document analyzer.
pub struct Project<P: ProjectAnalysis> {
    analysis: P,
    /// Held while summarizing, so concurrent documents wait for one
    /// derivation instead of each running their own.
    memo: Mutex<Option<Memo<P::Summary>>>,
}

impl<P: ProjectAnalysis> Project<P> {
    pub fn new(analysis: P) -> Self {
        Project {
            analysis,
            memo: Mutex::new(None),
        }
    }

    pub fn analysis(&self) -> &P {
        &self.analysis
    }

    fn summary(&self, ctx: &Context) -> Result<Arc<P::Summary>, BoxError> {
        let revision = self.analysis.revision();
        let mut memo = self.memo.lock().unwrap_or_else(PoisonError::into_inner);
        if let Some(memo) = memo.as_ref()
            && memo.revision == revision
        {
            return Ok(memo.summary.clone());
        }
        let summary = Arc::new(self.analysis.summarize(ctx)?);
        if !ctx.is_cancelled() {
            *memo = Some(Memo {
                revision,
                summary: summary.clone(),
            });
        }
        Ok(summary)
    }
}

impl<P: ProjectAnalysis> ProblemAnalyzer for Project<P> {
    /// The per-document phase reads only the document and the summary, whose
    /// inputs [`ProjectAnalysis::revision`] covers.
    fn input_revision(&self, _document: &swlsp_lsp::TextDocument) -> Option<u64> {
        Some(self.analysis.revision())
    }

    fn workspace_paths(&self, ctx: &Context) -> Vec<String> {
        self.summary(ctx)
            .map(|summary| self.analysis.affected_paths(&summary))
            .unwrap_or_default()
    }

    fn analyze(
        &self,
        ctx: &Context,
        document: &Arc<TextDocument>,
    ) -> Result<Vec<Problem>, BoxError> {
        let summary = SummaryHandle { project: self, ctx };
        self.analysis.analyze(ctx, document, &summary)
    }
}

impl<P: ProjectAnalysis> DeclaredAnalyzer for Project<P> {
    const INSPECTION: InspectionSpec = P::INSPECTION;
}
