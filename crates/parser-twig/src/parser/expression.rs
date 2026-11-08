//! Pratt parser for Twig expressions.

use swlsp_syntax::{CompletedMarker, ErrorBuilder, Kind, Marker, Parser};

use super::literal::{
    parse_postfix_operators, parse_twig_arrow_function, parse_twig_function, parse_twig_literal,
    parse_twig_name,
};
use super::parse_many;
use crate::syntax::*;

/// Master synchronization set for Twig expressions: closing delimiters,
/// literal starts and operators.
pub(crate) const TWIG_EXPRESSION_RECOVERY_SET: &[Kind] = &[
    TK_SINGLE_PIPE,
    TK_CLOSE_PARENTHESIS,
    TK_CLOSE_CURLY,
    TK_CLOSE_SQUARE,
    TK_COLON,
    TK_CLOSE_CURLY_CURLY,
    TK_MINUS_CLOSE_CURLY_CURLY,
    TK_TILDE_CLOSE_CURLY_CURLY,
    TK_PERCENT_CURLY,
    TK_MINUS_PERCENT_CURLY,
    TK_TILDE_PERCENT_CURLY,
    TK_OPEN_PARENTHESIS,
    // literals
    TK_NUMBER,
    TK_DOUBLE_QUOTES,
    TK_SINGLE_QUOTES,
    TK_OPEN_SQUARE,
    TK_NULL,
    TK_TRUE,
    TK_FALSE,
    TK_OPEN_CURLY,
    // operators
    TK_OR,
    TK_DOUBLE_PIPE,
    TK_AND,
    TK_DOUBLE_AMPERSAND,
    TK_BINARY_OR,
    TK_BINARY_XOR,
    TK_BINARY_AND,
    TK_DOUBLE_EQUAL,
    TK_EXCLAMATION_MARK_EQUALS,
    TK_LESS_THAN_EQUAL_GREATER_THAN,
    TK_LESS_THAN,
    TK_GREATER_THAN,
    TK_GREATER_THAN_EQUAL,
    TK_LESS_THAN_EQUAL,
    TK_NOT,
    TK_NOT_IN,
    TK_IN,
    TK_MATCHES,
    TK_STARTS_WITH,
    TK_ENDS_WITH,
    TK_TRIPLE_EQUAL,
    TK_EXCLAMATION_MARK_DOUBLE_EQUALS,
    TK_DOUBLE_DOT,
    TK_TRIPLE_DOT,
    TK_PLUS,
    TK_MINUS,
    TK_TILDE,
    TK_STAR,
    TK_FORWARD_SLASH,
    TK_DOUBLE_FORWARD_SLASH,
    TK_PERCENT,
    TK_IS,
    TK_IS_NOT,
    TK_DOUBLE_STAR,
    TK_DOUBLE_QUESTION_MARK,
];

/// `(left, right)` binding powers of a binary operator.
fn binary_binding_power(kind: Kind) -> Option<(u8, u8)> {
    Some(match kind {
        TK_OR | TK_DOUBLE_PIPE => (5, 6),
        TK_AND | TK_DOUBLE_AMPERSAND => (10, 11),
        TK_BINARY_OR => (14, 15),
        TK_BINARY_XOR => (16, 17),
        TK_BINARY_AND => (18, 19),
        TK_DOUBLE_EQUAL
        | TK_EXCLAMATION_MARK_EQUALS
        | TK_LESS_THAN_EQUAL_GREATER_THAN
        | TK_LESS_THAN
        | TK_GREATER_THAN
        | TK_GREATER_THAN_EQUAL
        | TK_LESS_THAN_EQUAL
        | TK_IN
        | TK_NOT_IN
        | TK_MATCHES
        | TK_STARTS_WITH
        | TK_ENDS_WITH
        | TK_TRIPLE_EQUAL
        | TK_EXCLAMATION_MARK_DOUBLE_EQUALS => (20, 21),
        TK_DOUBLE_DOT => (25, 26),
        TK_PLUS | TK_MINUS => (30, 31),
        TK_TILDE => (40, 41),
        TK_STAR | TK_FORWARD_SLASH | TK_DOUBLE_FORWARD_SLASH | TK_PERCENT => (60, 61),
        TK_IS | TK_IS_NOT => (100, 101),
        // right associative
        TK_DOUBLE_STAR => (121, 120),
        TK_DOUBLE_QUESTION_MARK => (151, 150),
        _ => return None,
    })
}

/// Right binding power of a prefix unary operator.
fn unary_binding_power(kind: Kind) -> Option<u8> {
    match kind {
        TK_NOT => Some(51),
        TK_PLUS | TK_MINUS => Some(201),
        TK_TRIPLE_DOT => Some(211),
        _ => None,
    }
}

pub(crate) fn parse_twig_expression(p: &mut Parser<'_>) -> Option<CompletedMarker> {
    parse_twig_expression_binding_power(p, 0)
}

fn parse_twig_expression_binding_power(
    p: &mut Parser<'_>,
    minimum_binding_power: u8,
) -> Option<CompletedMarker> {
    let lhs = parse_twig_expression_lhs(p)?;
    let m = p.precede(lhs);
    let lhs = p.complete(m, TWIG_EXPRESSION);
    Some(parse_twig_expression_binding_power_rhs(
        p,
        minimum_binding_power,
        lhs,
    ))
}

fn parse_twig_expression_binding_power_rhs(
    p: &mut Parser<'_>,
    minimum_binding_power: u8,
    mut lhs: CompletedMarker,
) -> CompletedMarker {
    let mut is_binary = false;
    while let Some(kind) = p.peek() {
        let Some((left, right)) = binary_binding_power(kind) else {
            break;
        };
        if left < minimum_binding_power {
            break;
        }
        p.bump();
        is_binary = true;

        let m = p.precede(lhs);
        let parsed_rhs = parse_twig_expression_binding_power(p, right).is_some();
        lhs = p.complete(m, TWIG_BINARY_EXPRESSION);
        if !parsed_rhs {
            break;
        }
    }

    if is_binary {
        let m = p.precede(lhs);
        lhs = p.complete(m, TWIG_EXPRESSION);
    }

    if minimum_binding_power == 0
        && let Some(m) = parse_conditional_expression(p, lhs)
    {
        lhs = m;
    }
    lhs
}

const VAR_AND_BLOCK_CLOSE: &[Kind] = &[
    TK_CLOSE_CURLY_CURLY,
    TK_MINUS_CLOSE_CURLY_CURLY,
    TK_TILDE_CLOSE_CURLY_CURLY,
    TK_PERCENT_CURLY,
    TK_MINUS_PERCENT_CURLY,
    TK_TILDE_PERCENT_CURLY,
];

/// `a ? b : c`, `a ? b` and `a ?: c`.
fn parse_conditional_expression(
    p: &mut Parser<'_>,
    lhs: CompletedMarker,
) -> Option<CompletedMarker> {
    if !p.at(TK_QUESTION_MARK) {
        return None;
    }
    let m = p.precede(lhs);
    p.bump();

    if parse_twig_expression_binding_power(p, 0).is_none() && !p.at(TK_COLON) {
        p.add_error(ErrorBuilder::new("twig expression or ':'"));
        let mut recovery = vec![TK_COLON];
        recovery.extend_from_slice(VAR_AND_BLOCK_CLOSE);
        p.recover(&recovery);
    }

    if p.at(TK_COLON) {
        p.bump();
        if parse_twig_expression_binding_power(p, 0).is_none() {
            p.add_error(ErrorBuilder::new("twig expression"));
            p.recover(VAR_AND_BLOCK_CLOSE);
        }
    }

    let conditional = p.complete(m, TWIG_CONDITIONAL_EXPRESSION);
    let outer = p.precede(conditional);
    Some(p.complete(outer, TWIG_EXPRESSION))
}

fn parse_twig_expression_lhs(p: &mut Parser<'_>) -> Option<CompletedMarker> {
    if p.at(TK_OPEN_PARENTHESIS) {
        let (m, is_paren_expression) = parse_paren_expression(p);
        if is_paren_expression {
            let node = p.complete(m, TWIG_PARENTHESES_EXPRESSION);
            return Some(parse_postfix_operators(p, node));
        }
        return Some(parse_paren_arrow_function(p, m));
    }
    if p.at_set(&[TK_MINUS, TK_PLUS, TK_NOT, TK_TRIPLE_DOT]) {
        return Some(parse_unary_expression(p));
    }
    let node = parse_twig_literal(p)?;
    Some(parse_postfix_operators(p, node))
}

/// Disambiguates a parenthesized expression from an arrow-function parameter
/// list. Returns the open marker and whether it is a parenthesized expression.
fn parse_paren_expression(p: &mut Parser<'_>) -> (Marker, bool) {
    let m = p.start();
    p.bump();

    let non_unary = p
        .peek()
        .is_some_and(|kind| unary_binding_power(kind).is_none());
    if non_unary {
        if let Some(mut literal) = parse_twig_name(p) {
            if p.at(TK_COMMA) || p.at_following(&[TK_CLOSE_PARENTHESIS, TK_EQUAL_GREATER_THAN]) {
                return (m, false); // arrow function
            }
            if p.at(TK_OPEN_PARENTHESIS) {
                literal = parse_twig_function(p, literal);
            }
            literal = parse_postfix_operators(p, literal);
            let expression = p.precede(literal);
            let lhs = p.complete(expression, TWIG_EXPRESSION);
            parse_twig_expression_binding_power_rhs(p, 0, lhs);
        } else {
            parse_twig_expression_binding_power(p, 0);
        }
    } else {
        parse_twig_expression_binding_power(p, 0);
    }

    p.expect(TK_CLOSE_PARENTHESIS, TWIG_EXPRESSION_RECOVERY_SET);
    (m, true)
}

fn parse_paren_arrow_function(p: &mut Parser<'_>, m: Marker) -> CompletedMarker {
    parse_many(
        p,
        |p| p.at(TK_CLOSE_PARENTHESIS),
        |p| {
            p.expect(TK_COMMA, TWIG_EXPRESSION_RECOVERY_SET);
            parse_twig_name(p);
        },
    );
    p.expect(TK_CLOSE_PARENTHESIS, TWIG_EXPRESSION_RECOVERY_SET);
    let last = p.complete(m, TWIG_ARGUMENTS);
    parse_twig_arrow_function(p, last)
}

fn parse_unary_expression(p: &mut Parser<'_>) -> CompletedMarker {
    let m = p.start();
    let operator = p.bump();
    let right =
        unary_binding_power(operator.kind).expect("'-', '+', 'not' and '...' have a binding power");
    parse_twig_expression_binding_power(p, right);
    p.complete(m, TWIG_UNARY_EXPRESSION)
}
