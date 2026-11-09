//! Lexical helpers for UTF-8 string paths.
//!
//! The scanner, its SQLite state and every indexer exchange paths as UTF-8
//! strings. These helpers clean, join and split them purely lexically (no
//! filesystem access), so persisted paths and the scanner's root containment
//! checks stay stable.

use std::path::MAIN_SEPARATOR;

pub(crate) const SEPARATOR: char = MAIN_SEPARATOR;
pub(crate) const SEPARATOR_STR: &str = std::path::MAIN_SEPARATOR_STR;

/// Whether `path` is already in [`clean`] form on a `/`-separated
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

/// The shortest lexically equivalent path for the platform separator: drops
/// empty and `.` elements, resolves `..` against the preceding element (never
/// above a root) and returns `.` for an empty result. Volume names are not
/// interpreted.
pub fn clean(path: &str) -> String {
    if SEPARATOR == '/' && is_clean_slash_path(path) {
        return path.to_owned();
    }
    clean_general(path)
}

fn clean_general(path: &str) -> String {
    if path.is_empty() {
        return ".".to_owned();
    }
    let rooted = path.starts_with(SEPARATOR) || (SEPARATOR != '/' && path.starts_with('/'));
    let mut parts: Vec<&str> = Vec::new();
    for part in path.split([SEPARATOR, '/']) {
        match part {
            "" | "." => {}
            ".." => {
                if parts.last().is_some_and(|last| *last != "..") {
                    parts.pop();
                } else if !rooted {
                    parts.push("..");
                }
            }
            other => parts.push(other),
        }
    }
    let joined = parts.join(SEPARATOR_STR);
    if rooted {
        format!("{SEPARATOR}{joined}")
    } else if joined.is_empty() {
        ".".to_owned()
    } else {
        joined
    }
}

/// The extension: the suffix starting at the final dot of the last path
/// element, or an empty string.
pub fn ext(path: &str) -> &str {
    for (index, character) in path.char_indices().rev() {
        if character == '.' {
            return &path[index..];
        }
        if character == SEPARATOR || character == '/' {
            break;
        }
    }
    ""
}

/// Joins non-empty elements with the separator and cleans the result; empty
/// when every element is empty.
pub fn join(parts: &[&str]) -> String {
    let joined: Vec<&str> = parts
        .iter()
        .copied()
        .filter(|part| !part.is_empty())
        .collect();
    if joined.is_empty() {
        return String::new();
    }
    clean(&joined.join(SEPARATOR_STR))
}

pub(crate) fn path_within(root: &str, candidate: &str) -> bool {
    relative_path_within(root, candidate).is_some()
}

/// The candidate's path relative to `root` when it is the root or a
/// descendant on a path-component boundary.
pub(crate) fn relative_path_within(root: &str, candidate: &str) -> Option<String> {
    let root = clean(root);
    let candidate = clean(candidate);
    if root == candidate {
        return Some(".".to_owned());
    }
    if candidate.len() > root.len()
        && candidate.starts_with(&root)
        && (root.ends_with(SEPARATOR) || candidate[root.len()..].starts_with(SEPARATOR))
    {
        let relative = &candidate[root.len()..];
        return Some(
            relative
                .strip_prefix(SEPARATOR)
                .unwrap_or(relative)
                .to_owned(),
        );
    }
    // A relative root of "." contains every relative path that does not
    // escape it.
    if root == "."
        && !is_absolute(&candidate)
        && candidate != ".."
        && !candidate.starts_with(&format!("..{SEPARATOR}"))
    {
        return Some(candidate);
    }
    None
}

fn is_absolute(path: &str) -> bool {
    std::path::Path::new(path).is_absolute()
}

pub(crate) fn has_ext_fold(path: &str, wanted: &str) -> bool {
    ext(path).eq_ignore_ascii_case(wanted)
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn clean_normalizes_lexically() {
        for (input, expected) in [
            ("", "."),
            ("/", "/"),
            ("a/b/../c", "a/c"),
            ("/../a", "/a"),
            ("../a/..", ".."),
            ("a//b/./c/", "a/b/c"),
            ("./", "."),
        ] {
            assert_eq!(
                clean(input),
                expected.replace('/', SEPARATOR_STR),
                "{input}"
            );
        }
    }

    #[test]
    fn ext_is_suffix_of_last_element() {
        assert_eq!(ext("a/b.php"), ".php");
        assert_eq!(ext("a.d/b"), "");
        assert_eq!(ext("a/.env"), ".env");
        assert_eq!(ext("file"), "");
    }

    #[test]
    fn relative_path_within_uses_path_component_boundaries() {
        assert_eq!(
            relative_path_within("/workspace", "/workspace").as_deref(),
            Some(".")
        );
        assert_eq!(
            relative_path_within("/workspace", "/workspace/src/a.php").as_deref(),
            Some("src/a.php")
        );
        assert_eq!(
            relative_path_within("/workspace", "/workspace-other/a.php"),
            None
        );
        assert_eq!(relative_path_within("/", "/a").as_deref(), Some("a"));
        assert_eq!(
            relative_path_within("/workspace/", "/workspace/a").as_deref(),
            Some("a")
        );
    }
}

#[cfg(all(test, unix))]
mod clean_fast_path_tests {
    use super::*;

    #[test]
    fn fast_path_agrees_with_general_clean() {
        let cases = [
            "/", "/a", "/a/b", "a", "a/b", "/a/b/", "a//b", "/a/./b", "/a/../b", "./a", "../a",
            "..", ".", "", "/..", "a/..", "//a", "a/b/.", "/a/b..", "/a/.b", "a/../..",
        ];
        for case in cases {
            if is_clean_slash_path(case) {
                assert_eq!(clean_general(case), case, "{case:?}");
            }
            assert_eq!(clean(case), clean_general(case), "{case:?}");
        }
    }
}
