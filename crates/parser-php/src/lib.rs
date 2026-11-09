//! Lossless, error-tolerant PHP parser.

pub mod lexer;
mod parser;
pub mod query;
pub mod syntax;

use std::sync::Arc;

use swlsp_syntax::{Parse, SyntaxNode};

/// Parses one PHP source snapshot into a lossless tree plus parse errors.
pub fn parse(source: &str) -> Parse {
    parser::parse(source)
}

/// Like [`parse`], but the tree shares `source` instead of copying it.
pub fn parse_shared(source: Arc<str>) -> Parse {
    parser::parse_shared(source)
}

/// Parses `source` and returns the root node.
pub fn root(source: &str) -> SyntaxNode {
    parse(source).root()
}

/// Case-insensitive comparison of `text` with an ASCII keyword. Besides ASCII
/// case, the only folds are the long s (`ſ`) to `s` and the Kelvin sign to
/// `k`, the two non-ASCII characters whose simple case folding is ASCII.
pub(crate) fn eq_ignore_case_keyword(text: &str, word: &str) -> bool {
    if text.len() == word.len() {
        return text.eq_ignore_ascii_case(word);
    }
    if text.is_ascii() {
        return false;
    }
    let mut word_chars = word.bytes();
    for character in text.chars() {
        let folded = match character {
            '\u{017F}' => b's',
            '\u{212A}' => b'k',
            c if c.is_ascii() => c.to_ascii_lowercase() as u8,
            _ => return false,
        };
        match word_chars.next() {
            Some(expected) if expected.to_ascii_lowercase() == folded => {}
            _ => return false,
        }
    }
    word_chars.next().is_none()
}

/// Case-insensitive comparison of two names, character by character through
/// simple case folding (a character folds to the lowercase of its uppercase
/// form when both are single characters).
pub(crate) fn eq_ignore_case(left: &str, right: &str) -> bool {
    if left.is_ascii() && right.is_ascii() {
        return left.eq_ignore_ascii_case(right);
    }
    if right.is_ascii() {
        return eq_ignore_case_keyword(left, right);
    }
    if left.is_ascii() {
        return eq_ignore_case_keyword(right, left);
    }
    fn fold(character: char) -> char {
        match character {
            '\u{017F}' => 's',
            '\u{212A}' => 'k',
            c => {
                let mut upper = c.to_uppercase();
                let (Some(u), None) = (upper.next(), upper.next()) else {
                    return c;
                };
                let mut lower = u.to_lowercase();
                match (lower.next(), lower.next()) {
                    (Some(l), None) => l,
                    _ => c,
                }
            }
        }
    }
    let mut a = left.chars();
    let mut b = right.chars();
    loop {
        match (a.next(), b.next()) {
            (None, None) => return true,
            (Some(x), Some(y)) if x == y || fold(x) == fold(y) => {}
            _ => return false,
        }
    }
}
