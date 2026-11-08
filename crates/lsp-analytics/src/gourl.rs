//! The subset of Go's `net/url` (`Parse` and `URL.String`) the profiler
//! catalog needs, with Go's acceptance rules. Components are kept as bytes
//! so percent-decoded non-UTF-8 data round-trips like Go strings.

#[derive(Clone, Copy, PartialEq, Eq)]
enum Encoding {
    Path,
    Host,
    Zone,
    UserPassword,
    Fragment,
}

#[derive(Clone, Debug, Default)]
pub(crate) struct Url {
    pub scheme: String,
    pub opaque: Vec<u8>,
    /// Username and optional password (Go `*Userinfo`).
    pub user: Option<(Vec<u8>, Option<Vec<u8>>)>,
    pub host: Vec<u8>,
    pub path: Vec<u8>,
    pub omit_host: bool,
    pub force_query: bool,
    pub raw_query: Vec<u8>,
}

fn should_escape(byte: u8, mode: Encoding) -> bool {
    if byte.is_ascii_alphanumeric() {
        return false;
    }
    if matches!(mode, Encoding::Host | Encoding::Zone)
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
            Encoding::Path => return byte == b'?',
            Encoding::UserPassword => return matches!(byte, b'@' | b'/' | b'?' | b':'),
            Encoding::Fragment => return false,
            Encoding::Host | Encoding::Zone => {}
        },
        _ => {}
    }
    if mode == Encoding::Fragment && matches!(byte, b'!' | b'(' | b')' | b'*') {
        return false;
    }
    true
}

fn escape(value: &[u8], mode: Encoding, out: &mut Vec<u8>) {
    const HEX: &[u8; 16] = b"0123456789ABCDEF";
    for &byte in value {
        if should_escape(byte, mode) {
            out.extend_from_slice(&[b'%', HEX[(byte >> 4) as usize], HEX[(byte & 15) as usize]]);
        } else {
            out.push(byte);
        }
    }
}

fn unhex(byte: u8) -> u8 {
    match byte {
        b'0'..=b'9' => byte - b'0',
        b'a'..=b'f' => byte - b'a' + 10,
        b'A'..=b'F' => byte - b'A' + 10,
        _ => 0,
    }
}

fn unescape(value: &[u8], mode: Encoding) -> Result<Vec<u8>, ()> {
    let mut index = 0;
    while index < value.len() {
        match value[index] {
            b'%' => {
                if index + 2 >= value.len()
                    || !value[index + 1].is_ascii_hexdigit()
                    || !value[index + 2].is_ascii_hexdigit()
                {
                    return Err(());
                }
                let is_25 = &value[index..index + 3] == b"%25";
                if mode == Encoding::Host && unhex(value[index + 1]) < 8 && !is_25 {
                    return Err(());
                }
                if mode == Encoding::Zone {
                    let decoded = (unhex(value[index + 1]) << 4) | unhex(value[index + 2]);
                    if !is_25 && decoded != b' ' && should_escape(decoded, Encoding::Host) {
                        return Err(());
                    }
                }
                index += 3;
            }
            byte => {
                if matches!(mode, Encoding::Host | Encoding::Zone)
                    && byte < 0x80
                    && should_escape(byte, mode)
                {
                    return Err(());
                }
                index += 1;
            }
        }
    }
    let mut result = Vec::with_capacity(value.len());
    let mut index = 0;
    while index < value.len() {
        if value[index] == b'%' {
            result.push((unhex(value[index + 1]) << 4) | unhex(value[index + 2]));
            index += 3;
        } else {
            result.push(value[index]);
            index += 1;
        }
    }
    Ok(result)
}

fn get_scheme(raw: &[u8]) -> Result<(&[u8], &[u8]), ()> {
    for (index, &byte) in raw.iter().enumerate() {
        match byte {
            b'a'..=b'z' | b'A'..=b'Z' => {}
            b'0'..=b'9' | b'+' | b'-' | b'.' => {
                if index == 0 {
                    return Ok((b"", raw));
                }
            }
            b':' => {
                if index == 0 {
                    return Err(());
                }
                return Ok((&raw[..index], &raw[index + 1..]));
            }
            _ => return Ok((b"", raw)),
        }
    }
    Ok((b"", raw))
}

fn valid_optional_port(port: &[u8]) -> bool {
    if port.is_empty() {
        return true;
    }
    let Some(digits) = port.strip_prefix(b":") else {
        return false;
    };
    digits.iter().all(u8::is_ascii_digit)
}

fn rfind(value: &[u8], needle: u8) -> Option<usize> {
    value.iter().rposition(|&byte| byte == needle)
}

fn find(value: &[u8], needle: u8) -> Option<usize> {
    value.iter().position(|&byte| byte == needle)
}

fn find_slice(value: &[u8], needle: &[u8]) -> Option<usize> {
    value
        .windows(needle.len())
        .position(|window| window == needle)
}

/// Go `netip.ParseAddr` acceptance for a bracketed host, rejecting IPv4.
fn valid_ip_literal(host: &[u8]) -> bool {
    let Ok(text) = std::str::from_utf8(host) else {
        return false;
    };
    let (address, zone) = match text.split_once('%') {
        Some((address, zone)) => (address, Some(zone)),
        None => (text, None),
    };
    if zone.is_some_and(str::is_empty) {
        return false;
    }
    if address.parse::<std::net::Ipv4Addr>().is_ok() {
        return false;
    }
    address.parse::<std::net::Ipv6Addr>().is_ok()
}

fn parse_host(scheme: &str, host: &[u8]) -> Result<Vec<u8>, ()> {
    match rfind(host, b'[') {
        Some(open) if open > 0 => Err(()),
        Some(_) => {
            let close = rfind(host, b']').ok_or(())?;
            let colon_port = &host[close + 1..];
            if !valid_optional_port(colon_port) {
                return Err(());
            }
            let colon_port = unescape(colon_port, Encoding::Host)?;
            let hostname = host.get(1..close).ok_or(())?;
            let mut unescaped = match find_slice(hostname, b"%25") {
                Some(zone) => {
                    let mut part = unescape(&hostname[..zone], Encoding::Host)?;
                    part.extend(unescape(&hostname[zone..], Encoding::Zone)?);
                    part
                }
                None => unescape(hostname, Encoding::Host)?,
            };
            if !valid_ip_literal(&unescaped) {
                return Err(());
            }
            unescaped.insert(0, b'[');
            unescaped.push(b']');
            unescaped.extend(colon_port);
            Ok(unescaped)
        }
        None => {
            if let Some(first) = find(host, b':') {
                let last = rfind(host, b':').unwrap_or(first);
                let colon = if last != first && (scheme == "http" || scheme == "https") {
                    first
                } else {
                    last
                };
                if !valid_optional_port(&host[colon..]) {
                    return Err(());
                }
            }
            unescape(host, Encoding::Host)
        }
    }
}

fn valid_userinfo(value: &[u8]) -> bool {
    value.iter().all(|&byte| {
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
    })
}

/// Go `url.Parse`; errors carry no detail because callers replace them.
pub(crate) fn parse(raw: &str) -> Result<Url, ()> {
    let raw = raw.as_bytes();
    let (without_fragment, fragment) = match find(raw, b'#') {
        Some(index) => (&raw[..index], &raw[index + 1..]),
        None => (raw, &b""[..]),
    };
    let url = parse_without_fragment(without_fragment)?;
    if !fragment.is_empty() {
        unescape(fragment, Encoding::Fragment)?;
    }
    Ok(url)
}

fn parse_without_fragment(raw: &[u8]) -> Result<Url, ()> {
    if raw.iter().any(|&byte| byte < 0x20 || byte == 0x7f) {
        return Err(());
    }
    let mut url = Url::default();
    if raw == b"*" {
        url.path = b"*".to_vec();
        return Ok(url);
    }
    let (scheme, mut rest) = get_scheme(raw)?;
    url.scheme = String::from_utf8_lossy(scheme).to_ascii_lowercase();
    let question_marks = rest.iter().filter(|&&byte| byte == b'?').count();
    if rest.ends_with(b"?") && question_marks == 1 {
        url.force_query = true;
        rest = &rest[..rest.len() - 1];
    } else if let Some(index) = find(rest, b'?') {
        url.raw_query = rest[index + 1..].to_vec();
        rest = &rest[..index];
    }
    if !rest.starts_with(b"/") {
        if !url.scheme.is_empty() {
            url.opaque = rest.to_vec();
            return Ok(url);
        }
        let segment = match find(rest, b'/') {
            Some(index) => &rest[..index],
            None => rest,
        };
        if segment.contains(&b':') {
            return Err(());
        }
    }
    if (!url.scheme.is_empty() || !rest.starts_with(b"///")) && rest.starts_with(b"//") {
        let authority_and_path = &rest[2..];
        let (authority, path) = match find(authority_and_path, b'/') {
            Some(index) => (&authority_and_path[..index], &authority_and_path[index..]),
            None => (authority_and_path, &b""[..]),
        };
        let at = rfind(authority, b'@');
        url.host = parse_host(
            &url.scheme,
            match at {
                Some(index) => &authority[index + 1..],
                None => authority,
            },
        )?;
        if let Some(index) = at {
            let userinfo = &authority[..index];
            if !valid_userinfo(userinfo) {
                return Err(());
            }
            url.user = Some(match find(userinfo, b':') {
                None => (unescape(userinfo, Encoding::UserPassword)?, None),
                Some(colon) => (
                    unescape(&userinfo[..colon], Encoding::UserPassword)?,
                    Some(unescape(&userinfo[colon + 1..], Encoding::UserPassword)?),
                ),
            });
        }
        rest = path;
    } else if !url.scheme.is_empty() && rest.starts_with(b"/") {
        url.omit_host = true;
    }
    url.path = unescape(rest, Encoding::Path)?;
    Ok(url)
}

impl Url {
    /// Go `URL.String` for a URL whose `RawPath`, `RawQuery` and `Fragment`
    /// were cleared.
    pub(crate) fn to_go_string(&self) -> String {
        let mut out: Vec<u8> = Vec::new();
        if !self.scheme.is_empty() {
            out.extend_from_slice(self.scheme.as_bytes());
            out.push(b':');
        }
        if !self.opaque.is_empty() {
            out.extend_from_slice(&self.opaque);
        } else {
            if (!self.scheme.is_empty() || !self.host.is_empty() || self.user.is_some())
                && !(self.omit_host && self.host.is_empty() && self.user.is_none())
            {
                if !self.host.is_empty() || !self.path.is_empty() || self.user.is_some() {
                    out.extend_from_slice(b"//");
                }
                if let Some((user, password)) = &self.user {
                    escape(user, Encoding::UserPassword, &mut out);
                    if let Some(password) = password {
                        out.push(b':');
                        escape(password, Encoding::UserPassword, &mut out);
                    }
                    out.push(b'@');
                }
                escape(&self.host, Encoding::Host, &mut out);
            }
            let mut path = Vec::new();
            if self.path == b"*" {
                path.push(b'*');
            } else {
                escape(&self.path, Encoding::Path, &mut path);
            }
            if !path.is_empty() && path[0] != b'/' && !self.host.is_empty() {
                out.push(b'/');
            }
            if out.is_empty() {
                let segment = match find(&path, b'/') {
                    Some(index) => &path[..index],
                    None => &path[..],
                };
                if segment.contains(&b':') {
                    out.extend_from_slice(b"./");
                }
            }
            out.extend_from_slice(&path);
        }
        if self.force_query || !self.raw_query.is_empty() {
            out.push(b'?');
            out.extend_from_slice(&self.raw_query);
        }
        String::from_utf8_lossy(&out).into_owned()
    }
}
