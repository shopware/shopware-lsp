//! Small Go-compatible helpers private to the Shopware analyzer group.

use swlsp_lsp::{DiagnosticId, Problem};
use swlsp_lsp_protocol as protocol;
use swlsp_syntax::{SyntaxNode, TextRange};
use swlsp_uriutil::filepath;

/// Go `filepath.Dir` for slash-separated paths.
pub(crate) fn go_dir(path: &str) -> String {
    match path.rfind('/') {
        Some(index) => filepath::clean(&path[..=index]),
        None => ".".to_owned(),
    }
}

/// Go `strconv.ParseInt(text, 0, 64)`.
pub(crate) fn go_parse_int(text: &str) -> Option<i64> {
    let (negative, digits) = match text.as_bytes().first()? {
        b'-' => (true, &text[1..]),
        b'+' => (false, &text[1..]),
        _ => (false, text),
    };
    let lower = digits.to_ascii_lowercase();
    let (base, body) = if let Some(rest) = lower.strip_prefix("0x") {
        (16, rest.to_owned())
    } else if let Some(rest) = lower.strip_prefix("0b") {
        (2, rest.to_owned())
    } else if let Some(rest) = lower.strip_prefix("0o") {
        (8, rest.to_owned())
    } else if lower.len() > 1 && lower.starts_with('0') {
        (8, lower[1..].to_owned())
    } else {
        (10, lower)
    };
    if body.is_empty() || body.starts_with(['+', '-']) {
        return None;
    }
    let magnitude = u64::from_str_radix(&body, base).ok()?;
    if negative {
        if magnitude > 1u64 << 63 {
            return None;
        }
        Some((magnitude as i64).wrapping_neg())
    } else {
        i64::try_from(magnitude).ok()
    }
}

/// Go `strings.TrimPrefix(strings.TrimSpace(name), "\\")`.
pub(crate) fn trim_php_name(name: &str) -> &str {
    let name = name.trim();
    name.strip_prefix('\\').unwrap_or(name)
}

pub(crate) fn equal_php_name(left: &str, right: &str) -> bool {
    swlsp_php_types::equal_fold(trim_php_name(left), trim_php_name(right))
}

/// Go `cst.TextRange.Contains`: `start <= offset < end`.
pub(crate) fn range_contains(range: TextRange, offset: u32) -> bool {
    offset >= u32::from(range.start()) && offset < u32::from(range.end())
}

/// Go `rangesTouch`.
pub(crate) fn ranges_touch(left: TextRange, right: TextRange) -> bool {
    range_contains(left, right.start().into())
        || range_contains(right, left.start().into())
        || left == right
}

pub(crate) fn node_text(node: &SyntaxNode) -> String {
    node.text().to_string()
}

pub(crate) fn warning(code: &str, range: TextRange, message: String) -> Problem {
    Problem {
        id: DiagnosticId::from(code),
        range,
        message,
        severity: Some(protocol::DiagnosticSeverity::WARNING),
        source: "shopware-lsp".to_owned(),
        ..Problem::default()
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn parse_int_matches_go_base_prefixes() {
        assert_eq!(go_parse_int("299"), Some(299));
        assert_eq!(go_parse_int("0x1F"), Some(31));
        assert_eq!(go_parse_int("017"), Some(15));
        assert_eq!(go_parse_int("0b11"), Some(3));
        assert_eq!(go_parse_int("0o17"), Some(15));
        assert_eq!(go_parse_int("-5"), Some(-5));
        assert_eq!(go_parse_int("0"), Some(0));
        assert_eq!(go_parse_int("1.5"), None);
        assert_eq!(go_parse_int(""), None);
        assert_eq!(go_parse_int("09"), None);
    }

    #[test]
    fn go_dir_cleans() {
        assert_eq!(go_dir("/a/b/c.json"), "/a/b");
        assert_eq!(go_dir("c.json"), ".");
        assert_eq!(go_dir("/c"), "/");
    }
}
