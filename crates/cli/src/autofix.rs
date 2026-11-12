//! Which findings of `check` have an automatic fix, and applying them.
//!
//! A finding is fixable when the server offers exactly one preferred quick
//! fix for it and that fix edits nothing but the checked file. Fixes that
//! need a decision (several alternatives), an editor command, or another
//! file stay with `codeaction`.
//!
//! Fixes are applied one at a time, diagnosing the file again after each:
//! fixes share side effects (two renamed references adding the same
//! import), which only sequential application gets right.

use std::collections::HashSet;

use serde_json::{Value, json};
use swlsp_lsp_protocol::{CodeAction, Diagnostic, Position, Range, TextEdit, WorkspaceEdit};

use crate::CliError;
use crate::edit::apply_protocol_text_edits;
use crate::only::Selection;
use crate::session::{CliDocument, CliSession, DiagnosticOutput};
use crate::workspace_commands::code_text;

const QUICKFIX: &str = "quickfix";

/// Fixes applied to one file before the rest is left to a second run.
const MAX_FIXES: usize = 1000;

/// What `check` does about fixes.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub(crate) enum FixMode {
    /// Fixes are not looked at.
    Ignore,
    /// Findings say whether they are fixable.
    Report,
    /// Fixable findings are fixed in the file; the rest is reported.
    Apply,
}

/// The outcome of checking one file.
#[derive(Debug, Default)]
pub(crate) struct CheckedFile {
    /// What is (still) wrong with the file.
    pub findings: Vec<DiagnosticOutput>,
    /// How many findings were fixed in the file.
    pub fixed: usize,
}

/// The result of one look at a file.
enum Round {
    /// A fix was written; the file has to be checked again.
    Fixed,
    Done(Vec<DiagnosticOutput>),
}

impl CliSession {
    /// Checks one file, keeping findings at or above `cutoff` (lower
    /// numbers are more severe).
    pub(crate) fn check_file(
        &self,
        path: &str,
        cutoff: i32,
        mode: FixMode,
        only: Option<&Selection>,
    ) -> Result<CheckedFile, CliError> {
        let mut fixed = 0;
        // Contents the file had: a fix leading back to one of them would
        // go round in circles.
        let mut seen = HashSet::new();
        loop {
            let round = self.with_document(path, |document| {
                let mut diagnostics = self.diagnostics(document, cutoff)?;
                if let Some(only) = only {
                    // Fixes are looked up for what is left, so the rest of
                    // the file stays as it is.
                    diagnostics.retain(|diagnostic| {
                        diagnostic
                            .code
                            .as_ref()
                            .is_some_and(|code| only.contains(&code_text(code)))
                    });
                }
                let fixes = match mode {
                    FixMode::Ignore => Vec::new(),
                    FixMode::Report | FixMode::Apply => {
                        self.automatic_fixes(document, &diagnostics)
                    }
                };
                if mode == FixMode::Apply && fixed < MAX_FIXES {
                    seen.insert(document.source.clone());
                    for edits in fixes.iter().flatten() {
                        if write_fix(document, edits, &seen)? {
                            return Ok(Round::Fixed);
                        }
                    }
                }
                let findings = diagnostics
                    .into_iter()
                    .enumerate()
                    .map(|(index, diagnostic)| DiagnosticOutput {
                        uri: document.uri.clone(),
                        related: Some(diagnostic.related_information.clone())
                            .filter(|items| !items.is_empty()),
                        // Whatever is left after fixing could not be fixed.
                        fixable: match mode {
                            FixMode::Ignore => None,
                            FixMode::Report => Some(fixes.get(index).is_some_and(Option::is_some)),
                            FixMode::Apply => Some(false),
                        },
                        diagnostic,
                    })
                    .collect();
                Ok(Round::Done(findings))
            })?;
            match round {
                Round::Fixed => fixed += 1,
                Round::Done(findings) => return Ok(CheckedFile { findings, fixed }),
            }
        }
    }

    /// Per diagnostic the edits of its automatic fix, if it has one.
    fn automatic_fixes(
        &self,
        document: &CliDocument,
        diagnostics: &[Diagnostic],
    ) -> Vec<Option<Vec<TextEdit>>> {
        // Only the server's inspections attach data, and only they bind
        // fixes to a diagnostic.
        let candidates: Vec<&Diagnostic> = diagnostics
            .iter()
            .filter(|diagnostic| diagnostic.data.as_ref().is_some_and(|data| !data.is_null()))
            .collect();
        if candidates.is_empty() {
            return Vec::new();
        }
        let params = json!({
            "textDocument": {"uri": document.uri},
            "range": covering_range(&candidates),
            "context": {"diagnostics": candidates, "only": [QUICKFIX]},
        });
        // A file whose fixes cannot be listed is still reported.
        let actions: Vec<CodeAction> = self
            .call::<Option<Vec<CodeAction>>>("textDocument/codeAction", params)
            .ok()
            .flatten()
            .unwrap_or_default();
        diagnostics
            .iter()
            .map(|diagnostic| {
                let mut offered = actions.iter().filter(|action| {
                    action.is_preferred
                        && action
                            .kind
                            .as_ref()
                            .is_some_and(|kind| kind.as_str() == QUICKFIX)
                        && action.diagnostics.as_slice() == std::slice::from_ref(diagnostic)
                });
                let action = offered.next()?;
                if offered.next().is_some() {
                    return None;
                }
                self.own_edits(document, action)
            })
            .collect()
    }

    /// The edits of `action` when it changes `document` and nothing else.
    fn own_edits(&self, document: &CliDocument, action: &CodeAction) -> Option<Vec<TextEdit>> {
        let resolved;
        let mut action = action;
        if action.edit.is_none() && action.command.is_none() && action.data.is_some() {
            resolved = self
                .call::<Option<CodeAction>>(
                    "codeAction/resolve",
                    serde_json::to_value(action).unwrap_or(Value::Null),
                )
                .ok()??;
            action = &resolved;
        }
        if action.disabled.is_some() || action.command.is_some() {
            return None;
        }
        document_edits(action.edit.as_ref()?, &document.uri)
    }
}

/// The text edits of `edit` when all of them are for `uri`.
fn document_edits(edit: &WorkspaceEdit, uri: &str) -> Option<Vec<TextEdit>> {
    let mut edits = Vec::new();
    for (target, changes) in &edit.changes {
        if target != uri {
            return None;
        }
        edits.extend(changes.iter().cloned());
    }
    for change in &edit.document_changes {
        // Without a text document the change creates, renames or deletes
        // a file.
        if change.text_document.as_ref()?.uri != uri {
            return None;
        }
        edits.extend(change.edits.iter().cloned());
    }
    (!edits.is_empty()).then_some(edits)
}

/// Writes `document` with `edits` applied. `false` when the fix is not
/// applied: it changes nothing, leads back to a content in `seen`, or the
/// file is not what the server analyzed.
fn write_fix(
    document: &CliDocument,
    edits: &[TextEdit],
    seen: &HashSet<String>,
) -> Result<bool, CliError> {
    let Ok(updated) = apply_protocol_text_edits(&document.source, edits) else {
        return Ok(false);
    };
    if seen.contains(&updated) {
        return Ok(false);
    }
    // Also false for a file that is not UTF-8: it was analyzed lossily.
    let unchanged =
        std::fs::read(&document.path).is_ok_and(|content| content == document.source.as_bytes());
    if !unchanged {
        return Ok(false);
    }
    std::fs::write(&document.path, updated)
        .map_err(|error| CliError::fail(format!("write {}: {error}", document.path)))?;
    Ok(true)
}

fn covering_range(diagnostics: &[&Diagnostic]) -> Range {
    let key = |position: &Position| (position.line, position.character);
    let start = diagnostics
        .iter()
        .map(|diagnostic| diagnostic.range.start)
        .min_by_key(key)
        .unwrap_or_default();
    let end = diagnostics
        .iter()
        .map(|diagnostic| diagnostic.range.end)
        .max_by_key(key)
        .unwrap_or_default();
    Range { start, end }
}
