//! HTML elements, attributes, comments, doctype and text.

use swlsp_syntax::{CompletedMarker, ErrorBuilder, FollowingContent, Kind, Parser};

use super::tags::at_twig_termination_tag;
use super::twig::{parse_any_twig, parse_twig_var_statement};
use super::{
    GENERAL_RECOVERY_SET, ParseFn, TwigParserExt, parse_any_element, parse_ludtwig_directive,
    parse_many,
};
use crate::syntax::*;

const HTML_VOID_ELEMENTS: &[&str] = &[
    "area", "base", "br", "col", "command", "embed", "hr", "img", "input", "keygen", "link",
    "meta", "param", "source", "track", "wbr",
];

const HTML_RAW_TEXT_ELEMENTS: &[&str] = &["script", "style", "textarea", "title"];

fn is_name_continue(b: u8) -> bool {
    b.is_ascii_alphanumeric() || b == b'_' || b == b'-'
}

/// `^[a-zA-Z][a-zA-Z0-9\-]*$`
pub(crate) fn html_tag_name_matches(text: &str) -> bool {
    let bytes = text.as_bytes();
    bytes.first().is_some_and(u8::is_ascii_alphabetic)
        && bytes[1..]
            .iter()
            .all(|b| b.is_ascii_alphanumeric() || *b == b'-')
}

/// `^[a-zA-Z_][a-zA-Z0-9_\-]*$`
fn html_attribute_name_segment_matches(text: &str) -> bool {
    let bytes = text.as_bytes();
    bytes
        .first()
        .is_some_and(|b| b.is_ascii_alphabetic() || *b == b'_')
        && bytes[1..].iter().all(|b| is_name_continue(*b))
}

/// `^:?(?:[a-zA-Z_]|[@#_$][a-zA-Z])[a-zA-Z0-9_\-]*(?:[:.][a-zA-Z_][a-zA-Z0-9_\-]*)*$`
/// (Vue/Alpine-style `:bind`, `@click.stop`, `#slot`, `v-bind:prop.sync`).
pub(crate) fn html_attribute_name_matches(text: &str) -> bool {
    let bytes = text.as_bytes();
    let mut i = usize::from(bytes.first() == Some(&b':'));
    match bytes.get(i) {
        Some(b) if b.is_ascii_alphabetic() || *b == b'_' => i += 1,
        Some(b'@' | b'#' | b'$') if bytes.get(i + 1).is_some_and(u8::is_ascii_alphabetic) => i += 2,
        _ => return false,
    }
    while bytes.get(i).is_some_and(|b| is_name_continue(*b)) {
        i += 1;
    }
    while i < bytes.len() {
        if !matches!(bytes[i], b':' | b'.') {
            return false;
        }
        i += 1;
        if !bytes
            .get(i)
            .is_some_and(|b| b.is_ascii_alphabetic() || *b == b'_')
        {
            return false;
        }
        i += 1;
        while bytes.get(i).is_some_and(|b| is_name_continue(*b)) {
            i += 1;
        }
    }
    true
}

thread_local! {
    /// The HTML elements open at this point of the parse, innermost last;
    /// `None` marks the start of a Twig statement body. A closing tag
    /// that matches none of the elements opened in the current body is a
    /// stray one: `<div>{% if x %}</div>{% endif %}` closes nothing in the
    /// branch, and must not end the branch either.
    static OPEN_ELEMENTS: std::cell::RefCell<Vec<Option<String>>> =
        const { std::cell::RefCell::new(Vec::new()) };
}

/// Forgets the open elements of a previous parse.
pub(crate) fn reset_open_elements() {
    OPEN_ELEMENTS.with(|elements| elements.borrow_mut().clear());
}

/// Marks the start of a Twig statement body; [`end_twig_statement`] closes
/// it together with the elements left open inside.
pub(crate) fn begin_twig_statement() {
    OPEN_ELEMENTS.with(|elements| elements.borrow_mut().push(None));
}

pub(crate) fn end_twig_statement() {
    OPEN_ELEMENTS.with(|elements| {
        let mut elements = elements.borrow_mut();
        while let Some(entry) = elements.pop() {
            if entry.is_none() {
                break;
            }
        }
    });
}

fn open_element(name: &str) {
    OPEN_ELEMENTS.with(|elements| elements.borrow_mut().push(Some(name.to_lowercase())));
}

fn close_element() {
    OPEN_ELEMENTS.with(|elements| {
        let mut elements = elements.borrow_mut();
        if elements.last().is_some_and(Option::is_some) {
            elements.pop();
        }
    });
}

/// Whether no element opened in the current Twig body has `name`.
fn closes_nothing_here(name: &str) -> bool {
    let name = name.to_lowercase();
    OPEN_ELEMENTS.with(|elements| {
        !elements
            .borrow()
            .iter()
            .rev()
            .take_while(|entry| entry.is_some())
            .any(|entry| entry.as_deref() == Some(name.as_str()))
    })
}

/// A closing tag that closes nothing opened in the current Twig body: kept
/// as an error node so the body goes on.
fn parse_stray_ending_tag(p: &mut Parser<'_>) -> CompletedMarker {
    let m = p.start();
    let name = p
        .peek_nth_token(1)
        .map(|token| token.text.to_owned())
        .unwrap_or_default();
    p.add_error(ErrorBuilder::new(format!(
        "an element to close; </{name}> closes no element opened in this block or branch"
    )));
    p.bump();
    if p.at(TK_WORD) || p.at(TK_TWIG_COMPONENT_NAME) {
        p.bump();
    }
    if p.at(TK_GREATER_THAN) {
        p.bump();
    }
    p.complete(m, ERROR)
}

pub(crate) fn parse_any_html(p: &mut Parser<'_>) -> Option<CompletedMarker> {
    if p.at(TK_LESS_THAN_SLASH)
        && let Some(name) = p.peek_nth_token(1)
        && (name.kind == TK_WORD || name.kind == TK_TWIG_COMPONENT_NAME)
        && closes_nothing_here(name.text)
    {
        return Some(parse_stray_ending_tag(p));
    }
    if p.at(TK_LESS_THAN) {
        let next = p.peek_nth_token(1).map(|token| token.kind);
        // '<' followed by EOF, whitespace, a number or a recovery token is text.
        if next.is_some_and(|kind| {
            kind != TK_WHITESPACE && kind != TK_NUMBER && !GENERAL_RECOVERY_SET.contains(&kind)
        }) {
            return Some(parse_html_element(p));
        }
    }
    if p.at(TK_LESS_THAN_EXCLAMATION_MARK_MINUS_MINUS) {
        return Some(parse_html_comment(p));
    }
    if p.at(TK_LESS_THAN_EXCLAMATION_MARK) {
        return Some(parse_html_doctype(p));
    }
    parse_html_text(p)
}

fn parse_html_doctype(p: &mut Parser<'_>) -> CompletedMarker {
    let m = p.start();
    p.bump();
    p.expect(TK_DOCTYPE, &[TK_WORD, TK_GREATER_THAN]);
    p.expect(TK_WORD, &[TK_GREATER_THAN]);
    p.expect(TK_GREATER_THAN, &[]);
    p.complete(m, HTML_DOCTYPE)
}

/// '<' followed by whitespace, a number, a recovery token or EOF is text.
fn at_less_than_non_word(p: &Parser<'_>) -> bool {
    if !p.at(TK_LESS_THAN) {
        return false;
    }
    match p.peek_nth_token(1) {
        None => true,
        Some(next) => {
            next.kind == TK_WHITESPACE
                || next.kind == TK_NUMBER
                || GENERAL_RECOVERY_SET.contains(&next.kind)
        }
    }
}

fn at_html_text_end(p: &Parser<'_>) -> bool {
    p.at(TK_LESS_THAN_SLASH) || (p.at_set(GENERAL_RECOVERY_SET) && !at_less_than_non_word(p))
}

fn parse_html_text(p: &mut Parser<'_>) -> Option<CompletedMarker> {
    if p.at_end() || at_html_text_end(p) {
        return None;
    }
    let m = p.start();
    parse_many(
        p,
        |p| at_html_text_end(p),
        |p| {
            p.bump();
        },
    );
    Some(p.complete(m, HTML_TEXT))
}

/// Raw text nested inside a Twig body within a raw-text element.
fn parse_html_raw_text_inner(p: &mut Parser<'_>) -> Option<CompletedMarker> {
    let m = p.start();
    parse_many(p, |p| at_twig_termination_tag(p), raw_text_child);
    Some(p.complete(m, HTML_RAW_TEXT))
}

fn raw_text_child(p: &mut Parser<'_>) {
    // Guarded against EOF: parse_any_twig may consume up to EOF and still fail.
    if parse_any_twig(p, parse_html_raw_text_inner).is_none() && !p.at_end() {
        p.bump();
    }
}

/// Raw-text element content; returns whether the matching end tag was found.
fn parse_html_raw_text(p: &mut Parser<'_>, tag_kind: Kind, tag_name: &str) -> bool {
    let mut matching_end_tag = false;
    let m = p.start();
    parse_many(
        p,
        |p| {
            if p.at_following_content(&[
                FollowingContent {
                    kind: TK_LESS_THAN_SLASH,
                    text: None,
                },
                FollowingContent {
                    kind: tag_kind,
                    text: Some(tag_name),
                },
            ]) {
                matching_end_tag = true;
                return true;
            }
            at_twig_termination_tag(p)
        },
        raw_text_child,
    );
    p.complete(m, HTML_RAW_TEXT);
    matching_end_tag
}

fn parse_html_comment(p: &mut Parser<'_>) -> CompletedMarker {
    let m = p.start();
    p.bump();
    if p.at_set(&[TK_LUDTWIG_IGNORE_FILE, TK_LUDTWIG_IGNORE]) {
        return parse_ludtwig_directive(p, m, &[TK_MINUS_MINUS_GREATER_THAN]);
    }
    parse_many(
        p,
        |p| p.at(TK_MINUS_MINUS_GREATER_THAN),
        |p| {
            p.bump();
        },
    );
    p.expect(TK_MINUS_MINUS_GREATER_THAN, &[]);
    p.complete(m, HTML_COMMENT)
}

fn parse_html_element(p: &mut Parser<'_>) -> CompletedMarker {
    let m = p.start();
    let starting_tag = p.start();
    p.bump();

    let (tag_name, tag_kind) = p
        .peek_token()
        .map_or(("", TK_WORD), |token| (token.text, token.kind));
    let tag_name_lowercase = tag_name.to_lowercase();

    if tag_kind == TK_TWIG_COMPONENT_NAME {
        p.bump();
    } else if html_tag_name_matches(tag_name) {
        p.bump_as(TK_WORD);
    } else {
        p.add_error(ErrorBuilder::new("HTML Tag Name"));
        p.recover(&[
            TK_GREATER_THAN,
            TK_SLASH_GREATER_THAN,
            TK_LESS_THAN_SLASH,
            TK_WORD,
        ]);
    }

    let attributes = p.start();
    parse_many(
        p,
        |p| p.at(TK_GREATER_THAN) || p.at(TK_SLASH_GREATER_THAN),
        |p| {
            parse_html_attribute_or_twig(p);
        },
    );
    p.complete(attributes, HTML_ATTRIBUTE_LIST);

    let mut is_self_closing = if p.at(TK_SLASH_GREATER_THAN) {
        p.bump();
        true
    } else {
        p.expect(
            TK_GREATER_THAN,
            &[TK_LESS_THAN_SLASH, TK_WORD, TK_GREATER_THAN],
        );
        false
    };
    if HTML_VOID_ELEMENTS.contains(&tag_name_lowercase.as_str()) {
        is_self_closing = true;
    }
    p.complete(starting_tag, HTML_STARTING_TAG);

    if is_self_closing {
        return p.complete(m, HTML_TAG);
    }

    open_element(tag_name);
    let body = p.start();
    let matching_end_tag = if HTML_RAW_TEXT_ELEMENTS.contains(&tag_name_lowercase.as_str()) {
        parse_html_raw_text(p, tag_kind, tag_name)
    } else {
        let mut matching = false;
        parse_many(
            p,
            |p| {
                if p.at_following_content(&[
                    FollowingContent {
                        kind: TK_LESS_THAN_SLASH,
                        text: None,
                    },
                    FollowingContent {
                        kind: tag_kind,
                        text: Some(tag_name),
                    },
                ]) {
                    matching = true;
                    return true;
                }
                at_twig_termination_tag(p)
            },
            |p| {
                parse_any_element(p);
            },
        );
        matching
    };
    p.complete(body, BODY);

    let end_tag = p.start();
    if matching_end_tag {
        p.expect(TK_LESS_THAN_SLASH, &[tag_kind, TK_GREATER_THAN]);
        if p.at(TK_TWIG_COMPONENT_NAME) {
            p.bump();
        } else if p.at(tag_kind) {
            p.bump_as(TK_WORD);
        } else {
            p.add_error(ErrorBuilder::new(format!("{tag_name} as ending tag name")));
            p.recover(&[TK_GREATER_THAN]);
        }
        p.expect(TK_GREATER_THAN, &[]);
    } else {
        p.add_error(ErrorBuilder::new(format!("</{tag_name}> ending tag")));
        p.recover(&[]);
    }
    p.complete(end_tag, HTML_ENDING_TAG);
    close_element();

    p.complete(m, HTML_TAG)
}

/// One HTML attribute or a Twig construct in an attribute list.
fn parse_html_attribute_or_twig(p: &mut Parser<'_>) -> Option<CompletedMarker> {
    if p.at_twig_block_open() || p.at_twig_var_open() || p.at_twig_comment_open() {
        return parse_any_twig(p, parse_html_attribute_or_twig);
    }

    let (count, name) = html_attribute_name(p);
    if count == 0 {
        return None;
    }

    let attribute = if html_attribute_name_matches(&name) {
        let m = p.start();
        if count > 1 {
            p.bump_next_n_as(count, TK_WORD);
        } else {
            p.bump_as(TK_WORD);
        }
        m
    } else if p.at_twig_var_open() {
        let m = p.start();
        parse_twig_var_statement(p);
        m
    } else {
        return parse_any_twig(p, parse_html_attribute_or_twig);
    };

    if p.at(TK_EQUAL) {
        p.bump();
        parse_html_attribute_value_string(p);
    }
    Some(p.complete(attribute, HTML_ATTRIBUTE))
}

/// The raw-token count and text of an attribute name at the cursor
/// (`:name`, `name`, plus `:arg` / `.modifier` segments).
fn html_attribute_name(p: &Parser<'_>) -> (usize, String) {
    let Some(first) = p.peek_token() else {
        return (0, String::new());
    };
    let mut count = 1;
    let mut name = String::new();
    if first.kind == TK_COLON {
        match p.peek_nth_token(1) {
            Some(word) if html_attribute_name_segment_matches(word.text) => {
                count = 2;
                name.push(':');
                name.push_str(word.text);
            }
            _ => return (0, String::new()),
        }
    } else {
        // Reserved words such as "style" or "for" are ordinary name segments here.
        if !html_attribute_name_matches(first.text) {
            return (0, String::new());
        }
        name.push_str(first.text);
    }
    while let (Some(separator), Some(word)) = (p.peek_nth_token(count), p.peek_nth_token(count + 1))
    {
        if !html_attribute_name_segment_matches(word.text)
            || (separator.kind != TK_COLON && separator.kind != TK_DOT)
        {
            break;
        }
        name.push_str(separator.text);
        name.push_str(word.text);
        count += 2;
    }
    (count, name)
}

fn parse_html_attribute_value_string(p: &mut Parser<'_>) -> CompletedMarker {
    let m = p.start();
    let quote_kind = if p.at_set(&[TK_DOUBLE_QUOTES, TK_SINGLE_QUOTES]) {
        Some(p.bump().kind)
    } else {
        None
    };

    let inner = p.start();
    match quote_kind {
        Some(TK_DOUBLE_QUOTES) => {
            inner_double_quote_parser(p);
        }
        Some(_) => {
            inner_single_quote_parser(p);
        }
        None => inner_no_quote_parser(p),
    }
    if quote_kind.is_some() {
        p.explicitly_consume_trivia();
    }
    p.complete(inner, HTML_STRING_INNER);

    if let Some(quote_kind) = quote_kind {
        p.expect(quote_kind, &[TK_GREATER_THAN, TK_SLASH_GREATER_THAN]);
    } else if p.at_set(&[TK_DOUBLE_QUOTES, TK_SINGLE_QUOTES]) {
        let error = p.start();
        let quote = p.bump();
        p.add_error(
            ErrorBuilder::new("no trailing quote because there is no leading quote")
                .at_token(&quote),
        );
        p.complete(error, ERROR);
    }
    p.complete(m, HTML_STRING)
}

fn inner_double_quote_parser(p: &mut Parser<'_>) -> Option<CompletedMarker> {
    inner_quote_parser(p, TK_DOUBLE_QUOTES, inner_double_quote_parser);
    None
}

fn inner_single_quote_parser(p: &mut Parser<'_>) -> Option<CompletedMarker> {
    inner_quote_parser(p, TK_SINGLE_QUOTES, inner_single_quote_parser);
    None
}

fn inner_quote_parser(p: &mut Parser<'_>, quote: Kind, itself: ParseFn) {
    parse_many(
        p,
        |p| p.at(quote) || at_twig_termination_tag(p),
        |p| attribute_string_child(p, itself),
    );
}

/// Exactly one word or one `{{ var }}`.
fn inner_no_quote_parser(p: &mut Parser<'_>) {
    if p.at(TK_WORD) {
        p.bump();
    } else if p.at_twig_var_open() {
        parse_twig_var_statement(p);
    } else {
        p.add_error(ErrorBuilder::new("html attribute value"));
        p.recover(&[TK_WORD, TK_GREATER_THAN, TK_SLASH_GREATER_THAN]);
    }
}

/// Offers each position inside a quoted attribute value to Twig, else bumps
/// one raw token. HTML openers and `>` are ordinary data inside quotes.
fn attribute_string_child(p: &mut Parser<'_>, inner: ParseFn) {
    if parse_any_twig(p, inner).is_some() {
        return;
    }
    if p.at_set(&[
        TK_LESS_THAN,
        TK_LESS_THAN_EXCLAMATION_MARK,
        TK_LESS_THAN_EXCLAMATION_MARK_MINUS_MINUS,
    ]) {
        p.bump();
        return;
    }
    if p.at_set(GENERAL_RECOVERY_SET) || p.at_end() {
        return;
    }
    p.bump();
}
