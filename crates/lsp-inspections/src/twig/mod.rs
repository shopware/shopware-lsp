//! Twig template, versioning, snippet and translation inspections (port of
//! the corresponding Go `internal/lsp/inspections` files).

mod snippet;
mod template;
mod translation;
mod versioning;

#[cfg(test)]
mod tests;

pub use snippet::new_snippet;
pub use template::new_template;
pub use translation::new_translation;
pub use versioning::new_twig_versioning;

use serde_json::{Map, Value};

/// Go `mapString`.
pub(crate) fn map_string(values: &Map<String, Value>, key: &str) -> String {
    match values.get(key) {
        Some(Value::String(value)) => value.clone(),
        _ => String::new(),
    }
}

/// Go `path.Clean` for `/`-separated paths.
pub(crate) fn clean_path(path: &str) -> String {
    if path.is_empty() {
        return ".".to_owned();
    }
    let rooted = path.starts_with('/');
    let mut parts: Vec<&str> = Vec::new();
    for part in path.split('/') {
        match part {
            "" | "." => {}
            ".." => {
                if parts.last().is_some_and(|last| *last != "..") {
                    parts.pop();
                } else if !rooted {
                    parts.push("..");
                }
            }
            _ => parts.push(part),
        }
    }
    let joined = parts.join("/");
    match (rooted, joined.is_empty()) {
        (true, _) => format!("/{joined}"),
        (false, true) => ".".to_owned(),
        (false, false) => joined,
    }
}

/// Go `filepath.Base`.
pub(crate) fn base_name(path: &str) -> String {
    if path.is_empty() {
        return ".".to_owned();
    }
    let trimmed = path.trim_end_matches('/');
    if trimmed.is_empty() {
        return "/".to_owned();
    }
    match trimmed.rfind('/') {
        Some(index) => trimmed[index + 1..].to_owned(),
        None => trimmed.to_owned(),
    }
}

#[cfg(test)]
mod path_tests {
    use super::*;

    #[test]
    fn clean_path_matches_go() {
        assert_eq!(clean_path("a/../../b"), "../b");
        assert_eq!(clean_path("/a/../.."), "/");
        assert_eq!(clean_path("a//b/./c/"), "a/b/c");
        assert_eq!(clean_path(""), ".");
        assert_eq!(base_name("/x/y.yaml"), "y.yaml");
    }
}
