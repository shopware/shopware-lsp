//! One analyzer behind several inspections.
//!
//! A large analyzer (the Administration checks, the Shopware migrations)
//! computes all its problems in one pass that shares per-document work, but
//! its rules belong to separate inspections so users can configure them, and
//! inputs and timings can be told apart. [`SharedAnalysis`] runs the analyzer
//! once per document snapshot and input revision; each inspection gets a
//! [`CodeGroup`] that reports only its own codes from that result.
//!
//! The shared analyzer must declare its input revision: without one the
//! memo could not tell whether an index changed between two inspections, so
//! every group runs the analyzer itself.

use std::collections::HashMap;
use std::sync::{Arc, Mutex, PoisonError, Weak};

use swlsp_lsp::{BoxError, Context, Problem, TextDocument};
use swlsp_syntax::{Kind, SyntaxElement, SyntaxNodeExt, TextRange};

use crate::ProblemAnalyzer;

/// Documents whose results are kept; enough for the files diagnosed
/// concurrently.
const MEMO_CAPACITY: usize = 32;

/// A problem without its syntax element, which is not `Send`; the element is
/// recorded by range and kind and resolved again in the same document.
struct StoredProblem {
    id: swlsp_lsp::DiagnosticId,
    range: TextRange,
    element: Option<(TextRange, Kind)>,
    message: String,
    severity: Option<swlsp_lsp_protocol::DiagnosticSeverity>,
    source: String,
    tags: Vec<swlsp_lsp_protocol::DiagnosticTag>,
    related_information: Vec<swlsp_lsp_protocol::DiagnosticRelatedInformation>,
    payload: Option<serde_json::Value>,
    fixes: Vec<swlsp_lsp::BoundFix>,
}

struct Entry {
    document: Weak<TextDocument>,
    revision: u64,
    problems: Arc<[StoredProblem]>,
}

/// An analyzer run once per document snapshot and input revision.
pub struct SharedAnalysis<A> {
    analyzer: A,
    memo: Mutex<HashMap<String, Entry>>,
}

impl<A: ProblemAnalyzer> SharedAnalysis<A> {
    pub fn new(analyzer: A) -> Arc<Self> {
        Arc::new(SharedAnalysis {
            analyzer,
            memo: Mutex::new(HashMap::new()),
        })
    }

    pub fn analyzer(&self) -> &A {
        &self.analyzer
    }

    fn problems(
        &self,
        ctx: &Context,
        document: &Arc<TextDocument>,
    ) -> Result<Vec<Problem>, BoxError> {
        let Some(revision) = self.analyzer.input_revision(document) else {
            return self.analyzer.analyze(ctx, document);
        };
        let cached = self
            .memo
            .lock()
            .unwrap_or_else(PoisonError::into_inner)
            .get(&document.uri)
            .filter(|entry| {
                entry.revision == revision
                    && Weak::ptr_eq(&entry.document, &Arc::downgrade(document))
            })
            .map(|entry| entry.problems.clone());
        let stored = match cached {
            Some(stored) => stored,
            None => {
                let problems = self.analyzer.analyze(ctx, document)?;
                let stored: Arc<[StoredProblem]> = problems.into_iter().map(store).collect();
                if !ctx.is_cancelled() {
                    let mut memo = self.memo.lock().unwrap_or_else(PoisonError::into_inner);
                    if memo.len() >= MEMO_CAPACITY && !memo.contains_key(&document.uri) {
                        memo.retain(|_, entry| entry.document.strong_count() > 0);
                        if memo.len() >= MEMO_CAPACITY {
                            memo.clear();
                        }
                    }
                    memo.insert(
                        document.uri.clone(),
                        Entry {
                            document: Arc::downgrade(document),
                            revision,
                            problems: stored.clone(),
                        },
                    );
                }
                stored
            }
        };
        Ok(stored
            .iter()
            .map(|stored| restore(stored, document))
            .collect())
    }
}

fn store(problem: Problem) -> StoredProblem {
    StoredProblem {
        id: problem.id,
        range: problem.range,
        element: problem
            .element
            .map(|element| (element.text_range(), element.kind())),
        message: problem.message,
        severity: problem.severity,
        source: problem.source,
        tags: problem.tags,
        related_information: problem.related_information,
        payload: problem.payload,
        fixes: problem.fixes,
    }
}

fn restore(stored: &StoredProblem, document: &TextDocument) -> Problem {
    let element = stored.element.and_then(|(range, kind)| {
        let root = document.root()?;
        let covering = root.covering_element(range);
        let mut candidate = Some(covering);
        while let Some(element) = candidate {
            if element.text_range() == range && element.kind() == kind {
                return Some(element);
            }
            if element.text_range().len() > range.len() {
                break;
            }
            candidate = element.parent().map(SyntaxElement::Node);
        }
        // The smallest element covering the range, as for problems
        // reported without one.
        root.descendant_for_range(range)
    });
    Problem {
        id: stored.id.clone(),
        range: stored.range,
        element,
        message: stored.message.clone(),
        severity: stored.severity,
        source: stored.source.clone(),
        tags: stored.tags.clone(),
        related_information: stored.related_information.clone(),
        payload: stored.payload.clone(),
        fixes: stored.fixes.clone(),
    }
}

/// The problems of one inspection's codes from a [`SharedAnalysis`].
pub struct CodeGroup<A> {
    shared: Arc<SharedAnalysis<A>>,
    codes: &'static [&'static str],
}

impl<A> CodeGroup<A> {
    pub fn new(shared: Arc<SharedAnalysis<A>>, codes: &'static [&'static str]) -> Self {
        CodeGroup { shared, codes }
    }
}

impl<A: ProblemAnalyzer> ProblemAnalyzer for CodeGroup<A> {
    fn analyze(
        &self,
        ctx: &Context,
        document: &Arc<TextDocument>,
    ) -> Result<Vec<Problem>, BoxError> {
        Ok(self
            .shared
            .problems(ctx, document)?
            .into_iter()
            .filter(|problem| self.codes.contains(&problem.id.as_str()))
            .collect())
    }

    fn input_revision(&self, document: &TextDocument) -> Option<u64> {
        self.shared.analyzer.input_revision(document)
    }
}
