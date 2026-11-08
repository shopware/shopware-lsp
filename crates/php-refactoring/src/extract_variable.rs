use std::collections::HashSet;

use swlsp_parser_php::query;
use swlsp_parser_php::syntax::*;
use swlsp_rewrite::Builder;
use swlsp_syntax::rowan::NodeOrToken;
use swlsp_syntax::{SyntaxNode, SyntaxNodeExt, TextRange};

use crate::extract_occurrences::{extract_all_occurrences, extract_argument_occurrences};
use crate::{Cancelled, Extraction, Result, check, child_tokens, newline_of, source_indent};

#[derive(Clone, Copy, Default)]
pub struct VariableOptions<'a> {
    /// Whether a call is known to return a value (and may therefore be
    /// extracted from an argument position).
    pub call_returns_value: Option<&'a dyn Fn(&SyntaxNode) -> bool>,
    pub all_occurrences: bool,
}

/// Replaces a selected expression (or the smallest supported expression at
/// a caret) with a fresh local variable.
pub fn extract_variable(
    root: &SyntaxNode,
    source: &str,
    mut selection: TextRange,
    options: VariableOptions<'_>,
    cancelled: Cancelled<'_>,
) -> Result<Option<Extraction>> {
    if u32::from(selection.end()) as usize > source.len() {
        return Ok(None);
    }
    // Explicit selections may include whitespace, but must cover one whole
    // expression.
    if !selection.is_empty() {
        let bytes = source.as_bytes();
        let space = |offset: u32| matches!(bytes[offset as usize], b' ' | b'\t' | b'\r' | b'\n');
        let (mut start, mut end) = (u32::from(selection.start()), u32::from(selection.end()));
        while start < end && space(start) {
            start += 1;
        }
        while end > start && space(end - 1) {
            end -= 1;
        }
        if start == end {
            return Ok(None);
        }
        selection = TextRange::new(start.into(), end.into());
    }
    let mut chosen: Option<(SyntaxNode, SyntaxNode, bool)> = None;
    let mut names: HashSet<String> = HashSet::new();
    for element in root.descendants_with_tokens() {
        check(cancelled)?;
        let node = match element {
            NodeOrToken::Token(token) => {
                if token.kind() == TK_VARIABLE {
                    names.insert(token.text().to_owned());
                }
                // Dynamic symbol tables and line-number literals make
                // introducing a local or moving source unsafe without
                // whole-program information.
                let text = token.text();
                if ["$", "eval", "extract", "get_defined_vars", "__line__"]
                    .iter()
                    .any(|unsafe_text| text.eq_ignore_ascii_case(unsafe_text))
                {
                    return Ok(None);
                }
                continue;
            }
            NodeOrToken::Node(node) => node,
        };
        if node.kind() == ERROR {
            return Ok(None);
        }
        if !query::extractable_expression(&node) {
            continue;
        }
        let range = node.range_trimmed_trivia();
        let matches = if selection.is_empty() {
            range.start() <= selection.start() && selection.start() < range.end()
        } else {
            range == selection
        };
        if !matches
            || chosen
                .as_ref()
                .is_some_and(|(current, _, _)| range.len() >= current.range_trimmed_trivia().len())
        {
            continue;
        }
        let (owner, in_place) = match query::extraction_statement(&node) {
            Some(owner) => (Some(owner), false),
            None => (
                query::inline_extraction_owner(&node, options.call_returns_value),
                true,
            ),
        };
        if let Some(owner) = owner
            && safe_extraction(&node, &owner)
        {
            chosen = Some((node, owner, in_place));
        }
    }
    let Some((chosen, statement, inline)) = chosen else {
        return Ok(None);
    };
    let mut name = "$extracted".to_owned();
    let mut suffix = 2;
    while names.contains(&name) || source.contains(&name[1..]) {
        name = format!("$extracted{suffix}");
        suffix += 1;
    }
    if options.all_occurrences {
        if inline {
            return extract_argument_occurrences(source, &chosen, name, cancelled);
        }
        return extract_all_occurrences(source, &chosen, &statement, name, cancelled);
    }
    build_variable_extraction(source, &chosen, &statement, name, inline).map(Some)
}

fn build_variable_extraction(
    source: &str,
    chosen: &SyntaxNode,
    statement: &SyntaxNode,
    name: String,
    inline: bool,
) -> Result<Extraction> {
    let range = chosen.range_trimmed_trivia();
    let text = &source[range];
    let mut builder = Builder::new(source);
    if inline {
        builder.replace_range(range, format!("({name} = {text})"))?;
    } else {
        let start = statement.range_trimmed_trivia().start();
        let indent = source_indent(source, start.into());
        let newline = newline_of(source);
        builder.insert(start.into(), format!("{name} = {text};{newline}{indent}"))?;
        builder.replace_range(range, name.clone())?;
    }
    Ok(Extraction {
        edits: builder.finish()?,
        name,
        occurrences: 1,
    })
}

fn safe_extraction(expression: &SyntaxNode, statement: &SyntaxNode) -> bool {
    if expression.descendants().any(|node| {
        matches!(
            node.kind(),
            PHP_YIELD_EXPRESSION | PHP_CLOSURE | PHP_ARROW_FUNCTION
        )
    }) {
        return false;
    }
    // Returning through a new temporary would change by-reference returns.
    for parent in statement.ancestors() {
        match parent.kind() {
            PHP_PROPERTY_HOOK => return false,
            PHP_FUNCTION_DECLARATION
            | PHP_METHOD_DECLARATION
            | PHP_CLOSURE
            | PHP_ARROW_FUNCTION => {
                return !child_tokens(&parent).any(|token| token.kind() == TK_AMPERSAND);
            }
            _ => {}
        }
    }
    true
}
