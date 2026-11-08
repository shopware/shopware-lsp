//! The subset of `github.com/tidwall/sjson` v1.2.5 `Set(json, path, string)`
//! the snippet commands use, byte-compatible with Go for simple dot paths.
//!
//! Paths containing gjson query characters (`|#@*?`) take sjson's complex
//! path branch; Rust leaves the document unchanged there (sjson's result
//! when the queried value does not exist).

#[derive(Debug, Clone, PartialEq, Eq)]
pub(crate) struct SetError(pub String);

impl std::fmt::Display for SetError {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        f.write_str(&self.0)
    }
}

impl std::error::Error for SetError {}

#[derive(Debug, Clone, Default)]
struct PathResult {
    /// The key with escapes removed.
    part: Vec<u8>,
    force: bool,
    more: bool,
    path: Vec<u8>,
}

fn is_simple_char(ch: u8) -> bool {
    !matches!(ch, b'|' | b'#' | b'@' | b'*' | b'?')
}

fn parse_path(path: &[u8]) -> (PathResult, bool) {
    let mut result = PathResult::default();
    let mut path = path;
    if path.first() == Some(&b':') {
        result.force = true;
        path = &path[1..];
    }
    let mut i = 0;
    while i < path.len() {
        if path[i] == b'.' {
            result.part = path[..i].to_vec();
            result.path = path[i + 1..].to_vec();
            result.more = true;
            return (result, true);
        }
        if !is_simple_char(path[i]) {
            return (result, false);
        }
        if path[i] == b'\\' {
            let mut epart = path[..i].to_vec();
            i += 1;
            if i < path.len() {
                epart.push(path[i]);
                i += 1;
                while i < path.len() {
                    if path[i] == b'\\' {
                        i += 1;
                        if i < path.len() {
                            epart.push(path[i]);
                        }
                        i += 1;
                        continue;
                    } else if path[i] == b'.' {
                        result.part = epart;
                        result.path = path[i + 1..].to_vec();
                        result.more = true;
                        return (result, true);
                    } else if !is_simple_char(path[i]) {
                        return (result, false);
                    }
                    epart.push(path[i]);
                    i += 1;
                }
            }
            result.part = epart;
            return (result, true);
        }
        i += 1;
    }
    result.part = path.to_vec();
    (result, true)
}

fn must_marshal_string(value: &[u8]) -> bool {
    value
        .iter()
        .any(|&byte| !(b' '..=0x7f).contains(&byte) || byte == b'"' || byte == b'\\')
}

/// Go `json.Marshal` of a string (HTML-safe escaping).
pub(crate) fn go_marshal_string(buf: &mut Vec<u8>, value: &str) {
    const HEX: &[u8; 16] = b"0123456789abcdef";
    buf.push(b'"');
    for character in value.chars() {
        match character {
            '"' => buf.extend_from_slice(b"\\\""),
            '\\' => buf.extend_from_slice(b"\\\\"),
            '\n' => buf.extend_from_slice(b"\\n"),
            '\r' => buf.extend_from_slice(b"\\r"),
            '\t' => buf.extend_from_slice(b"\\t"),
            '\u{8}' => buf.extend_from_slice(b"\\b"),
            '\u{c}' => buf.extend_from_slice(b"\\f"),
            '<' | '>' | '&' | '\u{0}'..='\u{1f}' => {
                let code = character as u8;
                buf.extend_from_slice(b"\\u00");
                buf.push(HEX[(code >> 4) as usize]);
                buf.push(HEX[(code & 0xf) as usize]);
            }
            '\u{2028}' => buf.extend_from_slice(b"\\u2028"),
            '\u{2029}' => buf.extend_from_slice(b"\\u2029"),
            other => {
                let mut encoded = [0; 4];
                buf.extend_from_slice(other.encode_utf8(&mut encoded).as_bytes());
            }
        }
    }
    buf.push(b'"');
}

fn append_stringify(buf: &mut Vec<u8>, value: &[u8]) {
    if must_marshal_string(value) {
        go_marshal_string(buf, &String::from_utf8_lossy(value));
        return;
    }
    buf.push(b'"');
    buf.extend_from_slice(value);
    buf.push(b'"');
}

fn atoui(path: &PathResult) -> (usize, bool) {
    if path.force {
        return (0, false);
    }
    let mut n: usize = 0;
    for &byte in &path.part {
        if !byte.is_ascii_digit() {
            return (0, false);
        }
        n = n.wrapping_mul(10).wrapping_add(usize::from(byte - b'0'));
    }
    (n, true)
}

fn append_repeat(buf: &mut Vec<u8>, value: &[u8], n: usize) {
    for _ in 0..n {
        buf.extend_from_slice(value);
    }
}

fn append_build(buf: &mut Vec<u8>, array: bool, paths: &[PathResult], raw: &[u8]) {
    if !array {
        append_stringify(buf, &paths[0].part);
        buf.push(b':');
    }
    if paths.len() > 1 {
        let (n, numeric) = atoui(&paths[1]);
        if numeric || (!paths[1].force && paths[1].part == b"-1") {
            buf.push(b'[');
            append_repeat(buf, b"null,", n);
            append_build(buf, true, &paths[1..], raw);
            buf.push(b']');
        } else {
            buf.push(b'{');
            append_build(buf, false, &paths[1..], raw);
            buf.push(b'}');
        }
    } else {
        append_stringify(buf, raw);
    }
}

fn trim(value: &[u8]) -> &[u8] {
    let mut value = value;
    while let [first, rest @ ..] = value {
        if *first > b' ' {
            break;
        }
        value = rest;
    }
    while let [rest @ .., last] = value {
        if *last > b' ' {
            break;
        }
        value = rest;
    }
    value
}

// --- minimal gjson ---------------------------------------------------------

/// The end of the JSON value starting at `start` (gjson's raw extent).
fn value_end(json: &[u8], start: usize) -> usize {
    match json[start] {
        b'"' => string_end(json, start),
        b'{' | b'[' => {
            let mut depth = 1;
            let mut i = start + 1;
            while i < json.len() {
                match json[i] {
                    b'"' => {
                        i = string_end(json, i);
                        continue;
                    }
                    b'{' | b'[' | b'(' => depth += 1,
                    b'}' | b']' | b')' => {
                        depth -= 1;
                        if depth == 0 {
                            return i + 1;
                        }
                    }
                    _ => {}
                }
                i += 1;
            }
            json.len()
        }
        _ => {
            let mut i = start + 1;
            while i < json.len() {
                if matches!(json[i], b' ' | b'\t' | b'\r' | b'\n' | b',' | b']' | b'}') {
                    break;
                }
                i += 1;
            }
            i
        }
    }
}

/// The index after the closing quote of the string at `start`.
fn string_end(json: &[u8], start: usize) -> usize {
    let mut i = start + 1;
    while i < json.len() {
        match json[i] {
            b'\\' => i += 2,
            b'"' => return i + 1,
            _ => i += 1,
        }
    }
    json.len()
}

fn decode_key(raw: &[u8]) -> Vec<u8> {
    if !raw.contains(&b'\\') {
        return raw[1..raw.len().saturating_sub(1).max(1)].to_vec();
    }
    match serde_json::from_slice::<String>(raw) {
        Ok(value) => value.into_bytes(),
        Err(_) => raw[1..raw.len().saturating_sub(1).max(1)].to_vec(),
    }
}

/// gjson `Get(json, part)` for one simple path component: the byte range of
/// the value.
fn get(json: &[u8], part: &PathResult) -> Option<(usize, usize)> {
    let mut i = 0;
    while i < json.len() && json[i] <= b' ' {
        i += 1;
    }
    match json.get(i)? {
        b'{' => {
            i += 1;
            loop {
                while i < json.len() && json[i] != b'"' && json[i] != b'}' {
                    i += 1;
                }
                if i >= json.len() || json[i] == b'}' {
                    return None;
                }
                let key_end = string_end(json, i);
                let key = decode_key(&json[i..key_end]);
                i = key_end;
                while i < json.len() && (json[i] <= b' ' || json[i] == b':') {
                    i += 1;
                }
                if i >= json.len() {
                    return None;
                }
                let end = value_end(json, i);
                if key == part.part {
                    return Some((i, end));
                }
                i = end;
            }
        }
        b'[' => {
            let (n, numeric) = atoui(part);
            if !numeric {
                return None;
            }
            i += 1;
            let mut index = 0;
            loop {
                while i < json.len() && (json[i] <= b' ' || json[i] == b',') {
                    i += 1;
                }
                if i >= json.len() || json[i] == b']' {
                    return None;
                }
                let end = value_end(json, i);
                if index == n {
                    return Some((i, end));
                }
                index += 1;
                i = end;
            }
        }
        _ => None,
    }
}

/// gjson `Parse(json).Raw` when the document is an object or array.
fn parse_json_raw(json: &[u8]) -> Option<&[u8]> {
    for (index, &byte) in json.iter().enumerate() {
        if byte == b'{' || byte == b'[' {
            return Some(&json[index..]);
        }
        if byte > b' ' {
            return None;
        }
    }
    None
}

/// gjson `Parse(raw).Array()` element raws.
fn array_elements(raw: &[u8]) -> Vec<&[u8]> {
    let mut result = Vec::new();
    let mut i = 1;
    loop {
        while i < raw.len() && (raw[i] <= b' ' || raw[i] == b',') {
            i += 1;
        }
        if i >= raw.len() || raw[i] == b']' {
            return result;
        }
        let end = value_end(raw, i);
        result.push(&raw[i..end]);
        i = end;
    }
}

fn append_raw_paths(
    buf: &mut Vec<u8>,
    json: &[u8],
    paths: &[PathResult],
    raw: &[u8],
) -> Result<(), SetError> {
    if let Some((start, end)) = get(json, &paths[0])
        && start > 0
    {
        buf.extend_from_slice(&json[..start]);
        if paths.len() > 1 {
            append_raw_paths(buf, &json[start..end], &paths[1..], raw)?;
        } else {
            append_stringify(buf, raw);
        }
        buf.extend_from_slice(&json[end..]);
        return Ok(());
    }
    let (n, numeric) = atoui(&paths[0]);
    let empty_fallback: &[u8] = if numeric { b"[]" } else { b"{}" };
    let json = if json.iter().all(|&byte| byte <= b' ') {
        empty_fallback
    } else {
        json
    };
    let raw_json = parse_json_raw(json).unwrap_or(empty_fallback);
    let mut comma = false;
    for &byte in &raw_json[1..] {
        if byte <= b' ' {
            continue;
        }
        if byte != b'}' && byte != b']' {
            comma = true;
        }
        break;
    }
    match raw_json[0] {
        b'{' => {
            let mut end = raw_json.len() - 1;
            while end > 0 && raw_json[end] != b'}' {
                end -= 1;
            }
            buf.extend_from_slice(&raw_json[..end]);
            if comma {
                buf.push(b',');
            }
            append_build(buf, false, paths, raw);
            buf.push(b'}');
            Ok(())
        }
        _ => {
            if !numeric {
                if paths[0].part == b"-1" && !paths[0].force {
                    let mut trimmed = trim(raw_json);
                    if trimmed.last() == Some(&b']') {
                        trimmed = &trimmed[..trimmed.len() - 1];
                    }
                    buf.extend_from_slice(trimmed);
                    if comma {
                        buf.push(b',');
                    }
                    append_build(buf, true, paths, raw);
                    buf.push(b']');
                    return Ok(());
                }
                return Err(SetError(format!(
                    "cannot set array element for non-numeric key '{}'",
                    String::from_utf8_lossy(&paths[0].part)
                )));
            }
            buf.push(b'[');
            let elements = array_elements(raw_json);
            for (index, element) in elements.iter().enumerate() {
                if index > 0 {
                    buf.push(b',');
                }
                buf.extend_from_slice(element);
            }
            if elements.is_empty() {
                append_repeat(buf, b"null,", n);
            } else {
                append_repeat(buf, b",null", n.saturating_sub(elements.len()));
                if comma {
                    buf.push(b',');
                }
            }
            append_build(buf, true, paths, raw);
            buf.push(b']');
            Ok(())
        }
    }
}

/// sjson `Set(json, path, value)` for a string value.
pub(crate) fn set(json: &str, path: &str, value: &str) -> Result<String, SetError> {
    if path.is_empty() {
        return Err(SetError("path cannot be empty".to_owned()));
    }
    let mut paths = Vec::new();
    let (mut result, mut simple) = parse_path(path.as_bytes());
    if simple {
        paths.push(result.clone());
        while result.more {
            (result, simple) = parse_path(&result.path.clone());
            if !simple {
                break;
            }
            paths.push(result.clone());
        }
    }
    if !simple {
        return Ok(json.to_owned());
    }
    let mut buf = Vec::with_capacity(json.len() + value.len() + path.len() + 8);
    append_raw_paths(&mut buf, json.as_bytes(), &paths, value.as_bytes())?;
    Ok(String::from_utf8_lossy(&buf).into_owned())
}
