//! Mode-less, maximal-munch tokenizer for the combined HTML + Twig grammar
//! (port of `internal/parser/twig/lexer`).
//!
//! The lexer is context free: the whole file is tokenized with one flat token
//! set. The parser decides contextually what each token means and may re-label
//! keyword tokens as words. Unlexable input becomes `TK_UNKNOWN`, one token per
//! character.

use swlsp_syntax::{Kind, Token};

use crate::syntax::*;

/// Tokenizes the whole source. Every byte ends up in exactly one token.
pub fn lex(source: &str) -> Vec<Token<'_>> {
    let bytes = source.as_bytes();
    let mut tokens = Vec::with_capacity(source.len() / 4);
    let mut position = 0;
    while position < bytes.len() {
        let (kind, length) = next(source, bytes, position);
        let end = position + length;
        tokens.push(Token::new(kind, source, position as u32, end as u32));
        position = end;
    }
    tokens
}

fn next(source: &str, bytes: &[u8], pos: usize) -> (Kind, usize) {
    let c = bytes[pos];
    match c {
        b' ' | b'\t' => (
            TK_WHITESPACE,
            scan_while(bytes, pos, |b| b == b' ' || b == b'\t'),
        ),
        b'\n' | b'\r' => match scan_line_break(bytes, pos) {
            0 => unknown(source, pos),
            n => (TK_LINE_BREAK, n),
        },
        b'0'..=b'9' => (TK_NUMBER, scan_number(bytes, pos)),
        b'a'..=b'z' | b'A'..=b'Z' => classify_word(source, bytes, pos),
        b'@' | b'$' => match scan_word(bytes, pos) {
            0 => unknown(source, pos),
            n => (TK_WORD, n),
        },
        b'_' => (TK_WORD, scan_word(bytes, pos)),
        b'#' => match scan_word(bytes, pos) {
            0 => match_punctuation(source, bytes, pos),
            n => (TK_WORD, n),
        },
        b'&' => match scan_html_escape(bytes, pos) {
            0 => match_punctuation(source, bytes, pos),
            n => (TK_HTML_ESCAPE_CHARACTER, n),
        },
        _ => match_punctuation(source, bytes, pos),
    }
}

/// One `TK_UNKNOWN` token spanning a single character.
fn unknown(source: &str, pos: usize) -> (Kind, usize) {
    let size = source[pos..].chars().next().map_or(1, char::len_utf8);
    (TK_UNKNOWN, size)
}

fn scan_while(bytes: &[u8], pos: usize, predicate: impl Fn(u8) -> bool) -> usize {
    bytes[pos..].iter().take_while(|b| predicate(**b)).count()
}

/// `((\n)|(\r\n))+`; a lone `\r` returns 0.
fn scan_line_break(bytes: &[u8], pos: usize) -> usize {
    let mut i = pos;
    while i < bytes.len() {
        if bytes[i] == b'\n' {
            i += 1;
        } else if bytes[i] == b'\r' && bytes.get(i + 1) == Some(&b'\n') {
            i += 2;
        } else {
            break;
        }
    }
    i - pos
}

/// `[0-9]+(\.[0-9]+)?([Ee][+\-][0-9]+)?`.
fn scan_number(bytes: &[u8], pos: usize) -> usize {
    let digit = |i: usize| bytes.get(i).is_some_and(u8::is_ascii_digit);
    let mut i = pos;
    while digit(i) {
        i += 1;
    }
    if bytes.get(i) == Some(&b'.') && digit(i + 1) {
        i += 1;
        while digit(i) {
            i += 1;
        }
    }
    if matches!(bytes.get(i), Some(b'e' | b'E'))
        && matches!(bytes.get(i + 1), Some(b'+' | b'-'))
        && digit(i + 2)
    {
        i += 2;
        while digit(i) {
            i += 1;
        }
    }
    i - pos
}

/// `&(([a-zA-Z][a-zA-Z0-9]*)|(#[0-9]+)|(#x[0-9a-fA-F]+));`, 0 if no match.
fn scan_html_escape(bytes: &[u8], pos: usize) -> usize {
    let mut i = pos + 1;
    let Some(&first) = bytes.get(i) else {
        return 0;
    };
    if first.is_ascii_alphabetic() {
        i += 1;
        while bytes.get(i).is_some_and(u8::is_ascii_alphanumeric) {
            i += 1;
        }
    } else if first == b'#' {
        i += 1;
        let hex = bytes.get(i) == Some(&b'x');
        if hex {
            i += 1;
        }
        let start = i;
        while bytes.get(i).is_some_and(|b| {
            if hex {
                b.is_ascii_hexdigit()
            } else {
                b.is_ascii_digit()
            }
        }) {
            i += 1;
        }
        if i == start {
            return 0;
        }
    } else {
        return 0;
    }
    if bytes.get(i) == Some(&b';') {
        i + 1 - pos
    } else {
        0
    }
}

fn is_word_continue(b: u8) -> bool {
    b.is_ascii_alphanumeric() || b == b'_' || b == b'-'
}

/// `([a-zA-Z]|([@#_$][a-zA-Z])|_)[a-zA-Z0-9_\-]*`, 0 if no word starts here.
fn scan_word(bytes: &[u8], pos: usize) -> usize {
    let c = bytes[pos];
    let mut i = pos;
    if c.is_ascii_alphabetic() || c == b'_' {
        i += 1;
    } else if matches!(c, b'@' | b'#' | b'$') {
        if !bytes.get(pos + 1).is_some_and(u8::is_ascii_alphabetic) {
            return 0;
        }
        i += 2;
    } else {
        return 0;
    }
    while bytes.get(i).is_some_and(|b| is_word_continue(*b)) {
        i += 1;
    }
    i - pos
}

fn classify_word(source: &str, bytes: &[u8], pos: usize) -> (Kind, usize) {
    let length = scan_word(bytes, pos);
    let word = &source[pos..pos + length];
    if word == "twig" {
        let tail = scan_twig_component_tail(bytes, pos + length);
        if tail > 0 {
            return (TK_TWIG_COMPONENT_NAME, length + tail);
        }
    }
    if let Some((kind, n)) = match_multi_word(bytes, pos + length, word) {
        return (kind, length + n);
    }
    if let Some(kind) = keyword(word) {
        return (kind, length);
    }
    if let Some(kind) = case_insensitive_keyword(word) {
        return (kind, length);
    }
    (TK_WORD, length)
}

/// `(:[a-zA-Z0-9_\-]+)+` starting at `pos`.
fn scan_twig_component_tail(bytes: &[u8], pos: usize) -> usize {
    let mut i = pos;
    while bytes.get(i) == Some(&b':') {
        let mut j = i + 1;
        while bytes.get(j).is_some_and(|b| is_word_continue(*b)) {
            j += 1;
        }
        if j == i + 1 {
            break;
        }
        i = j;
    }
    i - pos
}

fn has_literal(bytes: &[u8], at: usize, literal: &str) -> bool {
    bytes.get(at..at + literal.len()) == Some(literal.as_bytes())
}

fn match_multi_word(bytes: &[u8], after: usize, word: &str) -> Option<(Kind, usize)> {
    // `not in` / `is not` require a following space, tab, line feed or EOF.
    let bounded = |suffix: &str| {
        has_literal(bytes, after, suffix)
            && matches!(
                bytes.get(after + suffix.len()),
                None | Some(b' ' | b'\t' | b'\n')
            )
    };
    let (kind, suffix) = match word {
        "not" if bounded(" in") => (TK_NOT_IN, " in"),
        "is" if bounded(" not") => (TK_IS_NOT, " not"),
        "starts" => (TK_STARTS_WITH, " with"),
        "ends" => (TK_ENDS_WITH, " with"),
        "same" => (TK_SAME_AS, " as"),
        "divisible" => (TK_DIVISIBLE_BY, " by"),
        "ignore" => (TK_IGNORE_MISSING, " missing"),
        _ => return None,
    };
    has_literal(bytes, after, suffix).then_some((kind, suffix.len()))
}

fn keyword(word: &str) -> Option<Kind> {
    Some(match word {
        "block" => TK_BLOCK,
        "endblock" => TK_ENDBLOCK,
        "if" => TK_IF,
        "elseif" => TK_ELSE_IF,
        "else" => TK_ELSE,
        "endif" => TK_ENDIF,
        "apply" => TK_APPLY,
        "endapply" => TK_ENDAPPLY,
        "autoescape" => TK_AUTOESCAPE,
        "endautoescape" => TK_ENDAUTOESCAPE,
        "cache" => TK_CACHE,
        "endcache" => TK_ENDCACHE,
        "deprecated" => TK_DEPRECATED,
        "do" => TK_DO,
        "embed" => TK_EMBED,
        "endembed" => TK_ENDEMBED,
        "extends" => TK_EXTENDS,
        "flush" => TK_FLUSH,
        "for" => TK_FOR,
        "endfor" => TK_ENDFOR,
        "from" => TK_FROM,
        "import" => TK_IMPORT,
        "macro" => TK_MACRO,
        "endmacro" => TK_ENDMACRO,
        "sandbox" => TK_SANDBOX,
        "endsandbox" => TK_ENDSANDBOX,
        "set" => TK_SET,
        "endset" => TK_ENDSET,
        "use" => TK_USE,
        "verbatim" => TK_VERBATIM,
        "endverbatim" => TK_ENDVERBATIM,
        "only" => TK_ONLY,
        "with" => TK_WITH,
        "endwith" => TK_ENDWITH,
        "ttl" => TK_TTL,
        "tags" => TK_TAGS,
        "props" => TK_PROPS,
        "component" => TK_COMPONENT,
        "endcomponent" => TK_ENDCOMPONENT,
        "not" => TK_NOT,
        "or" => TK_OR,
        "and" => TK_AND,
        "b-or" => TK_BINARY_OR,
        "b-xor" => TK_BINARY_XOR,
        "b-and" => TK_BINARY_AND,
        "in" => TK_IN,
        "matches" => TK_MATCHES,
        "is" => TK_IS,
        "even" => TK_EVEN,
        "odd" => TK_ODD,
        "defined" => TK_DEFINED,
        "as" => TK_AS,
        "constant" => TK_CONSTANT,
        "empty" => TK_EMPTY,
        "iterable" => TK_ITERABLE,
        "max" => TK_MAX,
        "min" => TK_MIN,
        "range" => TK_RANGE,
        "cycle" => TK_CYCLE,
        "random" => TK_RANDOM,
        "date" => TK_DATE,
        "include" => TK_INCLUDE,
        "source" => TK_SOURCE,
        "trans" => TK_TRANS,
        "endtrans" => TK_ENDTRANS,
        "sw_extends" => TK_SW_EXTENDS,
        "sw_silent_feature_call" => TK_SW_SILENT_FEATURE_CALL,
        "endsw_silent_feature_call" => TK_ENDSW_SILENT_FEATURE_CALL,
        "sw_include" => TK_SW_INCLUDE,
        "return" => TK_RETURN,
        "sw_icon" => TK_SW_ICON,
        "sw_thumbnails" => TK_SW_THUMBNAILS,
        "style" => TK_STYLE,
        "sw_embed" => TK_SW_EMBED,
        "sw_end_embed" => TK_SW_END_EMBED,
        "sw_use" => TK_SW_USE,
        "sw_import" => TK_SW_IMPORT,
        "sw_from" => TK_SW_FROM,
        _ => return None,
    })
}

fn case_insensitive_keyword(word: &str) -> Option<Kind> {
    const KEYWORDS: &[(&str, Kind)] = &[
        ("true", TK_TRUE),
        ("none", TK_NONE),
        ("null", TK_NULL),
        ("false", TK_FALSE),
        ("doctype", TK_DOCTYPE),
        ("ludtwig-ignore", TK_LUDTWIG_IGNORE),
        ("ludtwig-ignore-file", TK_LUDTWIG_IGNORE_FILE),
    ];
    KEYWORDS
        .iter()
        .find(|(text, _)| word.eq_ignore_ascii_case(text))
        .map(|(_, kind)| *kind)
}

/// Punctuation literals grouped by first byte, longest first.
fn lookup_punctuation(bytes: &[u8], pos: usize) -> Option<(Kind, usize)> {
    let candidates: &[(&str, Kind)] = match bytes[pos] {
        b'.' => &[("...", TK_TRIPLE_DOT), ("..", TK_DOUBLE_DOT), (".", TK_DOT)],
        b',' => &[(",", TK_COMMA)],
        b':' => &[(":", TK_COLON)],
        b';' => &[(";", TK_SEMICOLON)],
        b'!' => &[
            ("!==", TK_EXCLAMATION_MARK_DOUBLE_EQUALS),
            ("!=", TK_EXCLAMATION_MARK_EQUALS),
            ("!", TK_EXCLAMATION_MARK),
        ],
        b'?' => &[("??", TK_DOUBLE_QUESTION_MARK), ("?", TK_QUESTION_MARK)],
        b'%' => &[("%}", TK_PERCENT_CURLY), ("%", TK_PERCENT)],
        b'~' => &[
            ("~%}", TK_TILDE_PERCENT_CURLY),
            ("~}}", TK_TILDE_CLOSE_CURLY_CURLY),
            ("~#}", TK_TILDE_HASHTAG_CLOSE_CURLY),
            ("~", TK_TILDE),
        ],
        b'|' => &[("||", TK_DOUBLE_PIPE), ("|", TK_SINGLE_PIPE)],
        b'&' => &[("&&", TK_DOUBLE_AMPERSAND), ("&", TK_AMPERSAND)],
        b'/' => &[
            ("//", TK_DOUBLE_FORWARD_SLASH),
            ("/>", TK_SLASH_GREATER_THAN),
            ("/", TK_FORWARD_SLASH),
        ],
        b'\\' => &[("\\", TK_BACKWARD_SLASH)],
        b'(' => &[("(", TK_OPEN_PARENTHESIS)],
        b')' => &[(")", TK_CLOSE_PARENTHESIS)],
        b'{' => &[
            ("{%-", TK_CURLY_PERCENT_MINUS),
            ("{%~", TK_CURLY_PERCENT_TILDE),
            ("{{-", TK_OPEN_CURLY_CURLY_MINUS),
            ("{{~", TK_OPEN_CURLY_CURLY_TILDE),
            ("{#-", TK_OPEN_CURLY_HASHTAG_MINUS),
            ("{#~", TK_OPEN_CURLY_HASHTAG_TILDE),
            ("{%", TK_CURLY_PERCENT),
            ("{{", TK_OPEN_CURLY_CURLY),
            ("{#", TK_OPEN_CURLY_HASHTAG),
            ("{", TK_OPEN_CURLY),
        ],
        b'}' => &[("}}", TK_CLOSE_CURLY_CURLY), ("}", TK_CLOSE_CURLY)],
        b'[' => &[("[", TK_OPEN_SQUARE)],
        b']' => &[("]", TK_CLOSE_SQUARE)],
        b'<' => &[
            ("<!--", TK_LESS_THAN_EXCLAMATION_MARK_MINUS_MINUS),
            ("<=>", TK_LESS_THAN_EQUAL_GREATER_THAN),
            ("<=", TK_LESS_THAN_EQUAL),
            ("</", TK_LESS_THAN_SLASH),
            ("<!", TK_LESS_THAN_EXCLAMATION_MARK),
            ("<", TK_LESS_THAN),
        ],
        b'>' => &[(">=", TK_GREATER_THAN_EQUAL), (">", TK_GREATER_THAN)],
        b'=' => &[
            ("===", TK_TRIPLE_EQUAL),
            ("=>", TK_EQUAL_GREATER_THAN),
            ("==", TK_DOUBLE_EQUAL),
            ("=", TK_EQUAL),
        ],
        b'-' => &[
            ("-->", TK_MINUS_MINUS_GREATER_THAN),
            ("-%}", TK_MINUS_PERCENT_CURLY),
            ("-}}", TK_MINUS_CLOSE_CURLY_CURLY),
            ("-#}", TK_MINUS_HASHTAG_CLOSE_CURLY),
            ("-", TK_MINUS),
        ],
        b'+' => &[("+", TK_PLUS)],
        b'*' => &[("**", TK_DOUBLE_STAR), ("*", TK_STAR)],
        b'"' => &[("\"", TK_DOUBLE_QUOTES)],
        b'\'' => &[("'", TK_SINGLE_QUOTES)],
        b'`' => &[("`", TK_GRAVE_ACCENT_QUOTES)],
        b'#' => &[("#}", TK_HASHTAG_CLOSE_CURLY), ("#", TK_HASHTAG)],
        _ => return None,
    };
    candidates
        .iter()
        .find(|(text, _)| has_literal(bytes, pos, text))
        .map(|(text, kind)| (*kind, text.len()))
}

fn match_punctuation(source: &str, bytes: &[u8], pos: usize) -> (Kind, usize) {
    lookup_punctuation(bytes, pos).unwrap_or_else(|| unknown(source, pos))
}

#[cfg(test)]
mod tests;
