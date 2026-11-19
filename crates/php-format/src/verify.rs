//! The check every formatted document has to pass: it holds the tokens of
//! the source, in their order.

use swlsp_parser_php::syntax::*;

use crate::casing::{LONG_CASTS, NATIVE_TYPES};
use swlsp_syntax::rowan::NodeOrToken;
use swlsp_syntax::{Kind, SyntaxNode};

/// Whether `formatted` holds the code and the comments of `source`. The
/// changes the formatter makes on purpose are levelled out first.
pub(crate) fn same_code(source: &SyntaxNode, formatted: &SyntaxNode) -> bool {
    let (code, comments) = tokens(source);
    let (formatted_code, formatted_comments) = tokens(formatted);
    code == formatted_code && comments == formatted_comments
}

fn tokens(root: &SyntaxNode) -> (Vec<String>, Vec<String>) {
    let mut code: Vec<(Kind, String)> = Vec::new();
    let mut comments = Vec::new();
    for element in root.descendants_with_tokens() {
        let NodeOrToken::Token(token) = element else {
            continue;
        };
        let kind = token.kind();
        if kind == TK_WHITESPACE || kind == TK_LINE_BREAK {
            continue;
        }
        // White space after a closing tag is no markup.
        if kind == TK_INLINE_HTML && token.text().trim().is_empty() {
            continue;
        }
        if kind == TK_LINE_COMMENT || kind == TK_BLOCK_COMMENT {
            comments.push(token.text().split_whitespace().collect());
            continue;
        }
        let closes = kind == TK_CLOSE_PAREN || kind == TK_CLOSE_BRACKET || kind == TK_CLOSE_BRACE;
        // A trailing comma comes and goes with the layout of its list.
        if closes && code.last().is_some_and(|(last, _)| *last == TK_COMMA) {
            code.pop();
        }
        code.push((kind, canonical(kind, token.text())));
    }
    // The closing tag at the end of a file is left out.
    if code.last().is_some_and(|(kind, _)| *kind == TK_CLOSE_TAG) {
        code.pop();
    }
    (code.into_iter().map(|(_, text)| text).collect(), comments)
}

/// The text of a token with the case the formatter may change levelled.
fn canonical(kind: Kind, text: &str) -> String {
    if kind == TK_KEYWORD {
        return text.to_ascii_lowercase();
    }
    if kind == TK_IDENTIFIER {
        if let Some((_, short)) = LONG_CASTS
            .iter()
            .find(|(long, _)| text.eq_ignore_ascii_case(long))
        {
            return (*short).to_owned();
        }
        if NATIVE_TYPES
            .iter()
            .any(|native| text.eq_ignore_ascii_case(native))
        {
            return text.to_ascii_lowercase();
        }
    }
    if kind == TK_STRING
        && let Some(heredoc) = crate::heredoc::canonical(text)
    {
        return heredoc;
    }
    text.to_owned()
}

/// Whether `normalized` holds what `source` holds, the keywords and
/// brackets that normalizing adds, removes and moves left aside.
pub(crate) fn same_content(source: &SyntaxNode, normalized: &SyntaxNode) -> bool {
    content(source) == content(normalized)
}

fn content(root: &SyntaxNode) -> Vec<String> {
    let mut found = Vec::new();
    for element in root.descendants_with_tokens() {
        let NodeOrToken::Token(token) = element else {
            continue;
        };
        let kind = token.kind();
        let kept = kind == TK_IDENTIFIER
            || kind == TK_VARIABLE
            || kind == TK_NUMBER
            || kind == TK_STRING
            || kind == TK_OPERATOR
            || kind == TK_LINE_COMMENT
            || kind == TK_BLOCK_COMMENT;
        if kept {
            found.push(token.text().to_owned());
        }
    }
    found.sort_unstable();
    found
}
