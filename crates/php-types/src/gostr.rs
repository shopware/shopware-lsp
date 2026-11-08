//! Byte-compatible ports of the Go string helpers whose output is visible to
//! users (`strconv.Quote`) or affects parsing (`strconv.Unquote`,
//! `strings.EqualFold`, `strings.ToLower`).

use unicode_general_category::{GeneralCategory, get_general_category};

/// Port of Go's `strconv.Quote`.
pub fn go_quote(value: &str) -> String {
    let mut out = String::with_capacity(value.len() + 2);
    write_go_quote(&mut out, value);
    out
}

/// Appends the Go `strconv.Quote` rendering of `value` to `out`.
pub fn write_go_quote(out: &mut String, value: &str) {
    use std::fmt::Write;
    out.push('"');
    for character in value.chars() {
        match character {
            '"' => out.push_str("\\\""),
            '\\' => out.push_str("\\\\"),
            _ if go_is_print(character) => out.push(character),
            '\u{7}' => out.push_str("\\a"),
            '\u{8}' => out.push_str("\\b"),
            '\u{c}' => out.push_str("\\f"),
            '\n' => out.push_str("\\n"),
            '\r' => out.push_str("\\r"),
            '\t' => out.push_str("\\t"),
            '\u{b}' => out.push_str("\\v"),
            _ => {
                let code = character as u32;
                if code < 0x20 || code == 0x7f {
                    let _ = write!(out, "\\x{code:02x}");
                } else if code < 0x10000 {
                    let _ = write!(out, "\\u{code:04x}");
                } else {
                    let _ = write!(out, "\\U{code:08x}");
                }
            }
        }
    }
    out.push('"');
}

/// Port of Go's `strconv.IsPrint`: letters, marks, numbers, punctuation,
/// symbols, and the ASCII space.
pub fn go_is_print(character: char) -> bool {
    if character.is_ascii() {
        return (' '..='~').contains(&character);
    }
    use GeneralCategory::*;
    matches!(
        get_general_category(character),
        UppercaseLetter
            | LowercaseLetter
            | TitlecaseLetter
            | ModifierLetter
            | OtherLetter
            | NonspacingMark
            | SpacingMark
            | EnclosingMark
            | DecimalNumber
            | LetterNumber
            | OtherNumber
            | ConnectorPunctuation
            | DashPunctuation
            | OpenPunctuation
            | ClosePunctuation
            | InitialPunctuation
            | FinalPunctuation
            | OtherPunctuation
            | MathSymbol
            | CurrencySymbol
            | ModifierSymbol
            | OtherSymbol
    )
}

/// Port of Go's `strconv.Unquote` for double- and single-quoted input.
///
/// Go strings may hold arbitrary bytes produced by `\x` or octal escapes;
/// such invalid UTF-8 is replaced with U+FFFD here.
pub fn go_unquote(input: &str) -> Option<String> {
    let bytes = input.as_bytes();
    if bytes.len() < 2 {
        return None;
    }
    let quote = bytes[0];
    if (quote != b'"' && quote != b'\'') || bytes[bytes.len() - 1] != quote {
        return None;
    }
    let mut rest = &bytes[1..];
    let mut buffer = Vec::with_capacity(bytes.len());
    while !rest.is_empty() && rest[0] != quote {
        if rest[0] == b'\n' {
            return None;
        }
        rest = unquote_char(rest, quote, &mut buffer)?;
        if quote == b'\'' {
            break;
        }
    }
    if rest.len() != 1 || rest[0] != quote {
        return None;
    }
    if quote == b'\'' && buffer.is_empty() && bytes.len() == 2 {
        return None;
    }
    Some(match String::from_utf8(buffer) {
        Ok(value) => value,
        Err(error) => String::from_utf8_lossy(error.as_bytes()).into_owned(),
    })
}

fn unquote_char<'a>(input: &'a [u8], quote: u8, out: &mut Vec<u8>) -> Option<&'a [u8]> {
    let first = input[0];
    if first == quote {
        return None;
    }
    if first >= 0x80 {
        let length = utf8_sequence_length(first).min(input.len());
        out.extend_from_slice(&input[..length]);
        return Some(&input[length..]);
    }
    if first != b'\\' {
        out.push(first);
        return Some(&input[1..]);
    }
    if input.len() <= 1 {
        return None;
    }
    let escape = input[1];
    let rest = &input[2..];
    let simple = match escape {
        b'a' => Some(7),
        b'b' => Some(8),
        b'f' => Some(12),
        b'n' => Some(b'\n'),
        b'r' => Some(b'\r'),
        b't' => Some(b'\t'),
        b'v' => Some(11),
        b'\\' => Some(b'\\'),
        b'\'' | b'"' => {
            if escape != quote {
                return None;
            }
            Some(escape)
        }
        _ => None,
    };
    if let Some(value) = simple {
        out.push(value);
        return Some(rest);
    }
    match escape {
        b'x' | b'u' | b'U' => {
            let digits = match escape {
                b'x' => 2,
                b'u' => 4,
                _ => 8,
            };
            if rest.len() < digits {
                return None;
            }
            let mut value: u32 = 0;
            for &digit in &rest[..digits] {
                value = (value << 4) | (digit as char).to_digit(16)?;
            }
            if escape == b'x' {
                out.push(value as u8);
            } else {
                let character = char::from_u32(value)?;
                let mut encoded = [0u8; 4];
                out.extend_from_slice(character.encode_utf8(&mut encoded).as_bytes());
            }
            Some(&rest[digits..])
        }
        b'0'..=b'7' => {
            if rest.len() < 2 {
                return None;
            }
            let mut value = u32::from(escape - b'0');
            for &digit in &rest[..2] {
                if !(b'0'..=b'7').contains(&digit) {
                    return None;
                }
                value = (value << 3) | u32::from(digit - b'0');
            }
            if value > 255 {
                return None;
            }
            out.push(value as u8);
            Some(&rest[2..])
        }
        _ => None,
    }
}

fn utf8_sequence_length(first: u8) -> usize {
    match first {
        0xf0..=0xff => 4,
        0xe0..=0xef => 3,
        0xc0..=0xdf => 2,
        _ => 1,
    }
}

/// Port of Go's per-rune `unicode.ToLower` (simple case mapping).
pub fn go_lower_char(character: char) -> char {
    if character.is_ascii() {
        return character.to_ascii_lowercase();
    }
    if character == '\u{130}' {
        return 'i';
    }
    let mut lower = character.to_lowercase();
    match (lower.next(), lower.next()) {
        (Some(value), None) => value,
        _ => character,
    }
}

/// Port of Go's `strings.ToLower`.
pub fn go_to_lower(value: &str) -> String {
    if value.is_ascii() {
        return value.to_ascii_lowercase();
    }
    value.chars().map(go_lower_char).collect()
}

fn fold_char(character: char) -> char {
    match character {
        '\u{17f}' => 's',
        '\u{212a}' => 'k',
        '\u{2126}' => '\u{3c9}',
        _ => go_lower_char(character),
    }
}

/// Approximation of Go's `strings.EqualFold` (Unicode simple case folding).
pub fn equal_fold(left: &str, right: &str) -> bool {
    if left.is_ascii() && right.is_ascii() {
        return left.eq_ignore_ascii_case(right);
    }
    let mut left_chars = left.chars();
    let mut right_chars = right.chars();
    loop {
        match (left_chars.next(), right_chars.next()) {
            (None, None) => return true,
            (Some(a), Some(b)) => {
                if a != b && fold_char(a) != fold_char(b) {
                    return false;
                }
            }
            _ => return false,
        }
    }
}

/// Go's `unicode.IsSpace(rune(byte))`, used by byte-oriented scanners.
pub(crate) fn is_space_byte(value: u8) -> bool {
    matches!(
        value,
        b'\t' | b'\n' | 0x0b | 0x0c | b'\r' | b' ' | 0x85 | 0xa0
    )
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn quote_matches_go() {
        assert_eq!(go_quote("line\n\"quoted\""), "\"line\\n\\\"quoted\\\"\"");
        assert_eq!(go_quote("ä"), "\"ä\"");
        assert_eq!(go_quote("\u{a0}\u{7f}\u{1}"), "\"\\u00a0\\x7f\\x01\"");
        assert_eq!(go_quote("\u{e000}"), "\"\\ue000\"");
    }

    #[test]
    fn unquote_matches_go() {
        assert_eq!(go_unquote("\"a\\\"b\"").as_deref(), Some("a\"b"));
        assert_eq!(go_unquote("'a'").as_deref(), Some("a"));
        assert_eq!(go_unquote("'ab'"), None);
        assert_eq!(go_unquote("''"), None);
        assert_eq!(go_unquote("\"\\u00e4\"").as_deref(), Some("ä"));
        assert_eq!(go_unquote("\"\\'\""), None);
        assert_eq!(go_unquote("\"\\101\"").as_deref(), Some("A"));
    }

    #[test]
    fn equal_fold_matches_go() {
        assert!(equal_fold("aſ", "AS"));
        assert!(equal_fold("Ärger", "äRGER"));
        assert!(!equal_fold("abc", "abd"));
    }
}
