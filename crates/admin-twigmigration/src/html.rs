//! Go `html.UnescapeString`.

use crate::entities::{ENTITY, ENTITY2};

/// Windows-1252 replacements for numeric references 0x80..=0x9F.
const REPLACEMENT_TABLE: [char; 32] = [
    '\u{20AC}', '\u{0081}', '\u{201A}', '\u{0192}', '\u{201E}', '\u{2026}', '\u{2020}', '\u{2021}',
    '\u{02C6}', '\u{2030}', '\u{0160}', '\u{2039}', '\u{0152}', '\u{008D}', '\u{017D}', '\u{008F}',
    '\u{0090}', '\u{2018}', '\u{2019}', '\u{201C}', '\u{201D}', '\u{2022}', '\u{2013}', '\u{2014}',
    '\u{02DC}', '\u{2122}', '\u{0161}', '\u{203A}', '\u{0153}', '\u{009D}', '\u{017E}', '\u{0178}',
];

/// All entities that do not end with `;` are 6 or fewer bytes long.
const LONGEST_ENTITY_WITHOUT_SEMICOLON: usize = 6;

fn entity(name: &[u8]) -> Option<char> {
    ENTITY
        .binary_search_by(|(candidate, _)| candidate.as_bytes().cmp(name))
        .ok()
        .map(|index| ENTITY[index].1)
}

fn entity2(name: &[u8]) -> Option<(char, char)> {
    ENTITY2
        .binary_search_by(|(candidate, _, _)| candidate.as_bytes().cmp(name))
        .ok()
        .map(|index| (ENTITY2[index].1, ENTITY2[index].2))
}

/// Unescapes entities like `&lt;` exactly like Go's `html.UnescapeString`.
pub fn unescape_string(value: &str) -> String {
    if !value.contains('&') {
        return value.to_owned();
    }
    let bytes = value.as_bytes();
    let mut result: Vec<u8> = Vec::with_capacity(bytes.len());
    let mut src = 0;
    while src < bytes.len() {
        if bytes[src] != b'&' {
            let next = bytes[src..]
                .iter()
                .position(|byte| *byte == b'&')
                .map_or(bytes.len(), |offset| src + offset);
            result.extend_from_slice(&bytes[src..next]);
            src = next;
            continue;
        }
        src += unescape_entity(&bytes[src..], &mut result);
    }
    // Entities decode to whole characters and other bytes are copied
    // verbatim, so the output stays valid UTF-8.
    String::from_utf8(result)
        .unwrap_or_else(|error| String::from_utf8_lossy(error.as_bytes()).into_owned())
}

fn push_char(result: &mut Vec<u8>, character: char) {
    let mut buffer = [0; 4];
    result.extend_from_slice(character.encode_utf8(&mut buffer).as_bytes());
}

/// Decodes one reference at the start of `s` (`s[0] == b'&'`), returning the
/// number of consumed bytes.
fn unescape_entity(s: &[u8], result: &mut Vec<u8>) -> usize {
    if s.len() <= 1 {
        result.push(s[0]);
        return 1;
    }
    let mut i = 1;
    if s[i] == b'#' {
        if s.len() <= 3 {
            result.push(s[0]);
            return 1;
        }
        i += 1;
        let mut c = s[i];
        let mut hex = false;
        if c == b'x' || c == b'X' {
            hex = true;
            i += 1;
        }
        let mut x: i32 = 0;
        while i < s.len() {
            c = s[i];
            i += 1;
            let digit = if hex {
                match c {
                    b'0'..=b'9' => Some(i32::from(c - b'0')),
                    b'a'..=b'f' => Some(i32::from(c - b'a') + 10),
                    b'A'..=b'F' => Some(i32::from(c - b'A') + 10),
                    _ => None,
                }
            } else if c.is_ascii_digit() {
                Some(i32::from(c - b'0'))
            } else {
                None
            };
            if let Some(digit) = digit {
                x = x
                    .wrapping_mul(if hex { 16 } else { 10 })
                    .wrapping_add(digit);
                continue;
            }
            if c != b';' {
                i -= 1;
            }
            break;
        }
        if i <= 3 {
            result.push(s[0]);
            return 1;
        }
        let character = if (0x80..=0x9F).contains(&x) {
            REPLACEMENT_TABLE[(x - 0x80) as usize]
        } else if x == 0 || (0xD800..=0xDFFF).contains(&x) || x > 0x10FFFF {
            '\u{FFFD}'
        } else {
            u32::try_from(x)
                .ok()
                .and_then(char::from_u32)
                .unwrap_or('\u{FFFD}')
        };
        push_char(result, character);
        return i;
    }

    while i < s.len() {
        let c = s[i];
        i += 1;
        if c.is_ascii_alphanumeric() {
            continue;
        }
        if c != b';' {
            i -= 1;
        }
        break;
    }
    let name = &s[1..i];
    if name.is_empty() {
        // No-op.
    } else if let Some(character) = entity(name) {
        push_char(result, character);
        return i;
    } else if let Some((first, second)) = entity2(name) {
        push_char(result, first);
        push_char(result, second);
        return i;
    } else {
        let max_len = (name.len() - 1).min(LONGEST_ENTITY_WITHOUT_SEMICOLON);
        for j in (2..=max_len).rev() {
            if let Some(character) = entity(&name[..j]) {
                push_char(result, character);
                return j + 1;
            }
        }
    }
    result.extend_from_slice(&s[..i]);
    i
}

#[cfg(test)]
mod tests {
    use super::unescape_string;

    #[test]
    fn matches_go_examples() {
        for (input, expected) in [
            ("plain", "plain"),
            ("&lt;b&gt; &amp; &quot;", "<b> & \""),
            ("&aacute;&#225;&#xE1;", "ááá"),
            ("&#x80;&#0;&#xD800;", "\u{20AC}\u{FFFD}\u{FFFD}"),
            ("&ampx &notit; &amp", "&x ¬it; &"),
            ("&NotEqualTilde;", "\u{2242}\u{338}"),
            ("& &# &#; &unknown; &", "& &# &#; &unknown; &"),
            ("&#99999999999;", "\u{FFFD}"),
            ("&#4294967361;", "A"),
            ("&#x;&#xg;&#12", "\u{FFFD}&#xg;\u{c}"),
        ] {
            assert_eq!(unescape_string(input), expected, "{input}");
        }
    }
}
