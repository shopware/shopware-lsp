//! Quoted string literals of the canonical type syntax.
//!
//! Literal string types (`"foo"`) and quoted array-shape keys are stored and
//! rendered in this form, and the type parser reads it back, so the escaping
//! is part of a type's identity and of persisted type text. Changing it
//! requires an index version bump.
//!
//! A literal is written in double quotes. `"` and `\` are backslash-escaped,
//! printable characters stay verbatim, and every other character uses
//! `\a \b \f \n \r \t \v`, `\xHH` (remaining ASCII controls), `\uHHHH` or
//! `\UHHHHHHHH`.

use std::fmt::Write;

use unicode_general_category::{GeneralCategory, get_general_category};

/// `value` as a double-quoted literal.
pub fn quote_literal(value: &str) -> String {
    let mut out = String::with_capacity(value.len() + 2);
    write_quoted_literal(&mut out, value);
    out
}

/// Appends `value` as a double-quoted literal to `out`.
pub(crate) fn write_quoted_literal(out: &mut String, value: &str) {
    out.push('"');
    for character in value.chars() {
        match character {
            '"' => out.push_str("\\\""),
            '\\' => out.push_str("\\\\"),
            _ if is_printable(character) => out.push(character),
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

/// Whether `character` is written verbatim: letters, marks, numbers,
/// punctuation, symbols, and the ASCII space.
fn is_printable(character: char) -> bool {
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

/// Decodes a literal written in double or single quotes.
///
/// Accepts the escapes [`quote_literal`] writes, three-digit octal escapes
/// (`\101`), and `\'` inside single quotes. A single-quoted literal holds
/// exactly one character or escape. Returns `None` for mismatched quotes, a
/// raw newline, an unknown escape, or the other quote character escaped.
/// Bytes from `\x` or octal escapes that do not form valid UTF-8 are replaced
/// with U+FFFD.
pub fn unquote_literal(input: &str) -> Option<String> {
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

/// Whitespace between type tokens: ASCII whitespace plus the single bytes
/// 0x85 (NEL) and 0xa0 (NBSP).
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
    fn quote_escapes_quotes_controls_and_unprintable_characters() {
        assert_eq!(
            quote_literal("line\n\"quoted\""),
            "\"line\\n\\\"quoted\\\"\""
        );
        assert_eq!(quote_literal("ä"), "\"ä\"");
        assert_eq!(quote_literal("\u{a0}\u{7f}\u{1}"), "\"\\u00a0\\x7f\\x01\"");
        assert_eq!(quote_literal("\u{e000}"), "\"\\ue000\"");
    }

    #[test]
    fn unquote_decodes_escapes_and_rejects_malformed_literals() {
        assert_eq!(unquote_literal("\"a\\\"b\"").as_deref(), Some("a\"b"));
        assert_eq!(unquote_literal("'a'").as_deref(), Some("a"));
        assert_eq!(unquote_literal("'ab'"), None);
        assert_eq!(unquote_literal("''"), None);
        assert_eq!(unquote_literal("\"\\u00e4\"").as_deref(), Some("ä"));
        assert_eq!(unquote_literal("\"\\'\""), None);
        assert_eq!(unquote_literal("\"\\101\"").as_deref(), Some("A"));
    }
}
