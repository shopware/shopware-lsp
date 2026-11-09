//! Small helpers private to the Shopware analyzer group.

use swlsp_lsp::{DiagnosticId, Problem};
use swlsp_lsp_protocol as protocol;
use swlsp_syntax::{SyntaxNode, TextRange};

pub(crate) fn trim_php_name(name: &str) -> &str {
    let name = name.trim();
    name.strip_prefix('\\').unwrap_or(name)
}

pub(crate) fn equal_php_name(left: &str, right: &str) -> bool {
    swlsp_textutil::eq_ignore_case(trim_php_name(left), trim_php_name(right))
}

/// `start <= offset < end`.
pub(crate) fn range_contains(range: TextRange, offset: u32) -> bool {
    offset >= u32::from(range.start()) && offset < u32::from(range.end())
}

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
