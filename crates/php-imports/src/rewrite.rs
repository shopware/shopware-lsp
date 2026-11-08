//! Import removal and organization (port of `imports/rewrite.go`).

use swlsp_parser_php::syntax::{TK_BLOCK_COMMENT, TK_LINE_COMMENT};
use swlsp_php_resolver::{Import, ImportKind};
use swlsp_php_types::go_to_lower;
use swlsp_rewrite::{Builder, Edit, RewriteError};
use swlsp_syntax::{SyntaxNodeExt, TextRange, TextSize};

use crate::OrganizeError;
use crate::analyze::{Analysis, Declaration, Item};

fn range(start: u32, end: u32) -> TextRange {
    TextRange::new(TextSize::from(start), TextSize::from(end))
}

fn start(value: TextRange) -> u32 {
    u32::from(value.start())
}

fn end(value: TextRange) -> u32 {
    u32::from(value.end())
}

/// Removes selected clauses while preserving every source comment. Returns
/// no edits when nothing is selected.
pub fn remove(
    source: &str,
    declaration: &Declaration,
    selected: impl Fn(&Item) -> bool,
) -> Result<Vec<Edit>, RewriteError> {
    let items = &declaration.items;
    let kept = items.iter().filter(|item| !selected(item)).count();
    if kept == items.len() {
        return Ok(Vec::new());
    }
    let mut ranges = Vec::new();
    if kept == 0 {
        ranges.push(declaration.node.range_trimmed_trivia());
    } else {
        let mut index = 0;
        while index < items.len() {
            if !selected(&items[index]) {
                index += 1;
                continue;
            }
            let mut last = index + 1;
            while last < items.len() && selected(&items[last]) {
                last += 1;
            }
            let mut removed = range(start(items[index].range), end(items[last - 1].range));
            if last < items.len() {
                removed = range(start(removed), start(items[last].range));
            } else if index > 0 {
                removed = range(start(declaration.commas[index - 1]), end(removed));
            }
            ranges.push(removed);
            index = last;
        }
    }
    let bytes = source.as_bytes();
    // Keep the line ending with line comments so retained PHP is never
    // swallowed by a comment when adjacent clauses are removed.
    let comments: Vec<TextRange> = comment_ranges(declaration)
        .into_iter()
        .map(|comment| {
            let text = &source[comment];
            let mut comment_end = end(comment) as usize;
            if text.starts_with("//") || text.starts_with('#') {
                if comment_end < bytes.len() && bytes[comment_end] == b'\r' {
                    comment_end += 1;
                }
                if comment_end < bytes.len() && bytes[comment_end] == b'\n' {
                    comment_end += 1;
                }
            }
            range(start(comment), comment_end as u32)
        })
        .collect();
    let mut builder = Builder::new(source);
    for mut removed in ranges {
        if kept == 0 && comments.is_empty() {
            removed = whole_line(source, removed);
        }
        let mut from = start(removed);
        for comment in &comments {
            if end(*comment) <= from || start(*comment) >= end(removed) {
                continue;
            }
            builder.replace_range(range(from, start(*comment)), "")?;
            from = end(*comment);
        }
        builder.replace_range(range(from, end(removed)), "")?;
    }
    builder.finish()
}

/// Removes unused imports and orders independent, comment-free runs.
/// Comments form ordering boundaries. Grouped imports without comments expand
/// to one import per line, with classes, functions, and constants ordered
/// apart.
pub fn organize(
    is_cancelled: &dyn Fn() -> bool,
    source: &str,
    analysis: &Analysis,
) -> Result<Vec<Edit>, OrganizeError> {
    let mut builder = Builder::new(source);
    for scope in &analysis.scopes {
        if scope.unsafe_scope {
            continue;
        }
        let declarations = &scope.declarations;
        let mut index = 0;
        while index < declarations.len() {
            if is_cancelled() {
                return Err(OrganizeError::Cancelled);
            }
            let first = &declarations[index];
            if !comment_ranges(first).is_empty() {
                for edit in remove(source, first, |item| !item.used)? {
                    builder.replace_range(edit.range, edit.new_text)?;
                }
                index += 1;
                continue;
            }
            let mut last = index + 1;
            while last < declarations.len() {
                let next = &declarations[last];
                let gap = &source[range(
                    end(declarations[last - 1].node.range_trimmed_trivia()),
                    start(next.node.range_trimmed_trivia()),
                )];
                if !gap.trim().is_empty() || !comment_ranges(next).is_empty() {
                    break;
                }
                last += 1;
            }
            let mut run = range(
                start(first.node.range_trimmed_trivia()),
                end(declarations[last - 1].node.range_trimmed_trivia()),
            );
            let replacement = organized_text(source, run, &declarations[index..last]);
            if replacement.is_empty() {
                run = whole_line(source, run);
            }
            if source[run] != replacement {
                builder.replace_range(run, replacement)?;
            }
            index = last;
        }
    }
    Ok(builder.finish()?)
}

fn import_text(item: &Import) -> String {
    let mut text = String::from("use ");
    match item.kind {
        ImportKind::Function => text.push_str("function "),
        ImportKind::Constant => text.push_str("const "),
        ImportKind::Class => {}
    }
    text.push_str(&item.target);
    let short = item
        .target
        .rfind('\\')
        .map_or(item.target.as_str(), |separator| {
            &item.target[separator + 1..]
        });
    if short != item.alias {
        text.push_str(" as ");
        text.push_str(&item.alias);
    }
    text.push(';');
    text
}

fn comment_ranges(declaration: &Declaration) -> Vec<TextRange> {
    let trimmed = declaration.node.range_trimmed_trivia();
    declaration
        .node
        .children_with_tokens()
        .filter_map(|element| element.into_token())
        .filter(|token| token.text_range().start() >= trimmed.start())
        .filter(|token| matches!(token.kind(), TK_LINE_COMMENT | TK_BLOCK_COMMENT))
        .map(|token| token.text_range())
        .collect()
}

fn whole_line(source: &str, value: TextRange) -> TextRange {
    let bytes = source.as_bytes();
    let line_start = source[..start(value) as usize]
        .rfind('\n')
        .map_or(0, |index| index + 1);
    let mut line_end = end(value) as usize;
    while line_end < bytes.len() && bytes[line_end] != b'\n' {
        line_end += 1;
    }
    if !source[line_start..start(value) as usize].trim().is_empty()
        || !source[end(value) as usize..line_end].trim().is_empty()
    {
        return value;
    }
    if line_end < bytes.len() {
        line_end += 1;
    }
    range(line_start as u32, line_end as u32)
}

fn organized_text(source: &str, run: TextRange, declarations: &[Declaration]) -> String {
    let mut items: Vec<&Item> = declarations
        .iter()
        .flat_map(|declaration| &declaration.items)
        .filter(|item| item.used)
        .collect();
    items.sort_by(|left, right| {
        (left.import.kind as u8)
            .cmp(&(right.import.kind as u8))
            .then_with(|| go_to_lower(&left.import.target).cmp(&go_to_lower(&right.import.target)))
            .then_with(|| left.import.alias.cmp(&right.import.alias))
    });
    let newline = if source.contains("\r\n") {
        "\r\n"
    } else {
        "\n"
    };
    let line_start = source[..start(run) as usize]
        .rfind('\n')
        .map_or(0, |index| index + 1);
    let mut indent = &source[line_start..start(run) as usize];
    if !indent.trim().is_empty() {
        indent = "";
    }
    let separator = format!("{newline}{indent}");
    items
        .iter()
        .map(|item| import_text(&item.import))
        .collect::<Vec<_>>()
        .join(&separator)
}
