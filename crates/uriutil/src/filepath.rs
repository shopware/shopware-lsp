//! Lexical path helpers over UTF-8 strings.
//!
//! Paths are stored and compared as strings throughout the server (index
//! keys, URIs, configuration scopes), so these helpers work on `&str`
//! without touching the filesystem and always produce the same string for
//! the same path: [`clean`] yields the shortest lexically equivalent form,
//! which makes paths comparable. On Windows both `\` and `/` are
//! separators and volume names (`C:`, `\\host\share`) are honored;
//! elsewhere only `/` is a separator. [`ext`] includes the leading dot, and
//! [`dir`] and [`base`] return `.` for an empty result.

/// The host path separator.
pub const SEPARATOR: char = if cfg!(windows) { '\\' } else { '/' };

/// Reports whether `byte` is a path separator on the host.
#[inline]
pub fn is_path_separator(byte: u8) -> bool {
    if cfg!(windows) {
        byte == b'\\' || byte == b'/'
    } else {
        byte == b'/'
    }
}

/// `path` with the host separator replaced by `/`.
pub fn to_slash(path: &str) -> String {
    if SEPARATOR == '/' {
        return path.to_owned();
    }
    path.replace(SEPARATOR, "/")
}

/// `path` with `/` replaced by the host separator.
pub fn from_slash(path: &str) -> String {
    if SEPARATOR == '/' {
        return path.to_owned();
    }
    path.replace('/', &SEPARATOR.to_string())
}

/// Length of the leading volume name (`C:` or `\\host\share`) on Windows;
/// always zero elsewhere.
pub fn volume_name_len(path: &str) -> usize {
    if !cfg!(windows) {
        return 0;
    }
    windows_volume_name_len(path)
}

fn windows_volume_name_len(path: &str) -> usize {
    let bytes = path.as_bytes();
    if bytes.len() >= 2 && bytes[1] == b':' && bytes[0].is_ascii_alphabetic() {
        return 2;
    }
    let sep = |b: u8| b == b'\\' || b == b'/';
    // UNC: \\host\share
    if bytes.len() >= 5 && sep(bytes[0]) && sep(bytes[1]) && !sep(bytes[2]) && bytes[2] != b'.' {
        let mut index = 3;
        while index < bytes.len() && !sep(bytes[index]) {
            index += 1;
        }
        if index < bytes.len() {
            index += 1;
            if index < bytes.len() && !sep(bytes[index]) {
                while index < bytes.len() && !sep(bytes[index]) {
                    index += 1;
                }
                return index;
            }
        }
    }
    0
}

/// The leading volume name of `path` (`C:` or `\\host\share` on Windows;
/// empty elsewhere).
pub fn volume_name(path: &str) -> &str {
    &path[..volume_name_len(path)]
}

/// Whether `path` is absolute.
pub fn is_abs(path: &str) -> bool {
    if !cfg!(windows) {
        return path.starts_with('/');
    }
    let length = volume_name_len(path);
    if length == 0 {
        return false;
    }
    if length > 2 {
        return true;
    }
    let rest = &path.as_bytes()[length..];
    !rest.is_empty() && is_path_separator(rest[0])
}

/// Whether `path` is already in `filepath.Clean` form on a `/`-separated
/// platform: non-empty, no empty, `.` or `..` elements, and no trailing
/// separator except for the root. Lets [`clean`] skip rebuilding the most
/// common input, an already clean absolute path.
fn is_clean_slash_path(path: &str) -> bool {
    let bytes = path.as_bytes();
    if bytes.is_empty() {
        return false;
    }
    if bytes == b"/" {
        return true;
    }
    if bytes[bytes.len() - 1] == b'/' {
        return false;
    }
    let body = bytes.strip_prefix(b"/").unwrap_or(bytes);
    body.split(|byte| *byte == b'/')
        .all(|segment| !segment.is_empty() && segment != b"." && segment != b"..")
}

/// Every element of `path` but the last, cleaned; `.` when there is none.
pub fn dir(path: &str) -> String {
    let volume = volume_name(path);
    let bytes = path.as_bytes();
    let mut index = bytes.len();
    while index > volume.len() && !is_path_separator(bytes[index - 1]) {
        index -= 1;
    }
    let directory = clean(&path[volume.len()..index]);
    if directory == "." && volume.len() > 2 {
        return volume.to_owned();
    }
    format!("{volume}{directory}")
}

/// The shortest lexically equivalent path: repeated separators collapse,
/// `.` elements go away, `..` elements remove the element before them (not
/// beyond the root), and an empty result becomes `.`.
pub fn clean(path: &str) -> String {
    if !cfg!(windows) && is_clean_slash_path(path) {
        return path.to_owned();
    }
    clean_general(path)
}

fn clean_general(path: &str) -> String {
    let original = path;
    let volume_length = volume_name_len(path);
    let path = &path[volume_length..];
    if path.is_empty() {
        let bytes = original.as_bytes();
        if volume_length > 1 && is_path_separator(bytes[0]) && is_path_separator(bytes[1]) {
            return from_slash(original);
        }
        return format!("{original}.");
    }
    let bytes = path.as_bytes();
    let rooted = is_path_separator(bytes[0]);
    let length = bytes.len();
    let separator = SEPARATOR as u8;
    let mut out: Vec<u8> = Vec::with_capacity(length + 1);
    let (mut read, mut dotdot) = (0usize, 0usize);
    if rooted {
        out.push(separator);
        read = 1;
        dotdot = 1;
    }
    while read < length {
        if is_path_separator(bytes[read])
            || (bytes[read] == b'.' && (read + 1 == length || is_path_separator(bytes[read + 1])))
        {
            read += 1;
        } else if bytes[read] == b'.'
            && bytes[read + 1] == b'.'
            && (read + 2 == length || is_path_separator(bytes[read + 2]))
        {
            read += 2;
            if out.len() > dotdot {
                // Drops the last written element.
                let mut write = out.len() - 1;
                while write > dotdot && !is_path_separator(out[write]) {
                    write -= 1;
                }
                out.truncate(write);
            } else if !rooted {
                if !out.is_empty() {
                    out.push(separator);
                }
                out.extend_from_slice(b"..");
                dotdot = out.len();
            }
        } else {
            if (rooted && out.len() != 1) || (!rooted && !out.is_empty()) {
                out.push(separator);
            }
            while read < length && !is_path_separator(bytes[read]) {
                out.push(bytes[read]);
                read += 1;
            }
        }
    }
    if out.is_empty() {
        out.push(b'.');
    }
    // Only ASCII bytes were inserted or removed at separator boundaries, so the
    // result is still valid UTF-8.
    let cleaned = String::from_utf8(out).unwrap_or_default();
    from_slash(&format!("{}{}", &original[..volume_length], cleaned))
}

fn same_word(left: &str, right: &str) -> bool {
    if cfg!(windows) {
        left.eq_ignore_ascii_case(right)
    } else {
        left == right
    }
}

/// [`rel`] could not express `target` relative to `base`.
#[derive(Debug, Clone, PartialEq, Eq, thiserror::Error)]
#[error("cannot make {target} relative to {base}")]
pub struct RelError {
    pub base: String,
    pub target: String,
}

/// `target_path` relative to `base_path`, lexically. Fails when one is
/// absolute and the other is not, when the volumes differ, or when the
/// result would have to climb out of `base_path` through a `..` element.
pub fn rel(base_path: &str, target_path: &str) -> Result<String, RelError> {
    let base_volume = volume_name(base_path);
    let target_volume = volume_name(target_path);
    let base_clean = clean(base_path);
    let target_clean = clean(target_path);
    if same_word(&target_clean, &base_clean) {
        return Ok(".".to_owned());
    }
    let mut base = &base_clean[base_volume.len()..];
    let target = &target_clean[target_volume.len()..];
    let separator_string = SEPARATOR.to_string();
    if base == "." {
        base = "";
    } else if base.is_empty() && volume_name_len(base_volume) > 2 {
        base = &separator_string;
    }
    let separator = SEPARATOR as u8;
    let base_slashed = !base.is_empty() && base.as_bytes()[0] == separator;
    let target_slashed = !target.is_empty() && target.as_bytes()[0] == separator;
    let failure = || RelError {
        base: base_path.to_owned(),
        target: target_path.to_owned(),
    };
    if base_slashed != target_slashed || !same_word(base_volume, target_volume) {
        return Err(failure());
    }
    let base_bytes = base.as_bytes();
    let target_bytes = target.as_bytes();
    let (base_length, target_length) = (base_bytes.len(), target_bytes.len());
    let (mut b0, mut bi, mut t0, mut ti) = (0, 0, 0, 0);
    loop {
        while bi < base_length && base_bytes[bi] != separator {
            bi += 1;
        }
        while ti < target_length && target_bytes[ti] != separator {
            ti += 1;
        }
        if !same_word(&target[t0..ti], &base[b0..bi]) {
            break;
        }
        if bi < base_length {
            bi += 1;
        }
        if ti < target_length {
            ti += 1;
        }
        b0 = bi;
        t0 = ti;
    }
    if &base[b0..bi] == ".." {
        return Err(failure());
    }
    if b0 != base_length {
        let separators = base[b0..].matches(SEPARATOR).count();
        let mut result = String::from("..");
        for _ in 0..separators {
            result.push(SEPARATOR);
            result.push_str("..");
        }
        if t0 != target_length {
            result.push(SEPARATOR);
            result.push_str(&target[t0..]);
        }
        return Ok(result);
    }
    Ok(target[t0..].to_owned())
}

/// Joins the non-empty `elements` with the host separator and cleans the
/// result; empty when every element is empty.
pub fn join<S: AsRef<str>>(elements: &[S]) -> String {
    let parts: Vec<&str> = elements
        .iter()
        .map(AsRef::as_ref)
        .filter(|value| !value.is_empty())
        .collect();
    if parts.is_empty() {
        return String::new();
    }
    clean(&parts.join(&SEPARATOR.to_string()))
}

/// `path` made absolute against the working directory, cleaned.
pub fn abs(path: &str) -> std::io::Result<String> {
    if is_abs(path) {
        return Ok(clean(path));
    }
    let working = std::env::current_dir()?;
    let working = working.to_string_lossy();
    Ok(join(&[working.as_ref(), path]))
}

/// The extension of the last element of `path`, with its dot (`.php`);
/// empty when there is none.
pub fn ext(path: &str) -> &str {
    let bytes = path.as_bytes();
    let mut index = bytes.len();
    while index > 0 && !is_path_separator(bytes[index - 1]) {
        index -= 1;
        if bytes[index] == b'.' {
            return &path[index..];
        }
    }
    ""
}

/// The last element of `path` without trailing separators; `.` for an
/// empty path and the separator for the root.
pub fn base(path: &str) -> String {
    if path.is_empty() {
        return ".".to_owned();
    }
    let mut path = path;
    while !path.is_empty() && is_path_separator(path.as_bytes()[path.len() - 1]) {
        path = &path[..path.len() - 1];
    }
    path = &path[volume_name_len(path)..];
    let mut index = path.len();
    while index > 0 && !is_path_separator(path.as_bytes()[index - 1]) {
        index -= 1;
    }
    let path = &path[index..];
    if path.is_empty() {
        return SEPARATOR.to_string();
    }
    path.to_owned()
}

#[cfg(test)]
mod tests {
    use super::*;

    #[cfg(not(windows))]
    #[test]
    fn clean_yields_shortest_equivalent_path() {
        for (input, expected) in [
            ("", "."),
            ("abc", "abc"),
            ("abc/def", "abc/def"),
            ("a/b/c", "a/b/c"),
            (".", "."),
            ("..", ".."),
            ("../..", "../.."),
            ("../../abc", "../../abc"),
            ("/abc", "/abc"),
            ("/", "/"),
            ("abc/", "abc"),
            ("abc/def/", "abc/def"),
            ("/abc/", "/abc"),
            ("abc//def//ghi", "abc/def/ghi"),
            ("//abc", "/abc"),
            ("///abc", "/abc"),
            ("abc/./def", "abc/def"),
            ("/./abc/def", "/abc/def"),
            ("abc/.", "abc"),
            ("abc/def/ghi/../jkl", "abc/def/jkl"),
            ("abc/def/../ghi/../jkl", "abc/jkl"),
            ("abc/def/..", "abc"),
            ("abc/def/../..", "."),
            ("/abc/def/../..", "/"),
            ("abc/def/../../..", ".."),
            ("/abc/def/../../..", "/"),
            ("abc/def/../../../ghi/jkl/../../../mno", "../../mno"),
            ("/../abc", "/abc"),
            ("abc/./../def", "def"),
            ("abc//./../def", "def"),
            ("abc/../../././../def", "../../def"),
        ] {
            assert_eq!(clean(input), expected, "{input}");
        }
    }

    #[cfg(not(windows))]
    #[test]
    fn rel_makes_paths_relative_lexically() {
        for (root, path, expected) in [
            ("a/b", "a/b", Some(".")),
            ("a/b/.", "a/b", Some(".")),
            ("a/b", "a/b/.", Some(".")),
            ("./a/b", "a/b", Some(".")),
            ("a/b", "./a/b", Some(".")),
            ("ab/cd", "ab/cde", Some("../cde")),
            ("ab/cd", "ab/c", Some("../c")),
            ("a/b", "a/b/c/d", Some("c/d")),
            ("a/b", "a/b/../c", Some("../c")),
            ("a/b/../c", "a/b", Some("../b")),
            ("a/b/c", "a/c/d", Some("../../c/d")),
            ("a/b", "c/d", Some("../../c/d")),
            ("a/b/c/d", "a/b", Some("../..")),
            ("a/b/c/d", "a/b/", Some("../..")),
            ("a/b/c/d/", "a/b", Some("../..")),
            ("a/b/c/d/", "a/b/", Some("../..")),
            ("../../a/b", "../../a/b/c/d", Some("c/d")),
            ("/a/b", "/a/b", Some(".")),
            ("/a/b/.", "/a/b", Some(".")),
            ("/a/b", "/a/b/.", Some(".")),
            ("/ab/cd", "/ab/cde", Some("../cde")),
            ("/ab/cd", "/ab/c", Some("../c")),
            ("/a/b", "/a/b/c/d", Some("c/d")),
            ("/a/b", "/a/b/../c", Some("../c")),
            ("/a/b/../c", "/a/b", Some("../b")),
            ("/a/b/c", "/a/c/d", Some("../../c/d")),
            ("/a/b", "/c/d", Some("../../c/d")),
            ("/a/b/c/d", "/a/b", Some("../..")),
            ("/a/b/c/d", "/a/b/", Some("../..")),
            ("/a/b/c/d/", "/a/b", Some("../..")),
            ("/a/b/c/d/", "/a/b/", Some("../..")),
            ("/../../a/b", "/../../a/b/c/d", Some("c/d")),
            (".", "a/b", Some("a/b")),
            (".", "..", Some("..")),
            ("..", ".", None),
            ("..", "a", None),
            ("../..", "..", None),
            ("a", "/a", None),
            ("/a", "a", None),
        ] {
            assert_eq!(rel(root, path).ok().as_deref(), expected, "{root} {path}");
        }
    }

    #[test]
    fn ext_and_base() {
        assert_eq!(ext("path.php"), ".php");
        assert_eq!(ext("path.html.twig"), ".twig");
        assert_eq!(ext("a.dir/b"), "");
        assert_eq!(ext("a.dir/b.php"), ".php");
        assert_eq!(ext("a.dir/"), "");
        assert_eq!(base(""), ".");
        assert_eq!(base("/"), "/");
        assert_eq!(base("a/b/"), "b");
    }
}

#[cfg(all(test, unix))]
mod clean_fast_path_tests {
    use super::*;

    const CASES: &[&str] = &[
        "/",
        "/a",
        "/a/b",
        "a",
        "a/b",
        "/a/b/",
        "a//b",
        "/a/./b",
        "/a/../b",
        "./a",
        "../a",
        "..",
        ".",
        "",
        "/..",
        "a/..",
        "/a.b/c..d/.e",
        "//a",
        "a/b/.",
        "/a/b..",
        "/a/.b",
        "a/../..",
    ];

    /// The fast path only accepts inputs the general algorithm leaves as is.
    #[test]
    fn fast_path_agrees_with_general_clean() {
        for case in CASES {
            if is_clean_slash_path(case) {
                assert_eq!(clean_general(case), *case, "{case:?}");
            }
            assert_eq!(clean(case), clean_general(case), "{case:?}");
        }
        assert!(is_clean_slash_path("/a/b..") && is_clean_slash_path("/a/.b"));
        assert!(!is_clean_slash_path("/a//b") && !is_clean_slash_path("/a/./b"));
        assert!(!is_clean_slash_path("/a/../b") && !is_clean_slash_path("a/"));
    }
}

#[cfg(test)]
mod dir_tests {
    #[test]
    fn dir_drops_the_last_element() {
        use super::dir;
        assert_eq!(dir("/a/b/c.js"), "/a/b");
        assert_eq!(dir("c.js"), ".");
        assert_eq!(dir("/c.js"), "/");
        assert_eq!(dir("/a/b/"), "/a/b");
        assert_eq!(dir(""), ".");
    }
}
