//! Twig literals and postfix operators.

use swlsp_syntax::{CompletedMarker, ErrorBuilder, Parser};

use super::expression::{TWIG_EXPRESSION_RECOVERY_SET, parse_twig_expression};
use super::parse_many;
use crate::syntax::*;

/// `^\$?[a-zA-Z_\x7f-\xff][a-zA-Z0-9_\x7f-\xff]*$` over characters (the high
/// range is U+007F..=U+00FF, not raw bytes). The optional leading `$` is a
/// deliberate extension for Vue-style names such as `$tc`.
pub(crate) fn twig_name_matches(text: &str) -> bool {
    let text = text.strip_prefix('$').unwrap_or(text);
    let mut chars = text.chars();
    let high = |c: char| ('\u{7f}'..='\u{ff}').contains(&c);
    let Some(first) = chars.next() else {
        return false;
    };
    (first.is_ascii_alphabetic() || first == '_' || high(first))
        && chars.all(|c| c.is_ascii_alphanumeric() || c == '_' || high(c))
}

pub(crate) fn parse_twig_literal(p: &mut Parser<'_>) -> Option<CompletedMarker> {
    match p.peek() {
        Some(TK_NUMBER) => return Some(parse_twig_number(p)),
        Some(TK_DOUBLE_QUOTES | TK_SINGLE_QUOTES) => return Some(parse_twig_string(p, true)),
        Some(TK_OPEN_SQUARE) => return Some(parse_twig_array(p)),
        Some(TK_NULL) => return Some(parse_twig_null(p)),
        Some(TK_TRUE | TK_FALSE) => return Some(parse_twig_boolean(p)),
        Some(TK_OPEN_CURLY) => return Some(parse_twig_hash(p)),
        _ => {}
    }

    let token_kind = p.peek();
    let mut node = parse_twig_name(p)?;

    if p.at(TK_OPEN_PARENTHESIS) {
        node = parse_twig_function(p, node);
    } else if p.at(TK_EQUAL_GREATER_THAN) {
        let m = p.precede(node);
        let last = p.complete(m, TWIG_ARGUMENTS);
        node = parse_twig_arrow_function(p, last);
    } else if matches!(token_kind, Some(TK_SAME_AS | TK_DIVISIBLE_BY)) {
        // `is same as 0` without parentheses
        node = parse_twig_test_implicit_argument(p, node);
    }
    Some(node)
}

/// Accessors (`.`), indexers (`[]`) and filters (`|`).
pub(crate) fn parse_postfix_operators(
    p: &mut Parser<'_>,
    mut node: CompletedMarker,
) -> CompletedMarker {
    parse_many(
        p,
        |_| false,
        |p| {
            if p.at(TK_DOT) {
                node = parse_twig_accessor(p, node);
            } else if p.at(TK_OPEN_SQUARE) {
                node = parse_twig_indexer(p, node);
            } else if p.at(TK_SINGLE_PIPE) {
                node = parse_twig_filter(p, node);
            }
        },
    );
    node
}

fn parse_twig_number(p: &mut Parser<'_>) -> CompletedMarker {
    let m = p.start();
    p.bump();
    p.complete(m, TWIG_LITERAL_NUMBER)
}

/// String literal; `#{expr}` interpolation only in double quotes when allowed.
pub(crate) fn parse_twig_string(
    p: &mut Parser<'_>,
    interpolation_allowed: bool,
) -> CompletedMarker {
    let m = p.start();
    let quote_kind = p.bump().kind;
    let interpolation_allowed = interpolation_allowed && quote_kind == TK_DOUBLE_QUOTES;

    let inner = p.start();
    parse_many(
        p,
        |p| p.at(quote_kind),
        |p| {
            if p.at_following(&[TK_BACKWARD_SLASH, quote_kind]) {
                p.bump();
                p.bump();
            } else if p.at_following(&[TK_HASHTAG, TK_OPEN_CURLY]) {
                if !interpolation_allowed {
                    let opening = p.bump();
                    p.add_error(
                        ErrorBuilder::new("no string interpolation, because it isn't allowed here")
                            .at_token(&opening),
                    );
                    return;
                }
                p.explicitly_consume_trivia();
                let interpolation = p.start();
                p.bump();
                p.bump();
                if parse_twig_expression(p).is_none() {
                    p.add_error(ErrorBuilder::new("twig expression"));
                }
                p.expect(TK_CLOSE_CURLY, TWIG_EXPRESSION_RECOVERY_SET);
                p.complete(interpolation, TWIG_LITERAL_STRING_INTERPOLATION);
            } else {
                p.bump();
            }
        },
    );
    p.explicitly_consume_trivia();
    p.complete(inner, TWIG_LITERAL_STRING_INNER);

    p.expect(quote_kind, TWIG_EXPRESSION_RECOVERY_SET);
    p.complete(m, TWIG_LITERAL_STRING)
}

fn parse_twig_array(p: &mut Parser<'_>) -> CompletedMarker {
    let m = p.start();
    p.bump();
    let list = p.start();
    parse_many(
        p,
        |p| p.at(TK_CLOSE_SQUARE),
        |p| {
            parse_twig_expression(p);
            if p.at(TK_COMMA) {
                p.bump();
            } else if !p.at(TK_CLOSE_SQUARE) {
                p.add_error(ErrorBuilder::new(","));
            }
        },
    );
    p.complete(list, TWIG_LITERAL_ARRAY_INNER);
    p.expect(TK_CLOSE_SQUARE, TWIG_EXPRESSION_RECOVERY_SET);
    p.complete(m, TWIG_LITERAL_ARRAY)
}

fn parse_twig_null(p: &mut Parser<'_>) -> CompletedMarker {
    let m = p.start();
    p.bump();
    p.complete(m, TWIG_LITERAL_NULL)
}

fn parse_twig_boolean(p: &mut Parser<'_>) -> CompletedMarker {
    let m = p.start();
    p.bump();
    p.complete(m, TWIG_LITERAL_BOOLEAN)
}

pub(crate) fn parse_twig_hash(p: &mut Parser<'_>) -> CompletedMarker {
    let m = p.start();
    p.bump();
    let list = p.start();
    parse_many(
        p,
        |p| p.at(TK_CLOSE_CURLY),
        |p| {
            parse_twig_hash_pair(p);
            if p.at(TK_COMMA) {
                p.bump();
            } else if !p.at(TK_CLOSE_CURLY) {
                p.add_error(ErrorBuilder::new(","));
            }
        },
    );
    p.complete(list, TWIG_LITERAL_HASH_ITEMS);
    p.expect(TK_CLOSE_CURLY, TWIG_EXPRESSION_RECOVERY_SET);
    p.complete(m, TWIG_LITERAL_HASH)
}

fn parse_twig_hash_pair(p: &mut Parser<'_>) -> Option<CompletedMarker> {
    let key = if p.at(TK_NUMBER) {
        let number = parse_twig_number(p);
        let m = p.precede(number);
        p.complete(m, TWIG_LITERAL_HASH_KEY)
    } else if p.at_set(&[TK_SINGLE_QUOTES, TK_DOUBLE_QUOTES]) {
        let string = parse_twig_string(p, false);
        let m = p.precede(string);
        p.complete(m, TWIG_LITERAL_HASH_KEY)
    } else if p.at(TK_OPEN_PARENTHESIS) {
        let m = p.start();
        p.bump();
        if parse_twig_expression(p).is_none() {
            p.add_error(ErrorBuilder::new("twig expression"));
            p.recover(TWIG_EXPRESSION_RECOVERY_SET);
        }
        p.expect(TK_CLOSE_PARENTHESIS, TWIG_EXPRESSION_RECOVERY_SET);
        p.complete(m, TWIG_LITERAL_HASH_KEY)
    } else {
        let token = p.peek_token()?;
        if !twig_name_matches(token.text) {
            return None;
        }
        let m = p.start();
        p.bump_as(TK_WORD);
        p.complete(m, TWIG_LITERAL_HASH_KEY)
    };

    if p.at(TK_COLON) {
        p.bump();
        if parse_twig_expression(p).is_none() {
            p.add_error(ErrorBuilder::new("value as twig expression"));
            p.recover(TWIG_EXPRESSION_RECOVERY_SET);
        }
    }

    let m = p.precede(key);
    Some(p.complete(m, TWIG_LITERAL_HASH_PAIR))
}

/// Parses `( args )` after `(` was peeked, completing `TWIG_ARGUMENTS`.
fn parse_parenthesized_arguments(p: &mut Parser<'_>) {
    let arguments = p.start();
    p.bump();
    parse_many(
        p,
        |p| p.at(TK_CLOSE_PARENTHESIS),
        |p| {
            parse_twig_function_argument(p);
            if p.at(TK_COMMA) {
                p.bump();
            } else if !p.at(TK_CLOSE_PARENTHESIS) {
                p.add_error(ErrorBuilder::new(","));
            }
        },
    );
    p.expect(TK_CLOSE_PARENTHESIS, TWIG_EXPRESSION_RECOVERY_SET);
    p.complete(arguments, TWIG_ARGUMENTS);
}

pub(crate) fn parse_twig_filter(p: &mut Parser<'_>, last: CompletedMarker) -> CompletedMarker {
    let m = p.precede(last);
    let last = p.complete(m, TWIG_OPERAND);
    let outer = p.precede(last);
    p.bump();

    let rhs = p.start();
    if parse_twig_name(p).is_none() {
        p.add_error(ErrorBuilder::new("twig filter"));
        p.recover(TWIG_EXPRESSION_RECOVERY_SET);
    } else if p.at(TK_OPEN_PARENTHESIS) {
        parse_parenthesized_arguments(p);
    }
    p.complete(rhs, TWIG_OPERAND);
    p.complete(outer, TWIG_FILTER)
}

fn parse_twig_indexer(p: &mut Parser<'_>, last: CompletedMarker) -> CompletedMarker {
    let m = p.precede(last);
    let last = p.complete(m, TWIG_OPERAND);
    let outer = p.precede(last);
    p.bump();

    let index = p.start();
    let missing_lower = parse_twig_expression(p).is_none();
    let mut is_slice = false;
    let mut missing_upper = true;
    if p.at(TK_COLON) {
        p.bump();
        is_slice = true;
        missing_upper = parse_twig_expression(p).is_none();
    }
    if missing_lower && missing_upper {
        p.add_error(ErrorBuilder::new("twig expression"));
        p.recover(TWIG_EXPRESSION_RECOVERY_SET);
    }
    p.complete(
        index,
        if is_slice {
            TWIG_INDEX_RANGE
        } else {
            TWIG_INDEX
        },
    );

    p.expect(TK_CLOSE_SQUARE, TWIG_EXPRESSION_RECOVERY_SET);
    p.complete(outer, TWIG_INDEX_LOOKUP)
}

fn parse_twig_accessor(p: &mut Parser<'_>, last: CompletedMarker) -> CompletedMarker {
    let m = p.precede(last);
    let last = p.complete(m, TWIG_OPERAND);
    let outer = p.precede(last);
    p.bump();

    let rhs = p.start();
    if p.at(TK_NUMBER) {
        // `.0` is an index lookup instead of a name accessor
        let number = parse_twig_number(p);
        let expression = p.precede(number);
        p.complete(expression, TWIG_EXPRESSION);
        p.complete(rhs, TWIG_INDEX);
        return p.complete(outer, TWIG_INDEX_LOOKUP);
    }
    if parse_twig_name(p).is_none() {
        p.add_error(ErrorBuilder::new("twig variable property, key or method"));
        p.recover(TWIG_EXPRESSION_RECOVERY_SET);
    }
    p.complete(rhs, TWIG_OPERAND);
    let mut node = p.complete(outer, TWIG_ACCESSOR);
    if p.at(TK_OPEN_PARENTHESIS) {
        node = parse_twig_function(p, node);
    }
    node
}

pub(crate) fn parse_twig_function(p: &mut Parser<'_>, last: CompletedMarker) -> CompletedMarker {
    let m = p.precede(last);
    let last = p.complete(m, TWIG_OPERAND);
    let outer = p.precede(last);
    parse_parenthesized_arguments(p);
    p.complete(outer, TWIG_FUNCTION_CALL)
}

fn parse_twig_test_implicit_argument(p: &mut Parser<'_>, last: CompletedMarker) -> CompletedMarker {
    let m = p.precede(last);
    let operand = p.complete(m, TWIG_OPERAND);
    let outer = p.precede(operand);
    let arguments = p.start();
    if parse_twig_expression(p).is_none() {
        p.add_error(ErrorBuilder::new("argument for twig test"));
        p.recover(TWIG_EXPRESSION_RECOVERY_SET);
    }
    p.complete(arguments, TWIG_ARGUMENTS);
    p.complete(outer, TWIG_FUNCTION_CALL)
}

pub(crate) fn parse_twig_arrow_function(
    p: &mut Parser<'_>,
    last: CompletedMarker,
) -> CompletedMarker {
    let outer = p.precede(last);
    // Release ludtwig blind-bumps here; only at EOF fall back to expect.
    if !p.at_end() {
        p.bump();
    } else {
        p.expect(TK_EQUAL_GREATER_THAN, TWIG_EXPRESSION_RECOVERY_SET);
    }
    if parse_twig_expression(p).is_none() {
        p.add_error(ErrorBuilder::new(
            "single twig expression as the body of the closure",
        ));
        p.recover(TWIG_EXPRESSION_RECOVERY_SET);
    }
    p.complete(outer, TWIG_ARROW_FUNCTION)
}

/// A named argument (`name = value` / `name: value`) or a plain expression.
pub(crate) fn parse_twig_function_argument(p: &mut Parser<'_>) -> Option<CompletedMarker> {
    if p.at_following(&[TK_WORD, TK_EQUAL]) || p.at_following(&[TK_WORD, TK_COLON]) {
        let m = p.start();
        p.bump();
        p.expect_any(&[TK_EQUAL, TK_COLON], TWIG_EXPRESSION_RECOVERY_SET);
        parse_twig_expression(p);
        return Some(p.complete(m, TWIG_NAMED_ARGUMENT));
    }
    parse_twig_expression(p)
}

/// A single name token (re-bumped as `TK_WORD`), plus the `same as` /
/// `divisible by` test tokens.
pub(crate) fn parse_twig_name(p: &mut Parser<'_>) -> Option<CompletedMarker> {
    let token = p.peek_token()?;
    let special = matches!(token.kind, TK_SAME_AS | TK_DIVISIBLE_BY);
    if !special && !twig_name_matches(token.text) {
        return None;
    }
    let m = p.start();
    p.bump_as(TK_WORD);
    Some(p.complete(m, TWIG_LITERAL_NAME))
}
