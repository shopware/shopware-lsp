//! Formatting a part of a document: the lines of the formatted document
//! that replace the selected ones.
//!
//! The whole document is formatted and compared with the source line by
//! line. Only the changes that touch the selection are applied. The result
//! is kept when formatting it again gives the formatted document: it is
//! then the same code, with the rest of its lines as they were. A change
//! that needs a second one outside the selection, like the braces put
//! around a body, fails that check, and the selection grows to the
//! construct around it until the check holds.

use std::ops::Range;

use similar::{Algorithm, DiffTag, capture_diff_slices};
use swlsp_syntax::{SyntaxNode, TextRange, TextSize};

use crate::{Options, Skipped, try_format, try_format_tree};

/// How many ever larger selections are tried before the whole document is
/// formatted.
const ATTEMPTS: usize = 8;

/// One change of the line comparison: the bytes of the source it replaces
/// and the text that replaces them.
struct Change<'a> {
    old: Range<usize>,
    new: &'a str,
}

pub(crate) fn run(
    source: &str,
    root: &SyntaxNode,
    has_errors: bool,
    range: Range<usize>,
    options: Options,
) -> Result<String, Skipped> {
    let formatted = try_format_tree(source, root, has_errors, options)?;
    if formatted == source {
        return Ok(formatted);
    }
    let changes = changes(source, &formatted);
    let mut tried: Option<Vec<usize>> = None;
    let mut attempts = 0;
    for region in regions(source, root, range) {
        let selected: Vec<usize> = (0..changes.len())
            .filter(|&index| touches(&changes[index].old, &region))
            .collect();
        if tried.as_ref() == Some(&selected) {
            continue;
        }
        if selected.len() == changes.len() || attempts == ATTEMPTS {
            break;
        }
        attempts += 1;
        let partial = applied(source, &changes, &selected);
        if selected.is_empty() || try_format(&partial, options).ok().as_ref() == Some(&formatted) {
            return Ok(partial);
        }
        tried = Some(selected);
    }
    Ok(formatted)
}

/// The changes that turn `source` into `formatted`, whole lines each.
/// Lines are matched by what they hold apart from white space, so a line
/// that only has its spaces set is a change of its own.
fn changes<'a>(source: &str, formatted: &'a str) -> Vec<Change<'a>> {
    let (old_lines, new_lines) = (line_starts(source), line_starts(formatted));
    let old_keys = keys(source, &old_lines);
    let new_keys = keys(formatted, &new_lines);
    let mut found = Vec::new();
    for op in capture_diff_slices(Algorithm::Myers, &old_keys, &new_keys) {
        let (tag, old, new) = op.as_tag_tuple();
        if tag != DiffTag::Equal {
            found.push(Change {
                old: old_lines[old.start]..old_lines[old.end],
                new: &formatted[new_lines[new.start]..new_lines[new.end]],
            });
            continue;
        }
        for (old, new) in old.zip(new) {
            let before = old_lines[old]..old_lines[old + 1];
            let after = &formatted[new_lines[new]..new_lines[new + 1]];
            if &source[before.clone()] != after {
                found.push(Change {
                    old: before,
                    new: after,
                });
            }
        }
    }
    found
}

/// Each line without its white space.
fn keys(text: &str, starts: &[usize]) -> Vec<String> {
    starts
        .windows(2)
        .map(|line| {
            text[line[0]..line[1]]
                .chars()
                .filter(|character| !character.is_whitespace())
                .collect()
        })
        .collect()
}

/// The offset each line starts at, and the end of the text after the last.
fn line_starts(text: &str) -> Vec<usize> {
    let mut starts = vec![0];
    starts.extend(text.match_indices('\n').map(|(at, _)| at + 1));
    if starts.last() != Some(&text.len()) {
        starts.push(text.len());
    }
    starts
}

/// Whether a change belongs to the region. Lines put in where the source
/// has none belong to it when they are put inside it or at one of its ends:
/// the brace that closes a body follows the last line of the body.
fn touches(change: &Range<usize>, region: &Range<usize>) -> bool {
    if change.is_empty() {
        return region.start <= change.start && change.start <= region.end;
    }
    change.start < region.end && region.start < change.end
}

fn applied(source: &str, changes: &[Change<'_>], selected: &[usize]) -> String {
    let mut out = String::with_capacity(source.len());
    let mut end = 0;
    for &index in selected {
        let change = &changes[index];
        out.push_str(&source[end..change.old.start]);
        out.push_str(change.new);
        end = change.old.end;
    }
    out.push_str(&source[end..]);
    out
}

/// The selection as whole lines, then the lines of each construct around
/// it, from the innermost to the document.
fn regions(source: &str, root: &SyntaxNode, range: Range<usize>) -> Vec<Range<usize>> {
    let end = boundary(source, range.end);
    let start = boundary(source, range.start.min(end));
    let mut found = vec![lines(source, start..end)];
    let (Ok(from), Ok(to)) = (u32::try_from(start), u32::try_from(end)) else {
        return found;
    };
    let covered = TextRange::new(TextSize::from(from), TextSize::from(to));
    if !root.text_range().contains_range(covered) {
        return found;
    }
    let element = root.covering_element(covered);
    let node = element.as_node().cloned().or_else(|| element.parent());
    for ancestor in node.iter().flat_map(SyntaxNode::ancestors) {
        // White space around the construct is not part of it.
        let range = ancestor.text_range();
        let text = &source[usize::from(range.start())..usize::from(range.end())];
        let start = usize::from(range.start()) + (text.len() - text.trim_start().len());
        let region = lines(source, start..start + text.trim().len());
        if found.last() != Some(&region) {
            found.push(region);
        }
    }
    found
}

/// The range grown to the start of its first line and the end of its last.
fn lines(source: &str, range: Range<usize>) -> Range<usize> {
    let start = source[..range.start].rfind('\n').map_or(0, |at| at + 1);
    // A selection that ends at the start of a line does not hold that line.
    let last = if range.end > range.start && source[..range.end].ends_with('\n') {
        range.end - 1
    } else {
        range.end
    };
    let end = source[last..]
        .find('\n')
        .map_or(source.len(), |at| last + at + 1);
    start..end.max(start)
}

/// The offset, or the nearest one before it that does not split a character.
fn boundary(source: &str, offset: usize) -> usize {
    let mut offset = offset.min(source.len());
    while !source.is_char_boundary(offset) {
        offset -= 1;
    }
    offset
}
