//! Native import declarations (port of Go `imports.go`).

use swlsp_syntax::{SyntaxNode, SyntaxToken, TextRange};

use super::child_tokens;
use crate::equal_fold;
use crate::syntax::*;

/// One imported item with exact source ranges and normalized text for the
/// central import resolver. Trivia never participates in import names.
#[derive(Clone, Debug, PartialEq, Eq)]
pub struct UseItem {
    pub text: String,
    pub range: TextRange,
    pub name_range: TextRange,
}

#[derive(Clone, Debug, Default, PartialEq, Eq)]
pub struct UseList {
    pub items: Vec<UseItem>,
    pub commas: Vec<TextRange>,
}

/// Parses a complete native import declaration; `None` for incomplete or
/// unsupported forms.
pub fn use_items(node: &SyntaxNode) -> Option<UseList> {
    if node.kind() != PHP_USE_DECLARATION {
        return None;
    }
    let all: Vec<SyntaxToken> = child_tokens(node)
        .filter(|token| !token.kind().is_trivia())
        .collect();
    if all.len() < 3
        || !equal_fold(all[0].text(), "use")
        || all[all.len() - 1].kind() != TK_SEMICOLON
    {
        return None;
    }
    let mut tokens = &all[1..all.len() - 1];
    let mut default_kind = "";
    if let Some(kind) = tokens.first().and_then(|token| import_kind(token.text())) {
        default_kind = kind;
        tokens = &tokens[1..];
    }
    let mut prefix = String::new();
    if let Some(brace) = tokens
        .iter()
        .position(|token| token.kind() == TK_OPEN_BRACE)
    {
        if tokens.len() < brace + 3 || tokens[tokens.len() - 1].kind() != TK_CLOSE_BRACE {
            return None;
        }
        prefix = import_name(&tokens[..brace], true)?;
        tokens = &tokens[brace + 1..tokens.len() - 1];
    }
    let mut result = UseList::default();
    let mut start = 0;
    for index in 0..=tokens.len() {
        if index < tokens.len() && tokens[index].kind() != TK_COMMA {
            continue;
        }
        if index == start {
            if index == tokens.len() && !prefix.is_empty() && !result.items.is_empty() {
                break;
            }
            return None;
        }
        result
            .items
            .push(use_item(&tokens[start..index], &prefix, default_kind)?);
        if index < tokens.len() {
            result.commas.push(tokens[index].text_range());
        }
        start = index + 1;
    }
    (!result.items.is_empty()).then_some(result)
}

fn use_item(mut tokens: &[SyntaxToken], prefix: &str, mut kind: &str) -> Option<UseItem> {
    let range = TextRange::new(
        tokens[0].text_range().start(),
        tokens[tokens.len() - 1].text_range().end(),
    );
    if let Some(item_kind) = import_kind(tokens[0].text()) {
        if prefix.is_empty() || !kind.is_empty() {
            return None;
        }
        kind = item_kind;
        tokens = &tokens[1..];
    }
    if tokens.is_empty() {
        return None;
    }
    let mut name_tokens = tokens;
    let mut alias = "";
    if let Some(index) = tokens
        .iter()
        .position(|token| equal_fold(token.text(), "as"))
    {
        if index + 2 != tokens.len() || !import_identifier(&tokens[index + 1]) {
            return None;
        }
        alias = tokens[index + 1].text();
        name_tokens = &tokens[..index];
    }
    let name = import_name(name_tokens, false)?;
    if !prefix.is_empty() && name.starts_with('\\') {
        return None;
    }
    let mut text = String::from("use ");
    if !kind.is_empty() {
        text.push_str(kind);
        text.push(' ');
    }
    text.push_str(prefix);
    text.push_str(&name);
    if !alias.is_empty() {
        text.push_str(" as ");
        text.push_str(alias);
    }
    text.push(';');
    let name_range = TextRange::new(
        name_tokens[0].text_range().start(),
        name_tokens[name_tokens.len() - 1].text_range().end(),
    );
    Some(UseItem {
        text,
        range,
        name_range,
    })
}

fn import_kind(text: &str) -> Option<&'static str> {
    if equal_fold(text, "function") {
        Some("function")
    } else if equal_fold(text, "const") {
        Some("const")
    } else {
        None
    }
}

fn import_identifier(token: &SyntaxToken) -> bool {
    matches!(token.kind(), TK_IDENTIFIER | TK_KEYWORD)
        && import_kind(token.text()).is_none()
        && !equal_fold(token.text(), "as")
}

fn import_name(tokens: &[SyntaxToken], trailing_slash: bool) -> Option<String> {
    if tokens.is_empty() {
        return None;
    }
    let mut name = String::new();
    let mut want_name = true;
    for (index, token) in tokens.iter().enumerate() {
        if token.kind() == TK_BACKSLASH {
            if want_name && index != 0 {
                return None;
            }
            want_name = true;
        } else {
            if !want_name || !import_identifier(token) {
                return None;
            }
            want_name = false;
        }
        name.push_str(token.text());
    }
    (want_name == trailing_slash).then_some(name)
}
