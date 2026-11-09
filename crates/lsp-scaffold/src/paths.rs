//! Filesystem path and file helpers for scaffold targets.

use std::io;

pub(crate) use swlsp_uriutil::filepath::{abs, base, clean, dir, ext, join, rel, to_slash};

/// `path` with every symbolic link resolved, or `None` when it does not
/// exist or cannot be resolved.
pub(crate) fn canonical_path(path: &str) -> Option<String> {
    std::fs::canonicalize(path)
        .ok()
        .map(|resolved| resolved.to_string_lossy().into_owned())
}

pub(crate) fn resolved_directory_path(path: &str) -> String {
    canonical_path(path).unwrap_or_else(|| clean(path))
}

/// Whether a [`rel`] result leaves its base.
pub(crate) fn escapes(relative: &str) -> bool {
    relative == ".." || relative.starts_with("../")
}

/// `rel` that reports escaping (or an error) as `None`.
pub(crate) fn rel_within(base_path: &str, target: &str) -> Option<String> {
    rel(base_path, target)
        .ok()
        .filter(|relative| !escapes(relative))
}

/// A failed filesystem operation on `path`.
#[derive(Debug, thiserror::Error)]
#[error("cannot {action} {path}: {source}")]
pub(crate) struct FileError {
    action: &'static str,
    path: String,
    #[source]
    source: io::Error,
}

impl FileError {
    pub(crate) fn new(action: &'static str, path: &str, source: io::Error) -> Self {
        Self {
            action,
            path: path.to_owned(),
            source,
        }
    }
}

pub(crate) enum Stat {
    Exists(std::fs::Metadata),
    Missing,
    Failed(io::Error),
}

/// The metadata of `path`, telling a missing path apart from other failures.
pub(crate) fn stat(path: &str) -> Stat {
    match std::fs::metadata(path) {
        Ok(metadata) => Stat::Exists(metadata),
        Err(error) if error.kind() == io::ErrorKind::NotFound => Stat::Missing,
        Err(error) => Stat::Failed(error),
    }
}

/// The content of `path` (invalid UTF-8 replaced), or `None` when it does
/// not exist.
pub(crate) fn read_file(path: &str) -> Result<Option<String>, FileError> {
    match std::fs::read(path) {
        Ok(content) => Ok(Some(String::from_utf8_lossy(&content).into_owned())),
        Err(error) if error.kind() == io::ErrorKind::NotFound => Ok(None),
        Err(error) => Err(FileError::new("read", path, error)),
    }
}
