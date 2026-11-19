//! Lines longer than the configured width (80 characters, as PER Coding
//! Style recommends, or its soft limit of 120) are split where a
//! split is at hand: a list goes one item per line, a method chain one
//! call per line, an expression one operand per line. A line that offers
//! none of these, as one long string does, stays as it is: the limit is
//! not a hard one.

use swlsp_parser_php::syntax::*;
use swlsp_syntax::SyntaxNode;

use crate::tokens::{Doc, Gap};
use crate::{Options, emit, layout};

/// The rounds of splitting one document gets. Each round splits at least
/// one more place, so this bounds deeply nested long lines.
const ROUNDS: usize = 64;

/// Lays out and writes the document, splitting lines that are too long.
pub(crate) fn run(doc: &mut Doc, root: &SyntaxNode, options: Options) -> String {
    let mut round = 0;
    loop {
        layout::run(doc, root, options);
        let written = emit::emit(doc);
        round += 1;
        if round == ROUNDS || !shorten(doc, &written, options.line_width.max(40)) {
            return written.text;
        }
    }
}

/// Marks one more place to split for every line that is too long. Returns
/// whether there was any.
fn shorten(doc: &mut Doc, written: &emit::Text, width: usize) -> bool {
    let lines = &written.lines;
    let mut changed = false;
    let mut index = 0;
    let mut start = 0;
    for (number, line) in written.text.lines().enumerate() {
        let long = line.len() > width && line.chars().count() > width;
        if long {
            while index < lines.len() && lines[index] < number {
                index += 1;
            }
            let tokens: Vec<usize> = (index..lines.len())
                .take_while(|&at| lines[at] == number)
                .filter(|&at| !doc.toks[at].skip && !doc.is_comment(at))
                .collect();
            let indent = line.len() - line.trim_start().len();
            let fits = |at: usize| {
                indent + INDENT_WIDTH + line[written.offsets[at] - start..].chars().count() <= width
            };
            changed |= split(doc, &tokens, fits);
        }
        start += line.len() + 1;
    }
    changed
}

const INDENT_WIDTH: usize = 4;

/// A place a line can be split at.
enum Split {
    /// A list goes one item per line.
    List(usize),
    /// The line is broken before each of these tokens.
    Before(Vec<usize>),
}

/// Splits the line at its outermost place: the one least deep in the tree,
/// so that a ternary is split before the arguments of a call inside it.
fn split(doc: &mut Doc, tokens: &[usize], fits: impl Fn(usize) -> bool) -> bool {
    let places = [
        list(doc, tokens),
        chain(doc, tokens),
        operators(doc, tokens),
        ternary(doc, tokens),
        match_conditions(doc, tokens),
        arrow(doc, tokens),
        interfaces(doc, tokens),
    ];
    let outermost = places
        .into_iter()
        .flatten()
        .min_by_key(|(depth, _)| *depth)
        .map(|(_, split)| split)
        // A value goes to the line after its `=` when nothing else can be
        // split and it fits there.
        .or_else(|| assignment(doc, tokens, &fits))
        .or_else(|| item_value(doc, tokens, &fits));
    match outermost {
        Some(Split::List(open)) => doc.split[open] = true,
        Some(Split::Before(breaks)) => {
            for at in breaks {
                break_before(doc, at);
            }
        }
        None => return false,
    }
    true
}

/// How deep in the tree the node of a token is.
fn depth(doc: &Doc, at: usize) -> usize {
    doc.toks[at].parent.ancestors().count()
}

fn written_on_line(doc: &Doc, at: usize) -> bool {
    !matches!(doc.gaps[at], Gap::Break(..)) && !doc.breaks[at]
}

/// The list that holds most of the line.
fn list(doc: &Doc, tokens: &[usize]) -> Option<(usize, Split)> {
    let open = tokens
        .iter()
        .copied()
        .filter(|&at| doc.flat[at])
        .max_by_key(|&at| (doc.pair[at] - at, std::cmp::Reverse(at)))?;
    Some((depth(doc, open), Split::List(open)))
}

/// A chain of two or more calls: one call per line.
fn chain(doc: &Doc, tokens: &[usize]) -> Option<(usize, Split)> {
    let calls: Vec<usize> = tokens
        .iter()
        .copied()
        .filter(|&at| {
            let kind = doc.kind(at);
            (kind == TK_OBJECT_OPERATOR || kind == TK_NULLSAFE_OBJECT_OPERATOR)
                && doc.parent_kind(at) == PHP_MEMBER_CALL
                && written_on_line(doc, at)
        })
        .collect();
    let outermost = calls.iter().map(|&at| depth(doc, at)).min()?;
    (calls.len() >= 2).then_some((outermost, Split::Before(calls)))
}

/// How loosely an operator binds: the loosest on a line is split first.
fn looseness(operator: &str) -> Option<u8> {
    Some(match operator.to_ascii_lowercase().as_str() {
        "||" | "or" | "xor" => 0,
        "&&" | "and" => 1,
        "??" => 2,
        "." => 3,
        "|" => 4,
        "===" | "!==" | "==" | "!=" | "<>" | "<" | ">" | "<=" | ">=" | "<=>" | "instanceof" => 5,
        "+" | "-" | "*" | "/" | "%" => 6,
        _ => return None,
    })
}

/// The expression an operator belongs to: the outermost of the binary
/// expressions nested in each other.
fn expression_depth(doc: &Doc, at: usize) -> usize {
    doc.toks[at]
        .parent
        .ancestors()
        .filter(|node| node.kind() != PHP_BINARY_EXPRESSION)
        .count()
        + 1
}

/// The loosest operators of the outermost expression: one operand per line.
fn operators(doc: &Doc, tokens: &[usize]) -> Option<(usize, Split)> {
    let found: Vec<(usize, usize, u8)> = tokens
        .iter()
        .copied()
        .filter(|&at| doc.parent_kind(at) == PHP_BINARY_EXPRESSION && written_on_line(doc, at))
        .filter_map(|at| {
            Some((
                at,
                expression_depth(doc, at),
                looseness(doc.source_text(at))?,
            ))
        })
        .collect();
    let outermost = found.iter().map(|(_, depth, _)| *depth).min()?;
    let loosest = found
        .iter()
        .filter(|(_, depth, _)| *depth == outermost)
        .map(|(_, _, looseness)| *looseness)
        .min()?;
    let breaks = found
        .into_iter()
        .filter(|(_, depth, looseness)| *depth == outermost && *looseness == loosest)
        .map(|(at, _, _)| at)
        .collect();
    Some((outermost, Split::Before(breaks)))
}

/// A ternary: its `?` and its `:` start a line each.
fn ternary(doc: &Doc, tokens: &[usize]) -> Option<(usize, Split)> {
    let found: Vec<usize> = tokens
        .iter()
        .copied()
        .filter(|&at| {
            let kind = doc.kind(at);
            (kind == TK_QUESTION || kind == TK_COLON)
                && doc.parent_kind(at) == PHP_TERNARY_EXPRESSION
                && written_on_line(doc, at)
                // The `:` of `?:` stays with its `?`.
                && !(kind == TK_COLON && at > 0 && doc.kind(at - 1) == TK_QUESTION)
        })
        .collect();
    let outermost = found.iter().map(|&at| depth(doc, at)).min()?;
    let breaks = found
        .into_iter()
        .filter(|&at| depth(doc, at) == outermost)
        .collect();
    Some((outermost, Split::Before(breaks)))
}

/// The conditions of a `match` arm: one per line.
fn match_conditions(doc: &Doc, tokens: &[usize]) -> Option<(usize, Split)> {
    let conditions: Vec<usize> = tokens
        .iter()
        .copied()
        .filter(|&at| doc.kind(at) == TK_COMMA && doc.parent_kind(at) == PHP_MATCH_ARM)
        .filter_map(|comma| doc.next_sig(comma))
        .filter(|&at| written_on_line(doc, at))
        .collect();
    let outermost = conditions.iter().map(|&at| depth(doc, at)).min()?;
    Some((outermost, Split::Before(conditions)))
}

/// The expression of a short closure, which goes to the next line.
fn arrow(doc: &Doc, tokens: &[usize]) -> Option<(usize, Split)> {
    let arrow = tokens.iter().copied().find(|&at| {
        doc.kind(at) == TK_ARROW
            && doc.parent_kind(at) == PHP_ARROW_FUNCTION
            && written_on_line(doc, at)
    })?;
    Some((depth(doc, arrow), Split::Before(vec![arrow])))
}

/// The value after an `=`, when it fits on a line of its own.
fn assignment(doc: &Doc, tokens: &[usize], fits: &impl Fn(usize) -> bool) -> Option<Split> {
    tokens.iter().copied().find_map(|at| {
        let assigns = doc.kind(at) == TK_EQUALS
            && [
                PHP_ASSIGNMENT_EXPRESSION,
                PHP_CLASS_CONST_DECLARATION,
                PHP_CONST_DECLARATION,
                PHP_PROPERTY_DECLARATION,
            ]
            .contains(&doc.parent_kind(at));
        let value = doc.next_sig(at).filter(|_| assigns)?;
        let heredoc = doc.source_text(value).starts_with("<<<");
        (tokens.contains(&value) && written_on_line(doc, value) && !heredoc && fits(value))
            .then(|| Split::Before(vec![value]))
    })
}

/// The interfaces of a class: one per line, the first on the line after
/// `implements`.
fn interfaces(doc: &Doc, tokens: &[usize]) -> Option<(usize, Split)> {
    let clause = tokens.iter().find_map(|&at| {
        let clause = doc.toks[at].parent.ancestors().find(|node| {
            node.kind() == PHP_IMPLEMENTS_CLAUSE || node.kind() == PHP_EXTENDS_CLAUSE
        })?;
        let names: Vec<usize> = clause
            .children()
            .filter_map(|name| doc.first(&name))
            .collect();
        // The one parent of a class stays on its line.
        (clause.kind() == PHP_IMPLEMENTS_CLAUSE || names.len() >= 2).then_some((clause, names))
    });
    let (clause, names) = clause?;
    names
        .iter()
        .all(|&at| written_on_line(doc, at))
        .then(|| (clause.ancestors().count(), Split::Before(names)))
}

/// The value of an array item or a `match` arm, when it fits on the line
/// after its `=>`.
fn item_value(doc: &Doc, tokens: &[usize], fits: &impl Fn(usize) -> bool) -> Option<Split> {
    tokens.iter().copied().find_map(|at| {
        let item = doc.kind(at) == TK_ARROW
            && [PHP_ARRAY_ITEM, PHP_MATCH_ARM].contains(&doc.parent_kind(at));
        let value = doc.next_sig(at).filter(|_| item)?;
        let heredoc = doc.source_text(value).starts_with("<<<");
        (tokens.contains(&value) && written_on_line(doc, value) && !heredoc && fits(value))
            .then(|| Split::Before(vec![value]))
    })
}

/// Breaks the line before the token. A line break between the items of a
/// list splits the list, so the list around the token is split with it.
fn break_before(doc: &mut Doc, at: usize) {
    doc.breaks[at] = true;
    let mut index = at;
    while index > 0 {
        index -= 1;
        if doc.is_closer(index) {
            index = doc.pair[index];
        } else if doc.is_opener(index) {
            if doc.flat[index] {
                doc.split[index] = true;
            }
            return;
        }
    }
}
