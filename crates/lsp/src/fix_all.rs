//! "Fix all in file": one code action applying a quick fix to every problem
//! of the same code in a document.
//!
//! Fixes are applied one after another to an in-memory copy of the
//! document, re-running the inspection between steps, rather than merged
//! from independently built edits: fixes can share side effects (two
//! occurrences of a renamed type both adding its import, or each seeing the
//! other keep an old import in use), which only sequential application gets
//! right. The result is one validated edit of the original document.

use std::sync::Arc;

use serde::{Deserialize, Serialize};
use serde_json::Value;
use swlsp_lsp_protocol as protocol;
use swlsp_rewrite::{DocumentPlan, Edit, ElementHandle, WorkspacePlan};
use swlsp_syntax::{TextRange, TextSize};

use crate::context::{BoxError, Context};
use crate::diagnostics::protocol_range;
use crate::document::{TextDocument, diagnostic_element};
use crate::inspection::{
    DiagnosticId, FixContext, FixId, Problem, ProblemReporter, RegisteredInspection, code_string,
    decode_diagnostic_envelope,
};
use crate::server::Server;

pub(crate) const FIX_ALL_SCHEMA: i64 = 3;

/// The data of a fix-all code action, resolved lazily.
#[derive(Debug, Clone, Default, Serialize, Deserialize)]
#[serde(default)]
pub(crate) struct FixAllEnvelope {
    pub schema: i64,
    pub inspection: String,
    pub code: DiagnosticId,
    pub fix: FixId,
    pub uri: String,
    pub version: i32,
}

#[derive(Default)]
struct Collector(Vec<Problem>);

impl ProblemReporter for Collector {
    fn report(&mut self, problem: Problem) -> Result<(), BoxError> {
        self.0.push(problem);
        Ok(())
    }
}

impl Server {
    /// How many published diagnostics of `document` have `code` and offer
    /// `fix`.
    pub(crate) fn fix_all_candidates(
        &self,
        ctx: &Context,
        document: &Arc<TextDocument>,
        code: &DiagnosticId,
        fix: &FixId,
    ) -> usize {
        self.diagnostics_for_document(ctx, document)
            .iter()
            .filter(|diagnostic| {
                code_string(diagnostic.code.as_ref()).as_deref() == Some(code.0.as_str())
            })
            .filter_map(|diagnostic| decode_diagnostic_envelope(diagnostic.data.as_ref()).ok())
            .filter(|envelope| envelope.fixes.iter().any(|bound| &bound.id == fix))
            .count()
    }

    /// Applies `fix` to every problem with `code` in `document`, one at a
    /// time. `None` when no problem could be fixed.
    pub(crate) fn fix_all_plan(
        &self,
        ctx: &Context,
        registered: &RegisteredInspection,
        document: &Arc<TextDocument>,
        code: &DiagnosticId,
        fix_id: &FixId,
    ) -> Result<Option<WorkspacePlan>, BoxError> {
        let Some(fix) = registered.fixes.get(fix_id) else {
            return Ok(None);
        };
        let Some(rewrite) = fix.as_rewrite() else {
            return Ok(None);
        };
        let Some(language) = document.language else {
            return Ok(None);
        };
        let suppressions = self.registry().suppressions.get(&language).cloned();
        let mut current = document.clone();
        let mut remaining = usize::MAX;
        let mut applied = 0;
        loop {
            ctx.check()?;
            let suppressed = suppressions.as_ref().map(|read| read(&current));
            let mut collector = Collector::default();
            registered
                .inspection
                .inspect(ctx, &current, &mut collector)?;
            let candidates: Vec<Problem> = collector
                .0
                .into_iter()
                .filter(|problem| &problem.id == code)
                .filter(|problem| problem.fixes.iter().any(|bound| &bound.id == fix_id))
                .filter(|problem| {
                    !suppressed
                        .as_ref()
                        .is_some_and(|set| set.suppresses(problem.range.start(), &problem.id))
                })
                .collect();
            // Every step must fix a problem; a fix that leaves its problem
            // in place ends the run instead of looping.
            if candidates.is_empty() || candidates.len() >= remaining {
                break;
            }
            remaining = candidates.len();
            let Some(next) = self.apply_one(ctx, rewrite, &current, &candidates[0], fix_id)? else {
                break;
            };
            current = next;
            applied += 1;
        }
        if applied == 0 || current.source() == document.source() {
            return Ok(None);
        }
        let edit = difference(document.source(), current.source());
        Ok(Some(WorkspacePlan {
            documents: vec![
                DocumentPlan::new(
                    document.uri.clone(),
                    Some(document.version),
                    document.source_arc().clone(),
                    vec![edit],
                )
                .with_line_index(document.line_index_arc().clone()),
            ],
            ..WorkspacePlan::default()
        }))
    }

    /// `document` after fixing `problem`, as a new in-memory version; `None`
    /// when the fix edits other documents or cannot be built.
    fn apply_one(
        &self,
        ctx: &Context,
        rewrite: &dyn crate::inspection::RewriteQuickFix,
        document: &Arc<TextDocument>,
        problem: &Problem,
        fix_id: &FixId,
    ) -> Result<Option<Arc<TextDocument>>, BoxError> {
        let (Some(language), Some(root)) = (document.language, document.root()) else {
            return Ok(None);
        };
        let Some(bound) = problem.fixes.iter().find(|bound| &bound.id == fix_id) else {
            return Ok(None);
        };
        let Some(element) = problem
            .element
            .clone()
            .or_else(|| diagnostic_element(&root, problem.range))
        else {
            return Ok(None);
        };
        let context = FixContext {
            document: document.clone(),
            diagnostic: protocol::Diagnostic {
                range: protocol_range(document.line_index(), problem.range),
                code: Some(Value::String(problem.id.0.clone())),
                message: problem.message.clone(),
                ..protocol::Diagnostic::default()
            },
            anchor: ElementHandle::new(document.uri.clone(), document.version, language, element),
            problem_payload: problem.payload.clone(),
            fix_payload: bound.payload.clone(),
            documents: self.document_resolver(),
        };
        let Ok(plan) = rewrite.build(ctx, &context) else {
            return Ok(None);
        };
        let [only] = plan.documents.as_slice() else {
            return Ok(None);
        };
        if only.uri != document.uri || !plan.creates.is_empty() {
            return Ok(None);
        }
        let updated = only.apply()?;
        Ok(Some(Arc::new(TextDocument::new(
            document.uri.clone(),
            &updated,
            document.version + 1,
        ))))
    }
}

/// One edit turning `source` into `result`: the differing middle.
fn difference(source: &str, result: &str) -> Edit {
    let mut prefix = source
        .bytes()
        .zip(result.bytes())
        .take_while(|(a, b)| a == b)
        .count();
    while !source.is_char_boundary(prefix) || !result.is_char_boundary(prefix) {
        prefix -= 1;
    }
    let max_suffix = (source.len() - prefix).min(result.len() - prefix);
    let mut suffix = source
        .bytes()
        .rev()
        .zip(result.bytes().rev())
        .take(max_suffix)
        .take_while(|(a, b)| a == b)
        .count();
    while !source.is_char_boundary(source.len() - suffix)
        || !result.is_char_boundary(result.len() - suffix)
    {
        suffix -= 1;
    }
    Edit::new(
        TextRange::new(
            TextSize::from(prefix as u32),
            TextSize::from((source.len() - suffix) as u32),
        ),
        &result[prefix..result.len() - suffix],
    )
}
