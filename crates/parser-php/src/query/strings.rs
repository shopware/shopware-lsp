//! Interpolated string inputs (port of Go `string_variables.go`).

use swlsp_syntax::SyntaxToken;

/// Lexical variable inputs (with `$`) of a native PHP string token. `None`
/// when the string uses dynamic variable names or executable interpolation.
pub fn string_variables(token: &SyntaxToken) -> Option<Vec<String>> {
    string_variables_in(token.text())
}

/// [`string_variables`] over raw string token text.
pub fn string_variables_in(text: &str) -> Option<Vec<String>> {
    let mut bytes = text.as_bytes();
    if bytes.len() > 1 && matches!(bytes[0], b'b' | b'B') && matches!(bytes[1], b'\'' | b'"') {
        bytes = &bytes[1..];
    }
    let nowdoc = bytes.starts_with(b"<<<")
        && std::str::from_utf8(&bytes[3..]).is_ok_and(|rest| rest.trim_start().starts_with('\''));
    if bytes.starts_with(b"'") || nowdoc {
        return Some(Vec::new());
    }
    let mut names = Vec::new();
    let mut braced = false;
    let mut index = 0;
    while index < bytes.len() {
        let byte = bytes[index];
        if byte == b'\\' {
            index += 2;
            continue;
        }
        if byte == b'{' && bytes.get(index + 1) == Some(&b'$') {
            if braced {
                return None;
            }
            braced = true;
        } else if braced && byte == b'}' {
            braced = false;
        } else if braced
            && (byte == b'('
                || byte == b'{'
                || [&b"__FUNCTION__"[..], b"__METHOD__", b"__LINE__"]
                    .iter()
                    .any(|magic| bytes[index..].starts_with(magic)))
        {
            return None;
        }
        if byte != b'$' {
            index += 1;
            continue;
        }
        let start = index;
        index += 1;
        if index >= bytes.len() {
            break;
        }
        if bytes[index] == b'{' || bytes[index] == b'$' {
            return None;
        }
        if !variable_start(bytes[index]) {
            // Go's loop increment also skips the byte after a bare `$`.
            index += 1;
            continue;
        }
        while index < bytes.len() && (variable_start(bytes[index]) || bytes[index].is_ascii_digit())
        {
            index += 1;
        }
        names.push(String::from_utf8_lossy(&bytes[start..index]).into_owned());
    }
    Some(names)
}

fn variable_start(byte: u8) -> bool {
    byte == b'_' || byte >= 0x80 || byte.is_ascii_alphabetic()
}
