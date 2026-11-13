//! A fix that replaces the reported source range with text the analyzer
//! computed.
//!
//! The analyzer puts `title`, `expected` (the source it saw in the range)
//! and `replacement` in the problem payload. The fix refuses to run when the
//! range no longer holds `expected`.

use serde::{Deserialize, Serialize};
use serde_json::{Map, Value};
use swlsp_lsp::{
    BoundFix, BoxError, Context, FixContext, FixId, FixPresentation, FixResolution, QuickFix,
    RewriteQuickFix, bind_fix,
};
use swlsp_lsp_protocol as protocol;
use swlsp_rewrite::{Builder, DocumentPlan, HandleError, WorkspacePlan};
use swlsp_syntax::{TextRange, TextSize};

use crate::inspections::inspection::resolve_anchor;
use crate::inspections::range::protocol_text_range;

pub const REWRITE_NODE_FIX_ID: &str = "rewrite-reported-source";

#[derive(Debug, Clone, Default, Serialize, Deserialize)]
#[serde(rename_all = "camelCase", default)]
struct RewriteNodePayload {
    title: String,
    expected: String,
    replacement: String,
    /// Whether the rewrite keeps the meaning of the code. Only such a fix
    /// is the preferred one, which `check --fix` applies on its own.
    equivalent: bool,
    /// With an empty replacement, removes the whole line when nothing else
    /// is on it.
    remove_line: bool,
}

/// Replaces the diagnostic range with the payload's replacement.
#[derive(Debug, Clone, Copy, Default)]
pub struct RewriteNodeFix;

impl QuickFix for RewriteNodeFix {
    fn id(&self) -> FixId {
        FixId::from(REWRITE_NODE_FIX_ID)
    }

    fn present(
        &self,
        _ctx: &Context,
        fix: &FixContext,
    ) -> Result<Option<FixPresentation>, BoxError> {
        let payload: RewriteNodePayload = fix.fix_payload()?;
        if payload.title.is_empty() || payload.expected.is_empty() {
            return Ok(None);
        }
        Ok(Some(FixPresentation {
            title: payload.title,
            kind: Some(protocol::CodeActionKind::QUICKFIX),
            preferred: payload.equivalent,
            resolution: FixResolution::Eager,
        }))
    }

    fn as_rewrite(&self) -> Option<&dyn RewriteQuickFix> {
        Some(self)
    }
}

impl RewriteQuickFix for RewriteNodeFix {
    fn build(&self, _ctx: &Context, fix: &FixContext) -> Result<WorkspacePlan, BoxError> {
        let payload: RewriteNodePayload = fix.fix_payload()?;
        resolve_anchor(fix)?;
        let document = &fix.document;
        let source = document.source();
        let mut range = protocol_text_range(document.line_index(), fix.diagnostic.range)?;
        let (start, end) = (usize::from(range.start()), usize::from(range.end()));
        if source.get(start..end) != Some(payload.expected.as_str()) {
            return Err(Box::new(HandleError::Stale));
        }
        if payload.remove_line && payload.replacement.is_empty() {
            range = whole_line(source, start, end);
        }
        let mut builder = Builder::new(source);
        builder.replace_range(range, payload.replacement)?;
        let edits = builder.finish()?;
        Ok(WorkspacePlan {
            documents: vec![
                DocumentPlan::new(
                    document.uri.clone(),
                    Some(document.version),
                    document.source_arc().clone(),
                    edits,
                )
                .with_line_index(document.line_index_arc().clone()),
            ],
            ..WorkspacePlan::default()
        })
    }
}

/// `start..end` widened to its line and line break when nothing else shares
/// the line.
fn whole_line(source: &str, start: usize, end: usize) -> TextRange {
    let range = |start: usize, end: usize| {
        TextRange::new(TextSize::from(start as u32), TextSize::from(end as u32))
    };
    let line_start = source[..start].rfind('\n').map_or(0, |index| index + 1);
    let line_end = source[end..]
        .find('\n')
        .map_or(source.len(), |index| end + index);
    let blank = |text: &str| {
        text.bytes()
            .all(|byte| matches!(byte, b' ' | b'\t' | b'\r'))
    };
    if !blank(&source[line_start..start]) || !blank(&source[end..line_end]) {
        return range(start, end);
    }
    range(line_start, (line_end + 1).min(source.len()))
}

/// The rewrite of a problem whose payload carries one.
pub fn rewrite_node_bound_fixes(payload: &Map<String, Value>) -> Vec<BoundFix> {
    let field = |name: &str| match payload.get(name) {
        Some(Value::String(value)) => value.clone(),
        _ => String::new(),
    };
    let flag = |name: &str| payload.get(name) == Some(&Value::Bool(true));
    let value = RewriteNodePayload {
        title: field("title"),
        expected: field("expected"),
        replacement: field("replacement"),
        equivalent: flag("equivalent"),
        remove_line: flag("removeLine"),
    };
    if value.title.is_empty() || value.expected.is_empty() {
        return Vec::new();
    }
    vec![bind_fix(REWRITE_NODE_FIX_ID, value)]
}
