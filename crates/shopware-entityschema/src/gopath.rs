//! Lexical path helpers with Go `path/filepath` semantics on Unix.

/// Go `filepath.Clean`.
pub(crate) fn clean(path: &str) -> String {
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

/// Go `filepath.Join`.
pub(crate) fn join(parts: &[&str]) -> String {
    let joined = parts
        .iter()
        .filter(|part| !part.is_empty())
        .copied()
        .collect::<Vec<_>>()
        .join("/");
    if joined.is_empty() {
        return String::new();
    }
    clean(&joined)
}

/// Go `filepath.Dir`.
pub(crate) fn dir(path: &str) -> String {
    match path.rfind('/') {
        Some(index) => clean(&path[..=index]),
        None => clean("."),
    }
}

/// Go `filepath.Base`.
pub(crate) fn base(path: &str) -> String {
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

/// Go `filepath.Ext`.
pub(crate) fn ext(path: &str) -> &str {
    for (index, byte) in path.bytes().enumerate().rev() {
        match byte {
            b'/' => return "",
            b'.' => return &path[index..],
            _ => {}
        }
    }
    ""
}

/// Go `filepath.Rel` for lexical paths; `None` when no relative path exists.
pub(crate) fn rel(base_path: &str, target: &str) -> Option<String> {
    let base_path = clean(base_path);
    let target = clean(target);
    if base_path == target {
        return Some(".".to_owned());
    }
    if base_path.starts_with('/') != target.starts_with('/') {
        return None;
    }
    let components = |path: &str| -> Vec<String> {
        if path == "." {
            Vec::new()
        } else {
            path.trim_start_matches('/')
                .split('/')
                .map(str::to_owned)
                .collect()
        }
    };
    let base_parts = components(&base_path);
    let target_parts = components(&target);
    let common = base_parts
        .iter()
        .zip(&target_parts)
        .take_while(|(left, right)| left == right)
        .count();
    if base_parts[common..].iter().any(|part| part == "..") {
        return None;
    }
    let mut result: Vec<&str> = vec![".."; base_parts.len() - common];
    result.extend(target_parts[common..].iter().map(String::as_str));
    Some(result.join("/"))
}

/// Whether `candidate` is `root` or below it.
pub(crate) fn within_directory(root: &str, candidate: &str) -> bool {
    match rel(root, candidate) {
        Some(relative) => relative != ".." && !relative.starts_with("../"),
        None => false,
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn matches_go_filepath() {
        assert_eq!(clean("/a/b/../c/./d/"), "/a/c/d");
        assert_eq!(clean("../../a"), "../../a");
        assert_eq!(clean("/../a"), "/a");
        assert_eq!(dir("/a/b/c.php"), "/a/b");
        assert_eq!(dir("c.php"), ".");
        assert_eq!(base("/a/b/"), "b");
        assert_eq!(ext("/a.b/c"), "");
        assert_eq!(ext("/a/c.PHP"), ".PHP");
        assert_eq!(rel("/a/b", "/a/b/c/d").as_deref(), Some("c/d"));
        assert_eq!(rel("/a/b", "/a/c").as_deref(), Some("../c"));
        assert!(within_directory("/a", "/a"));
        assert!(within_directory("/a", "/a/b"));
        assert!(!within_directory("/a", "/ab"));
        assert!(!within_directory("/a/b", "/a"));
    }
}
