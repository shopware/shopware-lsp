//! Go `path/filepath` helpers used throughout the package.

pub(crate) use swlsp_uriutil::filepath::{base, clean, dir, ext, is_abs, join, to_slash};

/// Go `filepath.Join` for two elements.
pub(crate) fn join2(left: &str, right: &str) -> String {
    join(&[left, right])
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn dir_matches_go() {
        assert_eq!(dir("/a/b/c.js"), "/a/b");
        assert_eq!(dir("c.js"), ".");
        assert_eq!(dir("/c.js"), "/");
        assert_eq!(dir("/a/b/"), "/a/b");
        assert_eq!(dir(""), ".");
    }
}
