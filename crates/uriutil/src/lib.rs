//! File URI <-> operating-system path conversion (port of `internal/uriutil`).
//!
//! URI parsing mirrors Go's `net/url.Parse` for every aspect that influences
//! the result: scheme detection, opaque URIs, authority/host validation,
//! percent-decoding and the exact error texts. Paths use the Go-compatible
//! lexical helpers in [`filepath`].

pub mod filepath;

mod gourl;

pub use gourl::{go_quote, path_escape};

/// Error returned by [`path`]. The message text matches the Go implementation.
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
    let parsed = gourl::parse(value)
        .map_err(|error| UriError(format!("parse URI {}: {error}", go_quote(value))))?;
    if parsed.scheme.is_empty() {
        return Ok(filepath::clean(value));
    }
    if parsed.scheme != "file" {
        return Err(UriError(format!(
            "unsupported URI scheme {}",
            go_quote(&parsed.scheme)
        )));
    }
    // Go: url.PathUnescape(parsed.EscapedPath()) round-trips to parsed.Path.
    let mut path = parsed.path;
    if !parsed.host.is_empty() && parsed.host != "localhost" {
        path = format!("//{}{}", parsed.host, path);
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
    gourl::file_url_string(&slash_path)
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

    #[test]
    fn path_rejects_non_file_uri() {
        assert!(super::path("https://example.com/file.php").is_err());
    }

    // Expected values below were produced by the Go implementation.
    #[cfg(not(windows))]
    #[test]
    fn path_matches_go_edge_cases() {
        for (input, expected) in [
            ("file:///tmp/a%20b", Ok("/tmp/a b")),
            ("file://localhost/tmp/x.php", Ok("/tmp/x.php")),
            ("file://server/share/x.php", Ok("//server/share/x.php")),
            ("file:///C:/Users/x.php", Ok("/C:/Users/x.php")),
            ("file:///tmp/%C3%A9.php", Ok("/tmp/é.php")),
            ("file:///tmp/a+b.php", Ok("/tmp/a+b.php")),
            ("file:///tmp/x.php#L10", Ok("/tmp/x.php")),
            ("file:///tmp/x.php?query", Ok("/tmp/x.php")),
            ("file:relative.php", Ok("")),
            ("/tmp/../tmp/./x.php", Ok("/tmp/x.php")),
            ("relative/../x.php", Ok("x.php")),
            ("", Err("empty URI")),
            (
                "file:///a%zz",
                Err(r#"parse URI "file:///a%zz": parse "file:///a%zz": invalid URL escape "%zz""#),
            ),
            (
                "1a:b",
                Err(
                    r#"parse URI "1a:b": parse "1a:b": first path segment in URL cannot contain colon"#,
                ),
            ),
            (
                ":foo",
                Err(r#"parse URI ":foo": parse ":foo": missing protocol scheme"#),
            ),
            (
                "file://a b/x",
                Err(
                    r#"parse URI "file://a b/x": parse "file://a b/x": invalid character " " in host name"#,
                ),
            ),
            (
                "file://h:x/p",
                Err(
                    r#"parse URI "file://h:x/p": parse "file://h:x/p": invalid port ":x" after host"#,
                ),
            ),
            (
                "file:///tmp/\nx",
                Err(
                    "parse URI \"file:///tmp/\\nx\": parse \"file:///tmp/\\nx\": net/url: invalid control character in URL",
                ),
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
    }
}
