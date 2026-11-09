//! Symfony resource-pattern matching: brace expansion, plain directory
//! prefixes, and shell globs where `*` stays within a directory and `**`
//! crosses directories.

use crate::util::{clean_path, relative_path};

/// Whether `candidate` lies inside or equals a Symfony resource pattern.
pub(crate) fn path_matches_config_pattern(candidate: &str, pattern: &str) -> bool {
    let candidate = clean_path(candidate);
    for expanded in expand_path_braces(&clean_path(pattern)) {
        if !has_path_meta(&expanded) {
            if candidate == clean_path(&expanded) || path_within(&candidate, &expanded) {
                return true;
            }
            continue;
        }
        if swlsp_pathmatch::path_glob(&expanded, &candidate, true) {
            return true;
        }
    }
    false
}

pub(crate) fn expand_path_braces(pattern: &str) -> Vec<String> {
    let Some(open) = pattern.find('{') else {
        return vec![pattern.to_owned()];
    };
    let Some(close) = pattern[open + 1..].find('}') else {
        return vec![pattern.to_owned()];
    };
    let close = close + open + 1;
    let mut expanded = Vec::new();
    for alternative in pattern[open + 1..close].split(',') {
        let value = format!(
            "{}{}{}",
            &pattern[..open],
            alternative,
            &pattern[close + 1..]
        );
        expanded.extend(expand_path_braces(&value));
    }
    expanded
}

fn has_path_meta(path: &str) -> bool {
    path.contains(['*', '?', '['])
}

fn path_within(candidate: &str, directory: &str) -> bool {
    match relative_path(directory, candidate) {
        None => false,
        Some(relative) => relative != "." && relative != ".." && !relative.starts_with("../"),
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn star_stays_within_a_directory_and_double_star_crosses() {
        assert!(path_matches_config_pattern("/p/src/b/c", "/p/src/*/c"));
        assert!(!path_matches_config_pattern("/p/src/b/x/c", "/p/src/*/c"));
        assert!(path_matches_config_pattern("/p/src/cd", "/p/src/[bc]d"));
        assert!(!path_matches_config_pattern("/p/src/cd", "/p/src/[^bc]d"));
        assert!(!path_matches_config_pattern("/p/src/a", "/p/src/["));
        assert!(path_matches_config_pattern("/p/src/A.php", "/p/src/"));
        assert!(path_matches_config_pattern(
            "/p/src/X/A.php",
            "/p/src/**/*.php"
        ));
        assert!(path_matches_config_pattern(
            "/p/src/A.php",
            "/p/{lib,src}/*.php"
        ));
        assert!(!path_matches_config_pattern("/p/srcx/A.php", "/p/src"));
    }
}
