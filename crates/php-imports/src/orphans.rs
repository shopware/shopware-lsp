//! Removing imports a rewrite leaves unused.
//!
//! A fix that replaces `CartPersisterInterface` with `AbstractCartPersister`
//! imports the new class but would leave `use …\CartPersisterInterface;`
//! behind, now unused. [`remove_orphaned_imports`] extends such a rewrite
//! with the removal of exactly those imports: used before the rewrite and
//! unused after it. Imports that were already unused stay; that is the
//! unused-import inspection's business, not the fix's.

use rustc_hash::FxHashSet;
use swlsp_php_binder::Binder;
use swlsp_php_resolver::ImportKind;
use swlsp_rewrite::{Builder, Edit, RewriteError};
use swlsp_syntax::{TextRange, TextSize};
use swlsp_textutil::lowercase;

use crate::analyze::{Analysis, Item, analyze};
use crate::rewrite::remove;

/// `edits` (against `source`) plus the removal of every import they orphan,
/// as edits against `source`. Returns `edits` unchanged when nothing is
/// orphaned or either version of the source does not parse cleanly.
pub fn remove_orphaned_imports(source: &str, edits: Vec<Edit>) -> Result<Vec<Edit>, RewriteError> {
    if edits.is_empty() {
        return Ok(edits);
    }
    let Some(before) = import_analysis(source) else {
        return Ok(edits);
    };
    let used_before: FxHashSet<(ImportKind, String)> = before
        .scopes
        .iter()
        .filter(|scope| !scope.unsafe_scope)
        .flat_map(|scope| &scope.declarations)
        .flat_map(|declaration| &declaration.items)
        .filter(|item| item.used)
        .map(key)
        .collect();
    let updated = swlsp_rewrite::apply(source, &edits)?;
    let Some(after) = import_analysis(&updated) else {
        return Ok(edits);
    };
    let mut removals = Vec::new();
    for scope in after.scopes.iter().filter(|scope| !scope.unsafe_scope) {
        for declaration in &scope.declarations {
            removals.extend(remove(&updated, declaration, |item| {
                !item.used && used_before.contains(&key(item))
            })?);
        }
    }
    if removals.is_empty() {
        return Ok(edits);
    }
    if let Some(combined) = combine(source, &edits, &removals) {
        return Ok(combined);
    }
    // A removal touches text the rewrite produced: replace the changed
    // region as a whole instead.
    let result = swlsp_rewrite::apply(&updated, &removals)?;
    Ok(vec![difference(source, &result)])
}

fn key(item: &Item) -> (ImportKind, String) {
    (
        item.import.kind,
        lowercase(item.import.target.trim_start_matches('\\')),
    )
}

/// The import analysis of `source`, or `None` when it has syntax errors.
fn import_analysis(source: &str) -> Option<Analysis> {
    let parsed = swlsp_parser_php::parse(source);
    if !parsed.errors.is_empty() {
        return None;
    }
    let document = Binder::new().bind("", 0, &parsed.tree);
    analyze(&|| false, &parsed.root(), &document).ok()
}

/// Maps `removals` (against the rewritten source) back onto `source` and
/// merges them with `edits`; `None` when a removal overlaps text an edit
/// inserted.
fn combine(source: &str, edits: &[Edit], removals: &[Edit]) -> Option<Vec<Edit>> {
    let mut builder = Builder::new(source);
    for edit in edits {
        builder
            .replace_range(edit.range, edit.new_text.clone())
            .ok()?;
    }
    for removal in removals {
        let start = original_offset(edits, u32::from(removal.range.start()), false)?;
        let end = original_offset(edits, u32::from(removal.range.end()), true)?;
        builder
            .replace_range(
                TextRange::new(TextSize::from(start), TextSize::from(end)),
                removal.new_text.clone(),
            )
            .ok()?;
    }
    builder.finish().ok()
}

/// The `source` offset of rewritten offset `offset`, or `None` inside text an
/// edit produced. An offset at the boundary of an edit maps to the side the
/// removal extends from: its start to the edit's end, its end to the edit's
/// start.
fn original_offset(edits: &[Edit], offset: u32, is_end: bool) -> Option<u32> {
    let mut delta: i64 = 0;
    for edit in edits {
        let start = i64::from(u32::from(edit.range.start())) + delta;
        let inserted = i64::try_from(edit.new_text.len()).ok()?;
        let end = start + inserted;
        let offset_i = i64::from(offset);
        if offset_i < start || (offset_i == start && is_end) {
            break;
        }
        if offset_i < end || (offset_i == end && is_end && inserted > 0) {
            return None;
        }
        delta += inserted - i64::from(u32::from(edit.range.len()));
    }
    u32::try_from(i64::from(offset) - delta).ok()
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
    let range = TextRange::new(
        TextSize::from(prefix as u32),
        TextSize::from((source.len() - suffix) as u32),
    );
    Edit::new(range, &result[prefix..result.len() - suffix])
}
