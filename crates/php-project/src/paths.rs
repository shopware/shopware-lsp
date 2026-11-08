//! Lexical path helpers mirroring Go's `path/filepath` semantics.

use std::path::{Component, Path, PathBuf};

/// Lexically normalizes `path` like Go `filepath.Clean`: removes `.`
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

/// Go `filepath.Join(base, path)`: concatenation followed by [`clean`]. An
/// absolute `path` is appended rather than replacing `base`.
pub fn join(base: &Path, path: &str) -> PathBuf {
    let mut joined = base.as_os_str().to_owned();
    if !path.is_empty() {
        joined.push(std::path::MAIN_SEPARATOR_STR);
        joined.push(path);
    }
    clean(Path::new(&joined))
}

/// Go `filepath.Abs`: joins relative paths with the working directory and
/// cleans the result.
pub fn absolute(path: &Path) -> PathBuf {
    if path.is_absolute() {
        return clean(path);
    }
    match std::env::current_dir() {
        Ok(current) => clean(&current.join(path)),
        Err(_) => clean(path),
    }
}
