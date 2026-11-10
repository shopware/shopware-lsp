//! Pretty-prints JSON for snippet files: two-space indentation, keys in
//! document order, one member per line, and arrays kept on one line while
//! they fit within 80 columns.

const WIDTH: i64 = 80;

pub(crate) fn pretty(json: &[u8]) -> Vec<u8> {
    let mut buf = Vec::with_capacity(json.len());
    append_any(&mut buf, json, 0, true, "  ", 0, 0, -1);
    if !buf.is_empty() {
        buf.push(b'\n');
    }
    buf
}

fn is_nan_or_inf(src: &[u8]) -> bool {
    matches!(src[0], b'i' | b'I' | b'+' | b'N')
        || (src[0] == b'n' && src.len() > 1 && src[1] != b'u')
}

/// Returns `(i, nl, ok)`.
#[allow(clippy::too_many_arguments)]
fn append_any(
    buf: &mut Vec<u8>,
    json: &[u8],
    mut i: usize,
    pretty: bool,
    indent: &str,
    tabs: usize,
    nl: usize,
    max: i64,
) -> (usize, usize, bool) {
    while i < json.len() {
        let byte = json[i];
        if byte <= b' ' {
            i += 1;
            continue;
        }
        if byte == b'"' {
            return append_string(buf, json, i, nl);
        }
        if byte.is_ascii_digit() || byte == b'-' || is_nan_or_inf(&json[i..]) {
            return append_number(buf, json, i, nl);
        }
        if byte == b'{' || byte == b'[' {
            let close = if byte == b'{' { b'}' } else { b']' };
            return append_object(buf, json, i, byte, close, pretty, indent, tabs, nl, max);
        }
        match byte {
            b't' => {
                buf.extend_from_slice(b"true");
                return (i + 4, nl, true);
            }
            b'f' => {
                buf.extend_from_slice(b"false");
                return (i + 5, nl, true);
            }
            b'n' => {
                buf.extend_from_slice(b"null");
                return (i + 4, nl, true);
            }
            _ => {}
        }
        i += 1;
    }
    (i, nl, true)
}

fn newline(buf: &mut Vec<u8>) -> usize {
    let nl = buf.len();
    if buf.last() == Some(&b' ') {
        *buf.last_mut().expect("non-empty") = b'\n';
    } else {
        buf.push(b'\n');
    }
    nl
}

#[allow(clippy::too_many_arguments)]
fn append_object(
    buf: &mut Vec<u8>,
    json: &[u8],
    mut i: usize,
    open: u8,
    close: u8,
    pretty: bool,
    indent: &str,
    tabs: usize,
    mut nl: usize,
    max: i64,
) -> (usize, usize, bool) {
    if pretty && open == b'[' && max == -1 {
        // Try a single-line array first.
        let max = WIDTH - (buf.len() as i64 - nl as i64);
        if max > 3 {
            let (s1, s2) = (buf.len(), i);
            let (next, _, ok) = append_object(buf, json, i, b'[', b']', false, "", 0, 0, max);
            if ok && (buf.len() - s1) as i64 <= max {
                return (next, nl, true);
            }
            buf.truncate(s1);
            i = s2;
        }
    } else if max != -1 && open == b'{' {
        return (i, nl, false);
    }
    buf.push(open);
    i += 1;
    let mut n = 0;
    while i < json.len() {
        if json[i] <= b' ' {
            i += 1;
            continue;
        }
        if json[i] == close {
            if pretty {
                if n > 0 {
                    nl = newline(buf);
                }
                if buf.last() != Some(&open) {
                    append_tabs(buf, indent, tabs);
                }
            }
            buf.push(close);
            return (i + 1, nl, open != b'{');
        }
        if open == b'[' || json[i] == b'"' {
            if n > 0 {
                buf.push(b',');
                if open == b'[' {
                    buf.push(b' ');
                }
            }
            if pretty {
                nl = newline(buf);
                append_tabs(buf, indent, tabs + 1);
            }
            if open == b'{' {
                let (next, next_nl, _) = append_string(buf, json, i, nl);
                i = next;
                nl = next_nl;
                buf.push(b':');
                if pretty {
                    buf.push(b' ');
                }
            }
            let (next, next_nl, ok) = append_any(buf, json, i, pretty, indent, tabs + 1, nl, max);
            i = next;
            nl = next_nl;
            if max != -1 && !ok {
                return (i, nl, false);
            }
            n += 1;
            continue;
        }
        i += 1;
    }
    (i, nl, open != b'{')
}

fn append_string(buf: &mut Vec<u8>, json: &[u8], start: usize, nl: usize) -> (usize, usize, bool) {
    let mut i = start + 1;
    while i < json.len() {
        if json[i] == b'"' {
            let mut escapes = 0;
            let mut j = i - 1;
            while j > start && json[j] == b'\\' {
                escapes += 1;
                j -= 1;
            }
            if escapes % 2 == 1 {
                i += 1;
                continue;
            }
            i += 1;
            break;
        }
        i += 1;
    }
    buf.extend_from_slice(&json[start..i]);
    (i, nl, true)
}

fn append_number(buf: &mut Vec<u8>, json: &[u8], start: usize, nl: usize) -> (usize, usize, bool) {
    let mut i = start + 1;
    while i < json.len() {
        if json[i] <= b' ' || matches!(json[i], b',' | b':' | b']' | b'}') {
            break;
        }
        i += 1;
    }
    buf.extend_from_slice(&json[start..i]);
    (i, nl, true)
}

fn append_tabs(buf: &mut Vec<u8>, indent: &str, tabs: usize) {
    for _ in 0..tabs {
        buf.extend_from_slice(indent.as_bytes());
    }
}
