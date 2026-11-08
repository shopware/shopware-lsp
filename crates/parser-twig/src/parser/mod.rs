//! Twig/HTML grammar (port of `internal/parser/twig/parser`, itself a port of
//! ludtwig-parser's grammar modules).
//!
//! The grammar drives the shared [`swlsp_syntax::Parser`] engine. It is total:
//! any input yields a lossless tree plus a flat list of errors.

mod expression;
mod html;
mod literal;
mod shopware;
mod tags;
mod twig;

use std::sync::Arc;

use swlsp_syntax::{
    CompletedMarker, Config, ErrorBuilder, FollowingContent, Kind, Marker, Parse, Parser,
};

use crate::lexer;
use crate::syntax::*;

/// A child parser passed down into tag bodies (ludtwig's `ParseFunction`).
pub(crate) type ParseFn = fn(&mut Parser<'_>) -> Option<CompletedMarker>;

/// Tokens that can begin a new top-level element.
pub(crate) const GENERAL_RECOVERY_SET: &[Kind] = &[
    TK_CURLY_PERCENT,
    TK_CURLY_PERCENT_MINUS,
    TK_CURLY_PERCENT_TILDE,
    TK_OPEN_CURLY_CURLY,
    TK_OPEN_CURLY_CURLY_MINUS,
    TK_OPEN_CURLY_CURLY_TILDE,
    TK_OPEN_CURLY_HASHTAG,
    TK_OPEN_CURLY_HASHTAG_MINUS,
    TK_OPEN_CURLY_HASHTAG_TILDE,
    TK_LESS_THAN,
    TK_LESS_THAN_EXCLAMATION_MARK_MINUS_MINUS,
    TK_LESS_THAN_EXCLAMATION_MARK,
];

pub(crate) const TWIG_BLOCK_OPEN_SET: &[Kind] = &[
    TK_CURLY_PERCENT,
    TK_CURLY_PERCENT_MINUS,
    TK_CURLY_PERCENT_TILDE,
];
pub(crate) const TWIG_BLOCK_CLOSE_SET: &[Kind] = &[
    TK_PERCENT_CURLY,
    TK_MINUS_PERCENT_CURLY,
    TK_TILDE_PERCENT_CURLY,
];
pub(crate) const TWIG_VAR_OPEN_SET: &[Kind] = &[
    TK_OPEN_CURLY_CURLY,
    TK_OPEN_CURLY_CURLY_MINUS,
    TK_OPEN_CURLY_CURLY_TILDE,
];
pub(crate) const TWIG_VAR_CLOSE_SET: &[Kind] = &[
    TK_CLOSE_CURLY_CURLY,
    TK_MINUS_CLOSE_CURLY_CURLY,
    TK_TILDE_CLOSE_CURLY_CURLY,
];
pub(crate) const TWIG_COMMENT_OPEN_SET: &[Kind] = &[
    TK_OPEN_CURLY_HASHTAG,
    TK_OPEN_CURLY_HASHTAG_MINUS,
    TK_OPEN_CURLY_HASHTAG_TILDE,
];
pub(crate) const TWIG_COMMENT_CLOSE_SET: &[Kind] = &[
    TK_HASHTAG_CLOSE_CURLY,
    TK_MINUS_HASHTAG_CLOSE_CURLY,
    TK_TILDE_HASHTAG_CLOSE_CURLY,
];

/// Parses Twig and HTML source into a lossless concrete syntax tree.
pub fn parse(source: &str) -> Parse {
    parse_shared(Arc::from(source))
}

/// Like [`parse`], but the tree shares `shared` instead of copying it.
pub fn parse_shared(shared: Arc<str>) -> Parse {
    let source: &str = &shared;
    let tokens = lexer::lex(source);
    let mut p = new_parser(source, tokens);
    root(&mut p);
    let (tree, errors) = p.finish_with_source(Arc::clone(&shared));
    Parse { tree, errors }
}

pub(crate) fn new_parser<'a>(source: &'a str, tokens: Vec<swlsp_syntax::Token<'a>>) -> Parser<'a> {
    Parser::new(
        source,
        tokens,
        Config {
            general_recovery_set: GENERAL_RECOVERY_SET,
            error_kind: ERROR,
        },
    )
}

/// Twig-specific predicates over the engine.
pub(crate) trait TwigParserExt {
    fn at_twig_block_open(&self) -> bool;
    fn at_twig_block_close(&self) -> bool;
    fn at_twig_var_open(&self) -> bool;
    fn at_twig_comment_open(&self) -> bool;
    /// Whether the parser is at `{% keyword` (any whitespace-control variant).
    fn at_twig_tag(&self, keyword: Kind) -> bool;
    /// Whether the parser is at `{% name` where `name` is a plain word.
    fn at_twig_word_tag(&self, name: &str) -> bool;
    /// Whether the current non-trivia token is a word with exactly this text.
    fn at_word(&self, text: &str) -> bool;
}

impl TwigParserExt for Parser<'_> {
    fn at_twig_block_open(&self) -> bool {
        self.at_set(TWIG_BLOCK_OPEN_SET)
    }

    fn at_twig_block_close(&self) -> bool {
        self.at_set(TWIG_BLOCK_CLOSE_SET)
    }

    fn at_twig_var_open(&self) -> bool {
        self.at_set(TWIG_VAR_OPEN_SET)
    }

    fn at_twig_comment_open(&self) -> bool {
        self.at_set(TWIG_COMMENT_OPEN_SET)
    }

    fn at_twig_tag(&self, keyword: Kind) -> bool {
        self.at_twig_block_open() && self.peek_next_non_trivia_kind() == Some(keyword)
    }

    fn at_twig_word_tag(&self, name: &str) -> bool {
        TWIG_BLOCK_OPEN_SET.iter().any(|opening| {
            self.at_following_content(&[
                FollowingContent {
                    kind: *opening,
                    text: None,
                },
                FollowingContent {
                    kind: TK_WORD,
                    text: Some(name),
                },
            ])
        })
    }

    fn at_word(&self, text: &str) -> bool {
        self.peek_token()
            .is_some_and(|token| token.kind == TK_WORD && token.text == text)
    }
}

/// Parses the whole document into a `ROOT` node.
fn root(p: &mut Parser<'_>) {
    let m = p.start();
    parse_many(
        p,
        |_| false,
        |p| {
            if parse_any_element(p).is_none() && !p.at_end() {
                p.add_error(ErrorBuilder::new("html, text or twig element"));
                p.recover(&[]);
            }
        },
    );
    p.complete(m, ROOT);
}

/// The universal loop combinator: silently bumps `TK_UNKNOWN`, stops at EOF,
/// on `early_exit`, or when `child` made no progress.
pub(crate) fn parse_many<'a>(
    p: &mut Parser<'a>,
    mut early_exit: impl FnMut(&mut Parser<'a>) -> bool,
    mut child: impl FnMut(&mut Parser<'a>),
) {
    loop {
        let position = p.get_pos();
        if p.at(TK_UNKNOWN) {
            p.bump();
            continue;
        }
        if p.at_end() || early_exit(p) {
            break;
        }
        child(p);
        if p.get_pos() == position {
            break;
        }
    }
}

/// Twig first, then HTML.
pub(crate) fn parse_any_element(p: &mut Parser<'_>) -> Option<CompletedMarker> {
    twig::parse_any_twig(p, parse_any_element).or_else(|| html::parse_any_html(p))
}

/// Parses a `ludtwig-ignore` / `ludtwig-ignore-file` directive inside an
/// already-started comment marker.
pub(crate) fn parse_ludtwig_directive(
    p: &mut Parser<'_>,
    outer: Marker,
    closing_kinds: &[Kind],
) -> CompletedMarker {
    let ignore_kind = if p.at(TK_LUDTWIG_IGNORE_FILE) {
        LUDTWIG_DIRECTIVE_FILE_IGNORE
    } else {
        LUDTWIG_DIRECTIVE_IGNORE
    };
    p.bump();

    let rule_list = p.start();
    let mut recovery = Vec::with_capacity(closing_kinds.len() + 1);
    recovery.push(TK_COMMA);
    recovery.extend_from_slice(closing_kinds);
    parse_many(
        p,
        |p| p.at_set(closing_kinds),
        |p| {
            p.expect(TK_WORD, &recovery);
            if p.at(TK_COMMA) {
                p.bump();
            }
        },
    );
    p.complete(rule_list, LUDTWIG_DIRECTIVE_RULE_LIST);

    p.expect_any(closing_kinds, &[]);
    p.complete(outer, ignore_kind)
}

#[cfg(test)]
mod tests;
