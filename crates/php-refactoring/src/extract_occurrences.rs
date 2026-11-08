//! Sharing one extracted variable between equal pure expressions.

use std::collections::HashSet;

use swlsp_parser_php::query;
use swlsp_parser_php::syntax::*;
use swlsp_rewrite::Builder;
use swlsp_syntax::{SyntaxNode, SyntaxNodeExt, TextRange};

use crate::{Cancelled, Extraction, Result, check, descendant_tokens, newline_of, source_indent};

/// Collects non-overlapping occurrences equal to `chosen` that start at or
/// after it.
struct Occurrences<'a> {
    chosen: &'a SyntaxNode,
    chosen_start: swlsp_syntax::TextSize,
    key: String,
    matches: Vec<TextRange>,
}

impl<'a> Occurrences<'a> {
    fn new(chosen: &'a SyntaxNode) -> Self {
        Self {
            chosen,
            chosen_start: chosen.range_trimmed_trivia().start(),
            key: extraction_key(chosen),
            matches: Vec::new(),
        }
    }

    fn add(&mut self, node: &SyntaxNode) {
        let range = node.range_trimmed_trivia();
        if range.start() < self.chosen_start
            || extraction_key(node) != self.key
            || (node != self.chosen && extraction_has_comments(node))
        {
            return;
        }
        if self
            .matches
            .last()
            .is_none_or(|last| last.end() <= range.start())
        {
            self.matches.push(range);
        }
    }

    /// `expression` and every node beneath it (the expression itself twice,
    /// like Go, which is harmless).
    fn add_tree(&mut self, expression: &SyntaxNode) {
        self.add(expression);
        for node in expression.descendants() {
            self.add(&node);
        }
    }
}

/// Sharing is limited to consecutive pure statements in the same block. A
/// mutation of an input, a call or control-flow boundary ends the region.
pub(crate) fn extract_all_occurrences(
    source: &str,
    chosen: &SyntaxNode,
    statement: &SyntaxNode,
    name: String,
    cancelled: Cancelled<'_>,
) -> Result<Option<Extraction>> {
    let Some(parent) = statement.parent() else {
        return Ok(None);
    };
    if !query::pure_extraction_expression(chosen) {
        return Ok(None);
    }
    let dependencies: HashSet<String> = descendant_tokens(chosen)
        .filter(|token| token.kind() == TK_VARIABLE)
        .map(|token| token.text().to_owned())
        .collect();
    let mut occurrences = Occurrences::new(chosen);
    let mut started = false;
    for next in parent.children() {
        check(cancelled)?;
        if next == *statement {
            started = true;
        }
        if !started {
            continue;
        }
        let expression = match query::simple_assignment(&next) {
            Some((target, expression)) => {
                if dependencies.contains(&target) {
                    break;
                }
                Some(expression)
            }
            None if matches!(next.kind(), PHP_ECHO_STATEMENT | PHP_RETURN_STATEMENT) => {
                next.first_child()
            }
            None => break,
        };
        let Some(expression) = expression.filter(query::pure_extraction_expression) else {
            break;
        };
        occurrences.add_tree(&expression);
        if next.kind() == PHP_RETURN_STATEMENT {
            break;
        }
    }
    let matches = occurrences.matches;
    if matches.len() < 2 {
        return Ok(None);
    }
    let mut builder = Builder::new(source);
    let start = statement.range_trimmed_trivia().start();
    let range = chosen.range_trimmed_trivia();
    let newline = newline_of(source);
    let indent = source_indent(source, start.into());
    builder.insert(
        start.into(),
        format!("{name} = {};{newline}{indent}", &source[range]),
    )?;
    for range in &matches {
        builder.replace_range(*range, name.clone())?;
    }
    Ok(Some(Extraction {
        edits: builder.finish()?,
        name,
        occurrences: matches.len(),
    }))
}

/// Non-trivia token texts, NUL-separated: equal keys mean syntactically equal
/// expressions regardless of white space and comments.
fn extraction_key(node: &SyntaxNode) -> String {
    let mut text = String::new();
    for token in descendant_tokens(node).filter(|token| !token.kind().is_trivia()) {
        text.push_str(token.text());
        text.push('\0');
    }
    text
}

fn extraction_has_comments(node: &SyntaxNode) -> bool {
    descendant_tokens(node).any(|token| matches!(token.kind(), TK_LINE_COMMENT | TK_BLOCK_COMMENT))
}

/// Shares one inline assignment between equal pure arguments of the same
/// eagerly evaluated argument list.
pub(crate) fn extract_argument_occurrences(
    source: &str,
    chosen: &SyntaxNode,
    name: String,
    cancelled: Cancelled<'_>,
) -> Result<Option<Extraction>> {
    let Some(list) = query::eager_argument_list(chosen) else {
        return Ok(None);
    };
    if !query::pure_extraction_expression(chosen) {
        return Ok(None);
    }
    let call = list.parent();
    let mut occurrences = Occurrences::new(chosen);
    for index in 0..list.children().count() {
        check(cancelled)?;
        let expression = call
            .as_ref()
            .and_then(|call| query::argument_expression(call, index));
        let Some(expression) = expression.filter(query::pure_extraction_expression) else {
            return Ok(None);
        };
        occurrences.add_tree(&expression);
    }
    let matches = occurrences.matches;
    if matches.len() < 2 || matches[0] != chosen.range_trimmed_trivia() {
        return Ok(None);
    }
    let mut builder = Builder::new(source);
    for (index, range) in matches.iter().enumerate() {
        let text = if index == 0 {
            format!("({name} = {})", &source[*range])
        } else {
            name.clone()
        };
        builder.replace_range(*range, text)?;
    }
    Ok(Some(Extraction {
        edits: builder.finish()?,
        name,
        occurrences: matches.len(),
    }))
}
