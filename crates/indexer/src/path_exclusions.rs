//! Configured workspace-relative path exclusions.

use swlsp_pathmatch::{CompileError, Matcher};

const EXCLUDED_DIRECTORY_PROBE: &str = ".shopware-lsp-descendant";

/// Matches workspace-relative paths excluded by configuration. Immutable and
/// safe for concurrent scanner and watcher use.
#[derive(Debug, Clone, Default)]
pub struct PathExclusions {
    matcher: Matcher,
}

impl PathExclusions {
    pub fn new<S: AsRef<str>>(patterns: &[S]) -> Result<Self, CompileError> {
        Ok(Self {
            matcher: Matcher::compile(patterns)?,
        })
    }

    /// Reports whether a workspace-relative file or directory is excluded.
    pub fn excludes(&self, relative_path: &str) -> bool {
        let relative_path = normalize_relative_path(relative_path);
        !relative_path.is_empty() && self.matcher.matches(&relative_path)
    }

    /// Reports whether a directory itself or all of its immediate descendants
    /// are excluded. The descendant probe makes patterns ending in `/**`
    /// pruneable without incorrectly pruning for file-only patterns such as
    /// `**/*.php`.
    pub fn excludes_directory(&self, relative_path: &str) -> bool {
        let relative_path = normalize_relative_path(relative_path);
        !relative_path.is_empty()
            && (self.matcher.matches(&relative_path)
                || self
                    .matcher
                    .matches(&format!("{relative_path}/{EXCLUDED_DIRECTORY_PROBE}")))
    }
}

pub(crate) fn normalize_relative_path(relative_path: &str) -> String {
    let mut normalized = relative_path.replace('\\', "/");
    if let Some(stripped) = normalized.strip_prefix("./") {
        normalized = stripped.to_owned();
    }
    if normalized == "." {
        return String::new();
    }
    normalized.trim_matches('/').to_owned()
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn directory_probe_prunes_only_descendant_patterns() {
        let exclusions = PathExclusions::new(&["**/generated/**", "**/*.php"]).unwrap();
        assert!(exclusions.excludes_directory("src/generated"));
        assert!(!exclusions.excludes_directory("src"));
        assert!(exclusions.excludes("src/Service.php"));
        assert!(!exclusions.excludes("."));
        assert!(!exclusions.excludes(""));
    }
}
