//! Lexical path helpers on UTF-8 strings.

pub(crate) use swlsp_uriutil::filepath::{base, clean, ext, is_abs, join, rel, to_slash};

/// The cleaned directory part of `path`; `.` without one. Volume names get
/// no special handling.
pub(crate) fn dir(path: &str) -> String {
    match path.rfind(['/', std::path::MAIN_SEPARATOR]) {
        Some(index) => clean(&path[..=index]),
        None => ".".to_owned(),
    }
}

/// The last element of a slash-separated path, ignoring trailing slashes;
/// `.` for an empty path and `/` for the root.
pub(crate) fn slash_base(path: &str) -> String {
    if path.is_empty() {
        return ".".to_owned();
    }
    let trimmed = path.trim_end_matches('/');
    if trimmed.is_empty() {
        return "/".to_owned();
    }
    trimmed.rsplit('/').next().unwrap_or(trimmed).to_owned()
}
