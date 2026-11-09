//! Which files the background workspace scan parses.

use swlsp_language::LanguageId;

use crate::paths;

/// The language registered for `path`'s extension.
pub fn language_for_path(path: &str) -> Option<LanguageId> {
    let extension = paths::ext(path);
    let extension = extension.strip_prefix('.')?;
    if extension.is_empty() {
        return None;
    }
    let lowered = extension.to_ascii_lowercase();
    LanguageId::ALL
        .into_iter()
        .find(|language| language.extensions().contains(&lowered.as_str()))
}

/// Reports whether `path` belongs to a language included in normal workspace
/// scans. Callers that walk a directory should also apply
/// [`should_skip_relative_path`](crate::should_skip_relative_path) to
/// directory entries.
pub fn is_scanned_path(path: &str) -> bool {
    const PHAR_PHP_SUFFIX: &str = ".phar.php";
    if path.len() >= PHAR_PHP_SUFFIX.len()
        && path.is_char_boundary(path.len() - PHAR_PHP_SUFFIX.len())
        && path[path.len() - PHAR_PHP_SUFFIX.len()..].eq_ignore_ascii_case(PHAR_PHP_SUFFIX)
    {
        return false;
    }
    // Plain HTML uses the Twig frontend for open-document framework features,
    // but large generated frontend trees commonly contain thousands of HTML
    // artifacts. Keep those documents available on demand without adding them
    // to the persistent workspace scan.
    if paths::ext(path).eq_ignore_ascii_case(".html") {
        return false;
    }
    language_for_path(path).is_some()
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn plain_html_is_parsed_on_demand_but_not_background_scanned() {
        assert_eq!(
            language_for_path("/workspace/public/index.html"),
            Some(LanguageId::Twig)
        );
        assert!(!is_scanned_path("/workspace/public/index.html"));
        assert!(is_scanned_path("/workspace/views/page.html.twig"));
    }

    #[test]
    fn phar_php_stubs_are_not_scanned() {
        assert!(!is_scanned_path("/workspace/vendor/bin/tool.PHAR.php"));
        assert!(is_scanned_path("/workspace/src/Tool.php"));
        assert!(!is_scanned_path("/workspace/src/README"));
    }
}
