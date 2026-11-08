//! The subset of Go's `net/url` used by the URI helpers, with identical
//! acceptance rules and error texts.

#[derive(Clone, Copy, PartialEq, Eq)]
enum Encoding {
    Path,
    PathSegment,
    Host,
    UserPassword,
    Fragment,
}

pub(crate) struct ParsedUrl {
    pub scheme: String,
    pub host: String,
    pub path: String,
}

fn should_escape(byte: u8, mode: Encoding) -> bool {
    if byte.is_ascii_alphanumeric() {
        return false;
    }
    if mode == Encoding::Host
        && matches!(
            byte,
            b'!' | b'$'
                | b'&'
                | b'\''
                | b'('
                | b')'
                | b'*'
                | b'+'
                | b','
                | b';'
                | b'='
                | b':'
                | b'['
                | b']'
                | b'<'
                | b'>'
                | b'"'
        )
    {
        return false;
    }
    match byte {
        b'-' | b'_' | b'.' | b'~' => return false,
        b'$' | b'&' | b'+' | b',' | b'/' | b':' | b';' | b'=' | b'?' | b'@' => match mode {
            Encoding::PathSegment => return matches!(byte, b'/' | b';' | b',' | b'?'),
            Encoding::Fragment => return false,
            Encoding::UserPassword => return matches!(byte, b'@' | b'/' | b'?' | b':'),
            Encoding::Path => return byte == b'?',
            Encoding::Host => {}
        },
        _ => {}
    }
    if mode == Encoding::Fragment && matches!(byte, b'!' | b'(' | b')' | b'*') {
        return false;
    }
    true
}

fn escape(value: &str, mode: Encoding) -> String {
    const HEX: &[u8; 16] = b"0123456789ABCDEF";
    let mut result = String::with_capacity(value.len());
    for &byte in value.as_bytes() {
        if should_escape(byte, mode) {
            result.push('%');
            result.push(HEX[(byte >> 4) as usize] as char);
            result.push(HEX[(byte & 15) as usize] as char);
        } else {
            result.push(byte as char);
        }
    }
    result
}

/// Go `url.PathEscape`.
pub fn path_escape(value: &str) -> String {
    escape(value, Encoding::PathSegment)
}

/// Equivalent of `(&url.URL{Scheme: "file", Path: path}).String()`.
pub(crate) fn file_url_string(path: &str) -> String {
    let escaped = if path == "*" {
        "*".to_owned()
    } else {
        escape(path, Encoding::Path)
    };
    if path.is_empty() {
        return "file:".to_owned();
    }
    format!("file://{escaped}")
}

fn unhex(byte: u8) -> u8 {
    match byte {
        b'0'..=b'9' => byte - b'0',
        b'a'..=b'f' => byte - b'a' + 10,
        b'A'..=b'F' => byte - b'A' + 10,
        _ => 0,
    }
}

fn unescape(value: &str, mode: Encoding) -> Result<String, String> {
    let bytes = value.as_bytes();
    let mut index = 0;
    while index < bytes.len() {
        match bytes[index] {
            b'%' => {
                if index + 2 >= bytes.len()
                    || !bytes[index + 1].is_ascii_hexdigit()
                    || !bytes[index + 2].is_ascii_hexdigit()
                {
                    let rest = &value[index..];
                    let rest = rest.get(..3.min(rest.len())).unwrap_or(rest);
                    return Err(format!("invalid URL escape {}", go_quote(rest)));
                }
                if mode == Encoding::Host
                    && unhex(bytes[index + 1]) < 8
                    && &value[index..index + 3] != "%25"
                {
                    return Err(format!(
                        "invalid URL escape {}",
                        go_quote(&value[index..index + 3])
                    ));
                }
                index += 3;
            }
            byte => {
                if mode == Encoding::Host && byte < 0x80 && should_escape(byte, mode) {
                    return Err(format!(
                        "invalid character {} in host name",
                        go_quote(&value[index..index + 1])
                    ));
                }
                index += 1;
            }
        }
    }
    let mut result = Vec::with_capacity(bytes.len());
    let mut index = 0;
    while index < bytes.len() {
        if bytes[index] == b'%' {
            result.push((unhex(bytes[index + 1]) << 4) | unhex(bytes[index + 2]));
            index += 3;
        } else {
            result.push(bytes[index]);
            index += 1;
        }
    }
    // Go strings may hold arbitrary bytes; Rust strings must be UTF-8.
    Ok(match String::from_utf8(result) {
        Ok(value) => value,
        Err(error) => String::from_utf8_lossy(error.as_bytes()).into_owned(),
    })
}

fn get_scheme(raw: &str) -> Result<(&str, &str), String> {
    for (index, byte) in raw.bytes().enumerate() {
        match byte {
            b'a'..=b'z' | b'A'..=b'Z' => {}
            b'0'..=b'9' | b'+' | b'-' | b'.' => {
                if index == 0 {
                    return Ok(("", raw));
                }
            }
            b':' => {
                if index == 0 {
                    return Err("missing protocol scheme".to_owned());
                }
                return Ok((&raw[..index], &raw[index + 1..]));
            }
            _ => return Ok(("", raw)),
        }
    }
    Ok(("", raw))
}

fn valid_optional_port(port: &str) -> bool {
    if port.is_empty() {
        return true;
    }
    let Some(digits) = port.strip_prefix(':') else {
        return false;
    };
    digits.bytes().all(|byte| byte.is_ascii_digit())
}

fn parse_host(host: &str) -> Result<String, String> {
    if host.starts_with('[') {
        let Some(closing) = host.rfind(']') else {
            return Err("missing ']' in host".to_owned());
        };
        let colon_port = &host[closing + 1..];
        if !valid_optional_port(colon_port) {
            return Err(format!("invalid port {} after host", go_quote(colon_port)));
        }
    } else if let Some(colon) = host.rfind(':') {
        let colon_port = &host[colon..];
        if !valid_optional_port(colon_port) {
            return Err(format!("invalid port {} after host", go_quote(colon_port)));
        }
    }
    unescape(host, Encoding::Host)
}

fn valid_userinfo(value: &str) -> bool {
    value.chars().all(|character| {
        character.is_ascii_alphanumeric()
            || matches!(
                character,
                '-' | '.'
                    | '_'
                    | ':'
                    | '~'
                    | '!'
                    | '$'
                    | '&'
                    | '\''
                    | '('
                    | ')'
                    | '*'
                    | '+'
                    | ','
                    | ';'
                    | '='
                    | '%'
                    | '@'
            )
    })
}

fn parse_authority(authority: &str) -> Result<String, String> {
    let at = authority.rfind('@');
    let host = parse_host(match at {
        Some(index) => &authority[index + 1..],
        None => authority,
    })?;
    if let Some(index) = at {
        let userinfo = &authority[..index];
        if !valid_userinfo(userinfo) {
            return Err("net/url: invalid userinfo".to_owned());
        }
        match userinfo.split_once(':') {
            None => {
                unescape(userinfo, Encoding::UserPassword)?;
            }
            Some((user, password)) => {
                unescape(user, Encoding::UserPassword)?;
                unescape(password, Encoding::UserPassword)?;
            }
        }
    }
    Ok(host)
}

/// Go `url.Parse`, returning the fields the URI helpers need. Errors are
/// formatted like `*url.Error`.
pub(crate) fn parse(raw: &str) -> Result<ParsedUrl, String> {
    let (without_fragment, fragment) = match raw.split_once('#') {
        Some((head, fragment)) => (head, Some(fragment)),
        None => (raw, None),
    };
    let parsed = parse_without_fragment(without_fragment)
        .map_err(|error| format!("parse {}: {error}", go_quote(without_fragment)))?;
    if let Some(fragment) = fragment {
        unescape(fragment, Encoding::Fragment)
            .map_err(|error| format!("parse {}: {error}", go_quote(raw)))?;
    }
    Ok(parsed)
}

fn parse_without_fragment(raw: &str) -> Result<ParsedUrl, String> {
    if raw.bytes().any(|byte| byte < 0x20 || byte == 0x7f) {
        return Err("net/url: invalid control character in URL".to_owned());
    }
    let mut result = ParsedUrl {
        scheme: String::new(),
        host: String::new(),
        path: String::new(),
    };
    if raw == "*" {
        result.path = "*".to_owned();
        return Ok(result);
    }
    let (scheme, mut rest) = get_scheme(raw)?;
    result.scheme = scheme.to_ascii_lowercase();
    if rest.ends_with('?') && rest.matches('?').count() == 1 {
        rest = &rest[..rest.len() - 1];
    } else if let Some((head, _query)) = rest.split_once('?') {
        rest = head;
    }
    if !rest.starts_with('/') {
        if !result.scheme.is_empty() {
            // Opaque URL: Path stays empty.
            return Ok(result);
        }
        let segment = rest.split_once('/').map_or(rest, |(segment, _)| segment);
        if segment.contains(':') {
            return Err("first path segment in URL cannot contain colon".to_owned());
        }
    }
    if (!result.scheme.is_empty() || !rest.starts_with("///")) && rest.starts_with("//") {
        let authority_and_path = &rest[2..];
        let (authority, path) = match authority_and_path.find('/') {
            Some(index) => (&authority_and_path[..index], &authority_and_path[index..]),
            None => (authority_and_path, ""),
        };
        result.host = parse_authority(authority)?;
        rest = path;
    }
    result.path = unescape(rest, Encoding::Path)?;
    Ok(result)
}

/// Quotes a string like Go's `strconv.Quote` / `%q`.
pub fn go_quote(value: &str) -> String {
    let mut result = String::with_capacity(value.len() + 2);
    result.push('"');
    for character in value.chars() {
        match character {
            '"' => result.push_str("\\\""),
            '\\' => result.push_str("\\\\"),
            '\u{7}' => result.push_str("\\a"),
            '\u{8}' => result.push_str("\\b"),
            '\u{c}' => result.push_str("\\f"),
            '\n' => result.push_str("\\n"),
            '\r' => result.push_str("\\r"),
            '\t' => result.push_str("\\t"),
            '\u{b}' => result.push_str("\\v"),
            character if (character as u32) < 0x20 || character == '\u{7f}' => {
                result.push_str(&format!("\\x{:02x}", character as u32));
            }
            character if character.is_control() => {
                let code = character as u32;
                if code <= 0xffff {
                    result.push_str(&format!("\\u{code:04x}"));
                } else {
                    result.push_str(&format!("\\U{code:08x}"));
                }
            }
            character => result.push(character),
        }
    }
    result.push('"');
    result
}
