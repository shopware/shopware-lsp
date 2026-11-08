//! Twig documentation comments (port of `documentation.go`).

use swlsp_parser_twig::syntax::{BODY, TWIG_COMMENT};
use swlsp_syntax::SyntaxNode;
use swlsp_syntax::rowan::NodeOrToken;

/// The normalized contents of a Twig 3.29 documentation comment. Accepts
/// construct comments (`{## ... #}` or `{## ... ##}`) and line-scoped binding
/// comments (`## ...`).
pub fn documentation_comment_text(source: &str) -> Option<String> {
    let value = source.trim();
    if value.starts_with("{##") {
        let bytes = value.as_bytes();
        let mut opening_end = "{##".len();
        if opening_end < bytes.len() && (bytes[opening_end] == b'-' || bytes[opening_end] == b'~') {
            opening_end += 1;
        }
        for closing in ["-##}", "~##}", "##}", "-#}", "~#}", "#}"] {
            if !value.ends_with(closing) {
                continue;
            }
            let closing_start = value.len() - closing.len();
            // `{##}` is Twig's empty regular comment, not documentation.
            if closing_start < opening_end {
                return None;
            }
            return Some(normalize_documentation(&value[opening_end..closing_start]));
        }
        return None;
    }
    if let Some(line) = value.strip_prefix("##") {
        let line = match line.find(['\r', '\n']) {
            Some(end) => &line[..end],
            None => line,
        };
        return Some(normalize_documentation(line));
    }
    None
}

/// Consecutive documentation comments associated with `node`. Regular Twig
/// comments do not interrupt attachment, while any other sibling is a
/// semantic boundary.
pub fn documentation_before(node: &SyntaxNode) -> String {
    let mut documentation = Vec::new();
    let mut sibling = node.prev_sibling_or_token();
    while let Some(NodeOrToken::Node(comment)) = sibling {
        if comment.kind() != TWIG_COMMENT {
            break;
        }
        if let Some(value) = documentation_comment_text(&comment.text().to_string())
            && !value.is_empty()
        {
            documentation.push(value);
        }
        sibling = comment.prev_sibling_or_token();
    }
    documentation.reverse();
    documentation.join("\n")
}

/// Documentation attached to the construct or binding containing `node`. A
/// body is a boundary.
pub fn documentation_for_node(node: &SyntaxNode) -> String {
    for current in node.ancestors() {
        if current.kind() == BODY {
            return String::new();
        }
        let documentation = documentation_before(&current);
        if !documentation.is_empty() {
            return documentation;
        }
    }
    String::new()
}

pub(crate) fn normalize_documentation(value: &str) -> String {
    value
        .replace("\r\n", "\n")
        .replace('\r', "\n")
        .trim()
        .to_owned()
}
