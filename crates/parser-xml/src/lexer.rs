//! Lossless XML lexer. XML declarations, comments, CDATA, doctypes, entities
//! and malformed fragments all stay in the token stream so editor-time parses
//! remain lossless.
//!
//! Every token boundary falls on an ASCII byte (non-ASCII bytes are name
//! characters in tag mode and plain text in data mode), so tokens never split
//! a UTF-8 character.

use swlsp_syntax::{Kind, Token};

use crate::syntax::*;

#[derive(Clone, Copy, PartialEq, Eq)]
enum Mode {
    Data,
    Tag,
}

pub fn lex(source: &str) -> Vec<Token<'_>> {
    let bytes = source.as_bytes();
    let mut tokens = Vec::with_capacity(source.len() / 4 + 1);
    let mut mode = Mode::Data;
    let mut position = 0;
    while position < bytes.len() {
        let start = position;
        let kind: Kind;
        if mode == Mode::Data {
            let rest = &bytes[position..];
            if rest.starts_with(b"<!--") {
                kind = TK_COMMENT;
                position = scan_until(bytes, position + 4, b"-->");
            } else if rest.starts_with(b"<![CDATA[") {
                kind = TK_CDATA;
                position = scan_until(bytes, position + 9, b"]]>");
            } else if rest.starts_with(b"<?") {
                kind = TK_PROCESSING_INSTRUCTION;
                position = scan_until(bytes, position + 2, b"?>");
            } else if rest.len() >= 9 && rest[..9].eq_ignore_ascii_case(b"<!DOCTYPE") {
                kind = TK_DOCTYPE;
                position = scan_doctype(bytes, position + 9);
            } else if rest.starts_with(b"</") {
                kind = TK_OPEN_END_TAG;
                position += 2;
                mode = Mode::Tag;
            } else if rest[0] == b'<' {
                kind = TK_OPEN_TAG;
                position += 1;
                mode = Mode::Tag;
            } else if rest[0] == b'&' {
                kind = TK_ENTITY_REFERENCE;
                position = scan_entity(bytes, position);
            } else {
                kind = TK_TEXT;
                position = memchr::memchr2(b'<', b'&', &bytes[position + 1..])
                    .map_or(bytes.len(), |offset| position + 1 + offset);
            }
        } else {
            match bytes[position] {
                b'/' if bytes.get(position + 1) == Some(&b'>') => {
                    kind = TK_CLOSE_EMPTY_TAG;
                    position += 2;
                    mode = Mode::Data;
                }
                b'>' => {
                    kind = TK_CLOSE_TAG;
                    position += 1;
                    mode = Mode::Data;
                }
                b'=' => {
                    kind = TK_EQUALS;
                    position += 1;
                }
                quote @ (b'"' | b'\'') => {
                    kind = TK_ATTRIBUTE_VALUE;
                    position = memchr::memchr(quote, &bytes[position + 1..])
                        .map_or(bytes.len(), |offset| position + 1 + offset + 1);
                }
                b'\r' | b'\n' => {
                    kind = TK_LINE_BREAK;
                    position += 1;
                    if bytes[start] == b'\r' && bytes.get(position) == Some(&b'\n') {
                        position += 1;
                    }
                }
                byte if is_horizontal_whitespace(byte) => {
                    kind = TK_WHITESPACE;
                    position += 1;
                    while position < bytes.len() && is_horizontal_whitespace(bytes[position]) {
                        position += 1;
                    }
                }
                byte if is_name_start(byte) => {
                    kind = TK_NAME;
                    position += 1;
                    while position < bytes.len() && is_name_continue(bytes[position]) {
                        position += 1;
                    }
                }
                b'<' => {
                    // A second opening delimiter is malformed; the parser
                    // recovers at the next tag.
                    kind = TK_OPEN_TAG;
                    position += 1;
                }
                _ => {
                    kind = TK_UNKNOWN;
                    position += 1;
                }
            }
        }
        tokens.push(Token::new(kind, source, start as u32, position as u32));
    }
    tokens
}

fn scan_until(source: &[u8], position: usize, closing: &[u8]) -> usize {
    match memchr::memmem::find(&source[position..], closing) {
        Some(offset) => position + offset + closing.len(),
        None => source.len(),
    }
}

fn scan_doctype(source: &[u8], mut position: usize) -> usize {
    let mut brackets = 0usize;
    let mut quote = 0u8;
    while position < source.len() {
        let value = source[position];
        if quote != 0 {
            if value == quote {
                quote = 0;
            }
            position += 1;
            continue;
        }
        match value {
            b'\'' | b'"' => quote = value,
            b'[' => brackets += 1,
            b']' => brackets = brackets.saturating_sub(1),
            b'>' => {
                position += 1;
                if brackets == 0 {
                    return position;
                }
                continue;
            }
            _ => {}
        }
        position += 1;
    }
    position
}

fn scan_entity(source: &[u8], mut position: usize) -> usize {
    position += 1;
    while position < source.len() && !matches!(source[position], b';' | b'<' | b'&' | b'\r' | b'\n')
    {
        position += 1;
    }
    if source.get(position) == Some(&b';') {
        position += 1;
    }
    position
}

fn is_horizontal_whitespace(value: u8) -> bool {
    matches!(value, b' ' | b'\t' | 0x0C)
}

fn is_name_start(value: u8) -> bool {
    value == b':' || value == b'_' || value.is_ascii_alphabetic() || value >= 0x80
}

fn is_name_continue(value: u8) -> bool {
    is_name_start(value) || value == b'-' || value == b'.' || value.is_ascii_digit()
}

#[cfg(test)]
mod tests {
    use super::*;

    fn kinds(tokens: &[Token<'_>]) -> Vec<Kind> {
        tokens.iter().map(|token| token.kind).collect()
    }

    #[test]
    fn lossless_xml() {
        let source = "<?xml version=\"1.0\"?>\n<root xmlns:x=\"urn:test\" disabled='yes'><x:item id=\"a&amp;b\">text &lt; <![CDATA[<raw>]]></x:item><!-- ok --></root>";
        let tokens = lex(source);
        let rebuilt: String = tokens.iter().map(|token| token.text).collect();
        assert_eq!(rebuilt, source);
        let kinds = kinds(&tokens);
        for kind in [
            TK_PROCESSING_INSTRUCTION,
            TK_CDATA,
            TK_COMMENT,
            TK_ENTITY_REFERENCE,
        ] {
            assert!(kinds.contains(&kind), "missing {kind:?}");
        }
    }

    #[test]
    fn incomplete_attribute_value() {
        let tokens = lex(r#"<argument type="service" id="App\Service"#);
        let last = tokens.last().unwrap();
        assert_eq!(last.kind, TK_ATTRIBUTE_VALUE);
        assert_eq!(last.text, r#""App\Service"#);
    }

    #[test]
    fn doctype_internal_subset() {
        let tokens = lex("<!DOCTYPE root [<!ELEMENT root (#PCDATA)>]><root>ok</root>");
        assert_eq!(tokens[0].kind, TK_DOCTYPE);
        assert_eq!(
            tokens[0].text,
            "<!DOCTYPE root [<!ELEMENT root (#PCDATA)>]>"
        );
    }

    #[test]
    fn non_ascii_names_and_text_stay_whole() {
        let source = "<äb ü=\"ö\">ß€ & x</äb>";
        let tokens = lex(source);
        let rebuilt: String = tokens.iter().map(|token| token.text).collect();
        assert_eq!(rebuilt, source);
        assert_eq!(tokens[1].kind, TK_NAME);
        assert_eq!(tokens[1].text, "äb");
    }
}
