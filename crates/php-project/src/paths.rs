//! Lexical path helpers that never touch the filesystem.

use std::path::{Component, Path, PathBuf};

/// Lexically normalizes `path`: removes `.`
/// segments, resolves `..` against preceding names, and drops redundant
/// separators. An empty result becomes `.`.
pub fn clean(path: &Path) -> PathBuf {
    let mut result: Vec<Component<'_>> = Vec::new();
    for component in path.components() {
        match component {
            Component::CurDir => {}
            Component::ParentDir => match result.last() {
                Some(Component::Normal(_)) => {
                    result.pop();
                }
                Some(Component::RootDir | Component::Prefix(_)) => {}
                _ => result.push(component),
            },
            other => result.push(other),
        }
    }
    if result.is_empty() {
        return PathBuf::from(".");
    }
    result.iter().collect()
}

/// Joins `path` to `base` and cleans the result with [`clean`]. An
/// absolute `path` is appended rather than replacing `base`.
pub fn join(base: &Path, path: &str) -> PathBuf {
    let mut joined = base.as_os_str().to_owned();
    if !path.is_empty() {
        joined.push(std::path::MAIN_SEPARATOR_STR);
        joined.push(path);
    }
    clean(Path::new(&joined))
}

/// `path` made absolute against the working directory, then cleaned.
pub fn absolute(path: &Path) -> PathBuf {
    if path.is_absolute() {
        return clean(path);
    }
    match std::env::current_dir() {
        Ok(current) => clean(&current.join(path)),
        Err(_) => clean(path),
    }
}
