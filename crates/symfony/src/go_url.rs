//! The subset of Go `net/url.Parse` and `URL.EscapedPath` that route path
//! normalization depends on. Error reporting is reduced to `None`.

#[derive(Clone, Copy, PartialEq, Eq)]
enum Mode {
    Path,
    Host,
    Zone,
    UserPassword,
    Fragment,
}

/// A parsed URL (Go `url.URL`, relevant fields only).
#[derive(Debug, Default)]
pub(crate) struct GoUrl {
    pub(crate) scheme: String,
    pub(crate) opaque: String,
    pub(crate) host: String,
    path: Vec<u8>,
    raw_path: String,
}

impl GoUrl {
    pub(crate) fn is_abs(&self) -> bool {
        !self.scheme.is_empty()
    }

    /// Go `URL.EscapedPath`.
    pub(crate) fn escaped_path(&self) -> String {
        if !self.raw_path.is_empty()
            && valid_encoded(&self.raw_path)
            && unescape(&self.raw_path, Mode::Path).as_deref() == Some(self.path.as_slice())
        {
            return self.raw_path.clone();
        }
        if self.path == b"*" {
            return "*".to_owned();
        }
        escape(&self.path)
    }
}

/// Go `url.Parse`.
pub(crate) fn parse(raw: &str) -> Option<GoUrl> {
    let (without_fragment, fragment) = match raw.find('#') {
        Some(index) => (&raw[..index], &raw[index + 1..]),
        None => (raw, ""),
    };
    let url = parse_reference(without_fragment)?;
    if !fragment.is_empty() {
        unescape(fragment, Mode::Fragment)?;
    }
    Some(url)
}

fn parse_reference(raw: &str) -> Option<GoUrl> {
    if raw.bytes().any(|byte| byte < b' ' || byte == 0x7f) {
        return None;
    }
    let mut url = GoUrl::default();
    if raw == "*" {
        url.path = b"*".to_vec();
        return Some(url);
    }
    let (scheme, rest) = get_scheme(raw)?;
    url.scheme = scheme.to_ascii_lowercase();
    let mut rest = rest;
    if rest.ends_with('?') && rest.matches('?').count() == 1 {
        rest = &rest[..rest.len() - 1];
    } else if let Some(index) = rest.find('?') {
        rest = &rest[..index];
    }
    if !rest.starts_with('/') {
        if !url.scheme.is_empty() {
            url.opaque = rest.to_owned();
            return Some(url);
        }
        let segment = rest.split('/').next().unwrap_or("");
        if segment.contains(':') {
            return None;
        }
    }
    if (!url.scheme.is_empty() || !rest.starts_with("///")) && rest.starts_with("//") {
        let mut authority = &rest[2..];
        rest = "";
        if let Some(index) = authority.find('/') {
            rest = &authority[index..];
            authority = &authority[..index];
        }
        url.host = parse_authority(authority)?;
    }
    let path = unescape(rest, Mode::Path)?;
    if escape(&path) != rest {
        url.raw_path = rest.to_owned();
    }
    url.path = path;
    Some(url)
}

fn get_scheme(raw: &str) -> Option<(&str, &str)> {
    for (index, byte) in raw.bytes().enumerate() {
        match byte {
            b'a'..=b'z' | b'A'..=b'Z' => {}
            b'0'..=b'9' | b'+' | b'-' | b'.' => {
                if index == 0 {
                    return Some(("", raw));
                }
            }
            b':' => {
                if index == 0 {
                    return None;
                }
                return Some((&raw[..index], &raw[index + 1..]));
            }
            _ => return Some(("", raw)),
        }
    }
    Some(("", raw))
}

fn parse_authority(authority: &str) -> Option<String> {
    let at = authority.rfind('@');
    let host = match at {
        Some(index) => parse_host(&authority[index + 1..])?,
        None => parse_host(authority)?,
    };
    if let Some(index) = at {
        let userinfo = &authority[..index];
        if !userinfo.bytes().all(valid_userinfo_byte) {
            return None;
        }
        match userinfo.split_once(':') {
            Some((user, password)) => {
                unescape(user, Mode::UserPassword)?;
                unescape(password, Mode::UserPassword)?;
            }
            None => {
                unescape(userinfo, Mode::UserPassword)?;
            }
        }
    }
    Some(host)
}

fn valid_userinfo_byte(byte: u8) -> bool {
    byte.is_ascii_alphanumeric()
        || matches!(
            byte,
            b'-' | b'.'
                | b'_'
                | b':'
                | b'~'
                | b'!'
                | b'$'
                | b'&'
                | b'\''
                | b'('
                | b')'
                | b'*'
                | b'+'
                | b','
                | b';'
                | b'='
                | b'%'
                | b'@'
        )
}

fn parse_host(host: &str) -> Option<String> {
    if host.starts_with('[') {
        let close = host.rfind(']')?;
        if !valid_optional_port(&host[close + 1..]) {
            return None;
        }
        if let Some(zone) = host[..close].find("%25") {
            let first = unescape(&host[..zone], Mode::Host)?;
            let second = unescape(&host[zone..close], Mode::Zone)?;
            let third = unescape(&host[close..], Mode::Host)?;
            let mut result = first;
            result.extend(second);
            result.extend(third);
            return Some(String::from_utf8_lossy(&result).into_owned());
        }
    } else if let Some(colon) = host.rfind(':')
        && !valid_optional_port(&host[colon..])
    {
        return None;
    }
    let value = unescape(host, Mode::Host)?;
    Some(String::from_utf8_lossy(&value).into_owned())
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

fn unhex(byte: u8) -> u8 {
    match byte {
        b'0'..=b'9' => byte - b'0',
        b'a'..=b'f' => byte - b'a' + 10,
        b'A'..=b'F' => byte - b'A' + 10,
        _ => 0,
    }
}

fn unescape(value: &str, mode: Mode) -> Option<Vec<u8>> {
    let bytes = value.as_bytes();
    let mut index = 0;
    while index < bytes.len() {
        match bytes[index] {
            b'%' => {
                if index + 2 >= bytes.len()
                    || !bytes[index + 1].is_ascii_hexdigit()
                    || !bytes[index + 2].is_ascii_hexdigit()
                {
                    return None;
                }
                let escape = &value[index..index + 3];
                if mode == Mode::Host && unhex(bytes[index + 1]) < 8 && escape != "%25" {
                    return None;
                }
                if mode == Mode::Zone {
                    let decoded = (unhex(bytes[index + 1]) << 4) | unhex(bytes[index + 2]);
                    if escape != "%25" && decoded != b' ' && should_escape(decoded, Mode::Host) {
                        return None;
                    }
                }
                index += 3;
            }
            byte => {
                if matches!(mode, Mode::Host | Mode::Zone)
                    && byte < 0x80
                    && should_escape(byte, mode)
                {
                    return None;
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
    Some(result)
}

fn should_escape(byte: u8, mode: Mode) -> bool {
    if byte.is_ascii_alphanumeric() {
        return false;
    }
    if matches!(mode, Mode::Host | Mode::Zone)
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
        b'$' | b'&' | b'+' | b',' | b'/' | b':' | b';' | b'=' | b'?' | b'@' => {
            return match mode {
                Mode::Path => byte == b'?',
                Mode::UserPassword => matches!(byte, b'@' | b'/' | b'?' | b':'),
                Mode::Fragment => false,
                Mode::Host | Mode::Zone => true,
            };
        }
        _ => {}
    }
    if mode == Mode::Fragment && matches!(byte, b'!' | b'(' | b')' | b'*') {
        return false;
    }
    true
}

fn escape(path: &[u8]) -> String {
    const HEX: &[u8; 16] = b"0123456789ABCDEF";
    let mut result = String::with_capacity(path.len());
    for &byte in path {
        if should_escape(byte, Mode::Path) {
            result.push('%');
            result.push(HEX[usize::from(byte >> 4)] as char);
            result.push(HEX[usize::from(byte & 15)] as char);
        } else {
            result.push(byte as char);
        }
    }
    result
}

fn valid_encoded(value: &str) -> bool {
    value.bytes().all(|byte| {
        matches!(
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
                | b'@'
                | b'['
                | b']'
                | b'%'
        ) || !should_escape(byte, Mode::Path)
    })
}

#[cfg(test)]
mod tests {
    use super::parse;

    #[test]
    fn parses_like_go() {
        let url = parse("https://shop.example/edit/foo.bar?preview=1#details").unwrap();
        assert_eq!(url.host, "shop.example");
        assert_eq!(url.escaped_path(), "/edit/foo.bar");
        assert_eq!(parse("/a b").unwrap().escaped_path(), "/a%20b");
        assert_eq!(parse("/a!b").unwrap().escaped_path(), "/a!b");
        assert!(parse("/a%zz").is_none());
        assert!(parse("a:b/c").map(|url| url.opaque).as_deref() == Some("b/c"));
        assert!(parse("1a:b").is_none());
        let opaque = parse("mailto:x@y").unwrap();
        assert_eq!(opaque.scheme, "mailto");
        assert_eq!(opaque.opaque, "x@y");
    }
}
