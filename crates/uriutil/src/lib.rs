//! File URI <-> operating-system path conversion (port of `internal/uriutil`)
//! and the URL escaping helpers shared by the other crates.
//!
//! Incoming URIs are parsed with the `url` crate. Outgoing file URIs keep the
//! escaping the Go server produced (and editors accept): every byte except
//! RFC 3986 unreserved characters, `/` and `$&+,:;=@` is percent-encoded and
//! Windows drive letters are written as `/C:/...`. Paths use the
//! Go-compatible lexical helpers in [`filepath`].

pub mod filepath;

mod quote;

use percent_encoding::{AsciiSet, NON_ALPHANUMERIC, percent_decode_str, percent_encode};
use url::Url;

pub use quote::go_quote;

/// Bytes percent-encoded in a URL path: everything except RFC 3986
/// unreserved characters, `/` and the sub-delimiters `$&+,:;=@`.
pub const PATH: &AsciiSet = &NON_ALPHANUMERIC
    .remove(b'-')
    .remove(b'_')
    .remove(b'.')
    .remove(b'~')
    .remove(b'/')
    .remove(b'$')
    .remove(b'&')
    .remove(b'+')
    .remove(b',')
    .remove(b':')
    .remove(b';')
    .remove(b'=')
    .remove(b'@');

/// Bytes percent-encoded in a single path segment: [`PATH`] plus `/`, `,`
/// and `;`.
pub const PATH_SEGMENT: &AsciiSet = &PATH.add(b'/').add(b',').add(b';');

/// Error returned by [`path`].
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct UriError(String);

impl std::fmt::Display for UriError {
    fn fmt(&self, formatter: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        formatter.write_str(&self.0)
    }
}

impl std::error::Error for UriError {}

/// Converts an LSP file URI to an operating-system path. Plain paths are
/// accepted for compatibility with older clients (Go `uriutil.Path`).
pub fn path(value: &str) -> Result<String, UriError> {
    if value.is_empty() {
        return Err(UriError("empty URI".to_owned()));
    }
    // The URL standard silently strips tabs and newlines; reject them instead.
    if value.bytes().any(|byte| byte < 0x20 || byte == 0x7f) {
        return Err(UriError(format!(
            "parse URI {}: invalid control character in URI",
            go_quote(value)
        )));
    }
    let parsed = match Url::parse(value) {
        Ok(parsed) => parsed,
        Err(url::ParseError::RelativeUrlWithoutBase) => return Ok(filepath::clean(value)),
        Err(error) => {
            return Err(UriError(format!("parse URI {}: {error}", go_quote(value))));
        }
    };
    if parsed.scheme() != "file" {
        return Err(UriError(format!(
            "unsupported URI scheme {}",
            go_quote(parsed.scheme())
        )));
    }
    let mut path = percent_decode_str(parsed.path())
        .decode_utf8_lossy()
        .into_owned();
    // The `url` crate already maps a `localhost` host to the empty host.
    if let Some(host) = parsed.host_str().filter(|host| !host.is_empty()) {
        path = format!("//{host}{path}");
    }
    if cfg!(windows) {
        let bytes = path.as_bytes();
        if bytes.len() >= 3 && bytes[0] == b'/' && bytes[2] == b':' {
            path.remove(0);
        }
    }
    Ok(filepath::from_slash(&path))
}

/// Converts a path to a standards-compliant escaped file URI (Go
/// `uriutil.FileURI`).
pub fn file_uri(path: &str) -> String {
    let cleaned = filepath::clean(path);
    let mut slash_path = filepath::to_slash(&cleaned);
    if cfg!(windows) && slash_path.len() >= 2 && slash_path.as_bytes()[1] == b':' {
        slash_path.insert(0, '/');
    }
    format!("file://{}", percent_encode(slash_path.as_bytes(), PATH))
}

/// Creates a file URI whose fragment is already decoded (Go
/// `uriutil.FileURIWithFragment`).
pub fn file_uri_with_fragment(path: &str, fragment: &str) -> String {
    let uri = file_uri(path);
    if fragment.is_empty() {
        return uri;
    }
    let fragment = fragment.strip_prefix('#').unwrap_or(fragment);
    format!("{uri}#{}", path_escape(fragment))
}

/// Escapes a string for use as one URL path segment (Go `url.PathEscape`).
pub fn path_escape(value: &str) -> String {
    percent_encode(value.as_bytes(), PATH_SEGMENT).to_string()
}

/// Canonical escaped form of a raw (possibly already escaped) URL path, as
/// Go's `URL.EscapedPath` returns it: a validly encoded path is kept
/// verbatim, anything else is decoded and re-encoded with [`PATH`]. Returns
/// `None` for a malformed `%` escape.
pub fn escaped_path(raw: &str) -> Option<String> {
    let bytes = raw.as_bytes();
    for (index, _) in raw.match_indices('%') {
        let valid = bytes
            .get(index + 1..index + 3)
            .is_some_and(|hex| hex.iter().all(u8::is_ascii_hexdigit));
        if !valid {
            return None;
        }
    }
    let already_encoded = bytes
        .iter()
        .all(|&byte| byte.is_ascii_alphanumeric() || b"-._~/!$&'()*+,;=:@[]%".contains(&byte));
    if already_encoded {
        return Some(raw.to_owned());
    }
    let decoded: Vec<u8> = percent_decode_str(raw).collect();
    Some(percent_encode(&decoded, PATH).to_string())
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn file_uri_round_trip() {
        let directory = tempfile::tempdir().unwrap();
        let path = filepath::join(&[
            directory.path().to_str().unwrap(),
            "folder with spaces",
            "file.php",
        ]);
        let uri = file_uri(&path);
        let round_trip = super::path(&uri).unwrap();
        assert_eq!(path, round_trip);
    }

    #[cfg(not(windows))]
    #[test]
    fn file_uri_round_trips_tricky_names() {
        for (path, uri) in [
            ("/tmp/a b.php", "file:///tmp/a%20b.php"),
            ("/tmp/a#b.php", "file:///tmp/a%23b.php"),
            ("/tmp/a?b.php", "file:///tmp/a%3Fb.php"),
            ("/tmp/100%.php", "file:///tmp/100%25.php"),
            ("/tmp/%41.php", "file:///tmp/%2541.php"),
            ("/tmp/a+b.php", "file:///tmp/a+b.php"),
            (
                "/tmp/Ünï/日本.php",
                "file:///tmp/%C3%9Cn%C3%AF/%E6%97%A5%E6%9C%AC.php",
            ),
            ("/tmp/[x] (y)!.php", "file:///tmp/%5Bx%5D%20%28y%29%21.php"),
            ("/tmp/a\\b.php", "file:///tmp/a%5Cb.php"),
            ("/C:/Users/x.php", "file:///C:/Users/x.php"),
        ] {
            assert_eq!(file_uri(path), uri, "{path:?}");
            assert_eq!(super::path(uri).unwrap(), path, "{uri:?}");
        }
    }

    // URIs as VS Code (`vscode-uri`) and Zed (`url` crate) send them.
    #[cfg(not(windows))]
    #[test]
    fn path_decodes_editor_uris() {
        for (uri, expected) in [
            ("file:///tmp/a%40b%3Ac%2B%24.php", "/tmp/a@b:c+$.php"),
            ("file:///c%3A/Users/x.php", "/c:/Users/x.php"),
            ("file:///C:/Users/x.php", "/C:/Users/x.php"),
            ("file:///tmp/a%20b/%C3%A9.php", "/tmp/a b/é.php"),
            ("file:///tmp/%5Bid%5D.php", "/tmp/[id].php"),
            ("file:///tmp/[id].php", "/tmp/[id].php"),
            ("file:///tmp/a b.php", "/tmp/a b.php"),
            ("file:///tmp/é.php", "/tmp/é.php"),
            ("file:///tmp/a%23b.php", "/tmp/a#b.php"),
            ("file:///tmp/a%3Fb.php", "/tmp/a?b.php"),
            ("file:///tmp/100%25.php", "/tmp/100%.php"),
        ] {
            assert_eq!(super::path(uri).unwrap(), expected, "{uri:?}");
        }
    }

    #[test]
    fn path_rejects_non_file_uri() {
        assert!(super::path("https://example.com/file.php").is_err());
    }

    #[cfg(not(windows))]
    #[test]
    fn path_edge_cases() {
        for (input, expected) in [
            ("file:///tmp/a%20b", Ok("/tmp/a b")),
            ("file://localhost/tmp/x.php", Ok("/tmp/x.php")),
            ("file://server/share/x.php", Ok("//server/share/x.php")),
            ("file:///C:/Users/x.php", Ok("/C:/Users/x.php")),
            ("file:///tmp/%C3%A9.php", Ok("/tmp/é.php")),
            ("file:///tmp/a+b.php", Ok("/tmp/a+b.php")),
            ("file:///tmp/x.php#L10", Ok("/tmp/x.php")),
            ("file:///tmp/x.php?query", Ok("/tmp/x.php")),
            ("/tmp/../tmp/./x.php", Ok("/tmp/x.php")),
            ("relative/../x.php", Ok("x.php")),
            ("", Err("empty URI")),
            (
                "file://a b/x",
                Err(r#"parse URI "file://a b/x": invalid international domain name"#),
            ),
            (
                "file://h:x/p",
                Err(r#"parse URI "file://h:x/p": invalid international domain name"#),
            ),
            // Accepted by the URL standard, unlike Go's `net/url`.
            ("file:relative.php", Ok("/relative.php")),
            ("file:///a%zz", Ok("/a%zz")),
            ("file:///tmp/../x.php", Ok("/x.php")),
            ("1a:b", Ok("1a:b")),
            (":foo", Ok(":foo")),
            (
                "file:///tmp/\nx",
                Err(r#"parse URI "file:///tmp/\nx": invalid control character in URI"#),
            ),
            ("C:\\x", Err(r#"unsupported URI scheme "c""#)),
            ("HTTPS://x", Err(r#"unsupported URI scheme "https""#)),
            ("FILE:///tmp/x", Ok("/tmp/x")),
        ] {
            let actual = super::path(input).map_err(|error| error.to_string());
            assert_eq!(
                actual,
                expected.map(str::to_owned).map_err(str::to_owned),
                "{input:?}"
            );
        }
    }

    #[cfg(not(windows))]
    #[test]
    fn file_uri_matches_go_escaping() {
        assert_eq!(
            file_uri("/tmp/a b!?#é[x]"),
            "file:///tmp/a%20b%21%3F%23%C3%A9%5Bx%5D"
        );
        assert_eq!(
            file_uri("/tmp/a:b@c&d=e+f$g,h;i"),
            "file:///tmp/a:b@c&d=e+f$g,h;i"
        );
        assert_eq!(file_uri("/tmp/../x/./y/"), "file:///x/y");
        assert_eq!(file_uri("relative/x"), "file://relative/x");
        assert_eq!(
            file_uri_with_fragment("/tmp/x.php", "#a/b c;d#e"),
            "file:///tmp/x.php#a%2Fb%20c%3Bd%23e"
        );
        assert_eq!(
            file_uri_with_fragment("/tmp/x.php", ""),
            "file:///tmp/x.php"
        );
        assert_eq!(path_escape("a/b c;d#e"), "a%2Fb%20c%3Bd%23e");
        assert_eq!(path_escape("a$&+:=@,?"), "a$&+:=@%2C%3F");
    }

    #[test]
    fn escaped_path_matches_go() {
        assert_eq!(
            escaped_path("/edit/foo%2Ebar~v1").as_deref(),
            Some("/edit/foo%2Ebar~v1")
        );
        assert_eq!(escaped_path("/a!b[c]").as_deref(), Some("/a!b[c]"));
        assert_eq!(escaped_path("/a b!").as_deref(), Some("/a%20b%21"));
        assert_eq!(escaped_path("/a%41 é").as_deref(), Some("/aA%20%C3%A9"));
        assert_eq!(escaped_path("catalog/42").as_deref(), Some("catalog/42"));
        assert_eq!(escaped_path(""), Some(String::new()));
        assert_eq!(escaped_path("/a%zz"), None);
        assert_eq!(escaped_path("/a%4"), None);
    }
}
