//! Twig statement dispatch, comments and `{{ }}` output statements.

use swlsp_syntax::{CompletedMarker, ErrorBuilder, Marker, Parser};

use super::expression::{TWIG_EXPRESSION_RECOVERY_SET, parse_twig_expression};
use super::tags::parse_twig_block_statement;
use super::{
    ParseFn, TWIG_COMMENT_CLOSE_SET, TWIG_VAR_CLOSE_SET, TwigParserExt, parse_ludtwig_directive,
    parse_many,
};
use crate::syntax::*;

/// Dispatches to a Twig construct; `None` when not at a Twig opener.
pub(crate) fn parse_any_twig(p: &mut Parser<'_>, child_parser: ParseFn) -> Option<CompletedMarker> {
    if p.at_twig_block_open() {
        parse_twig_block_statement(p, child_parser)
    } else if p.at_twig_var_open() {
        Some(parse_twig_var_statement(p))
    } else if p.at_twig_comment_open() {
        Some(parse_twig_comment_statement(p))
    } else {
        None
    }
}

fn parse_twig_comment_statement(p: &mut Parser<'_>) -> CompletedMarker {
    let m = p.start();
    p.bump();
    if p.at_set(&[TK_LUDTWIG_IGNORE_FILE, TK_LUDTWIG_IGNORE]) {
        return parse_ludtwig_directive(p, m, TWIG_COMMENT_CLOSE_SET);
    }
    parse_twig_plain_comment(p, m)
}

fn parse_twig_plain_comment(p: &mut Parser<'_>, outer: Marker) -> CompletedMarker {
    parse_many(
        p,
        |p| p.at_set(TWIG_COMMENT_CLOSE_SET),
        |p| {
            p.bump();
        },
    );
    p.expect_any(TWIG_COMMENT_CLOSE_SET, &[]);
    p.complete(outer, TWIG_COMMENT)
}

/// Line-scoped `# ...` / `## ...` comment inside a binding list, consumed as
/// raw tokens up to (excluding) the next line break.
pub(crate) fn parse_twig_inline_comment(p: &mut Parser<'_>) -> Option<CompletedMarker> {
    if !p.at(TK_HASHTAG) {
        return None;
    }
    let mut count = 0;
    let mut seen_marker = false;
    while let Some(token) = p.peek_nth_token(count) {
        if seen_marker && token.kind == TK_LINE_BREAK {
            break;
        }
        if !seen_marker && !token.kind.is_trivia() {
            if token.kind != TK_HASHTAG {
                return None;
            }
            seen_marker = true;
        }
        count += 1;
    }
    if !seen_marker || count == 0 {
        return None;
    }
    let m = p.start();
    p.bump_next_n_as(count, TK_WORD);
    Some(p.complete(m, TWIG_COMMENT))
}

pub(crate) fn parse_twig_inline_comments(p: &mut Parser<'_>) {
    while parse_twig_inline_comment(p).is_some() {}
}

pub(crate) fn parse_twig_var_statement(p: &mut Parser<'_>) -> CompletedMarker {
    let m = p.start();
    p.bump();
    if parse_twig_expression(p).is_none() {
        p.add_error(ErrorBuilder::new("twig expression"));
        p.recover(TWIG_EXPRESSION_RECOVERY_SET);
    }
    p.expect_any(TWIG_VAR_CLOSE_SET, &[]);
    p.complete(m, TWIG_VAR)
}
