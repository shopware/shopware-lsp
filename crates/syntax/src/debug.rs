//! Debug rendering compatible with the Go `cst.DebugTree` (rowan format).

use std::fmt::Write;

use rowan::NodeOrToken;

use crate::{SyntaxElement, SyntaxNode};

/// One line per element, two-space indent per depth, no trailing newline:
/// `KIND@start..end` for nodes and `KIND@start..end "text"` for tokens.
pub fn debug_tree(root: &SyntaxNode) -> String {
    let mut out = String::new();
    write_element(&mut out, &NodeOrToken::Node(root.clone()), 0);
    out
}

fn write_element(out: &mut String, element: &SyntaxElement, depth: usize) {
    for _ in 0..depth {
        out.push_str("  ");
    }
    let range = element.text_range();
    let _ = write!(
        out,
        "{}@{}..{}",
        element.kind().name(),
        u32::from(range.start()),
        u32::from(range.end())
    );
    match element {
        NodeOrToken::Token(token) => {
            let _ = write!(out, " {:?}", truncate(token.text()));
        }
        NodeOrToken::Node(node) => {
            for child in node.children_with_tokens() {
                out.push('\n');
                write_element(out, &child, depth + 1);
            }
        }
    }
}

fn truncate(text: &str) -> std::borrow::Cow<'_, str> {
    if text.len() < 25 {
        return text.into();
    }
    for index in 21..25 {
        if text.is_char_boundary(index) {
            return format!("{} ...", &text[..index]).into();
        }
    }
    text.into()
}
