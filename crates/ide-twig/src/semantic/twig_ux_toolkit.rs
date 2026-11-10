//! Symfony UX Toolkit `@prop`/`@block` and Twig `@var` comment annotations.

use std::collections::HashSet;
use std::sync::LazyLock;

use regex::Regex;
use swlsp_lsp::{
    Context, ProviderResult, SemanticToken, SemanticTokensProvider, SemanticTokensRequest,
};
use swlsp_lsp_protocol::{
    SEMANTIC_TOKEN_KEYWORD, SEMANTIC_TOKEN_PROPERTY, SEMANTIC_TOKEN_TYPE, SEMANTIC_TOKEN_VARIABLE,
};
use swlsp_parser_twig::syntax::TWIG_COMMENT;
use swlsp_syntax::{SyntaxNode, TextRange, TextSize};

use crate::semantic::{has_suffix_fold, sort_tokens, token};

// `\S` and `\w` are meant as ASCII classes; spell them out so Unicode classes
// do not widen the matches.
const NON_SPACE: &str = r"[^\t\n\x0C\r ]";
const TYPE_CHAR: &str = r"[0-9A-Za-z_\\\[\]]";

static PROP_PATTERN: LazyLock<Regex> = LazyLock::new(|| {
    Regex::new(&format!(
        r"(@prop)[ \t]+([A-Za-z_][A-Za-z0-9_]*)[ \t]+({NON_SPACE}+)[ \t]+[^\r\n#]+"
    ))
    .expect("valid @prop pattern")
});
static BLOCK_PATTERN: LazyLock<Regex> = LazyLock::new(|| {
    Regex::new(r"(@block)[ \t]+([A-Za-z_][A-Za-z0-9_]*)[ \t]+[^\r\n#]+")
        .expect("valid @block pattern")
});
static VAR_FIRST_PATTERN: LazyLock<Regex> = LazyLock::new(|| {
    Regex::new(&format!(
        r"(@var)[ \t]+(\$?[A-Za-z_][A-Za-z0-9_]*)[ \t]+({TYPE_CHAR}+(?:\|{TYPE_CHAR}+)*)"
    ))
    .expect("valid @var pattern")
});
static TYPE_FIRST_VAR_PATTERN: LazyLock<Regex> = LazyLock::new(|| {
    Regex::new(&format!(
        r"(@var)[ \t]+({TYPE_CHAR}+(?:\|{TYPE_CHAR}+)*)[ \t]+(\$?[A-Za-z_][A-Za-z0-9_]*)"
    ))
    .expect("valid type-first @var pattern")
});

/// Colors Symfony UX Toolkit `@prop` and `@block` comments, plus both
/// supported Twig `@var` annotation orders.
#[derive(Debug, Default)]
pub struct TwigUxToolkitProvider;

impl TwigUxToolkitProvider {
    pub fn new() -> Self {
        Self
    }
}

impl SemanticTokensProvider for TwigUxToolkitProvider {
    fn get_semantic_tokens(
        &self,
        _ctx: &Context,
        request: &SemanticTokensRequest,
    ) -> ProviderResult<Vec<SemanticToken>> {
        let document = &request.document;
        let Some(root) = document.root() else {
            return Ok(Vec::new());
        };
        if !has_suffix_fold(&document.uri, ".twig") {
            return Ok(Vec::new());
        }
        let mut result = Vec::new();
        for comment in root
            .descendants()
            .filter(|node| node.kind() == TWIG_COMMENT)
        {
            append_var_annotation_tokens(&mut result, &comment);
            append_annotation_tokens(
                &mut result,
                &comment,
                &PROP_PATTERN,
                &[
                    SEMANTIC_TOKEN_KEYWORD,
                    SEMANTIC_TOKEN_PROPERTY,
                    SEMANTIC_TOKEN_TYPE,
                ],
            );
            append_annotation_tokens(
                &mut result,
                &comment,
                &BLOCK_PATTERN,
                &[SEMANTIC_TOKEN_KEYWORD, SEMANTIC_TOKEN_PROPERTY],
            );
        }
        sort_tokens(&mut result);
        Ok(result)
    }
}

fn append_var_annotation_tokens(result: &mut Vec<SemanticToken>, comment: &SyntaxNode) {
    let text = comment.text().to_string();
    if !text.contains("@var") {
        return;
    }
    let base = u32::from(comment.text_range().start());
    let mut handled = HashSet::new();
    for captures in VAR_FIRST_PATTERN.captures_iter(&text) {
        let whole = captures.get(0).expect("whole match");
        if !var_annotation_boundary(&text, whole.end()) {
            continue;
        }
        handled.insert(captures.get(1).map_or(usize::MAX, |group| group.start()));
        append_match_tokens(
            result,
            base,
            &captures,
            &[
                SEMANTIC_TOKEN_KEYWORD,
                SEMANTIC_TOKEN_VARIABLE,
                SEMANTIC_TOKEN_TYPE,
            ],
        );
    }
    for captures in TYPE_FIRST_VAR_PATTERN.captures_iter(&text) {
        let whole = captures.get(0).expect("whole match");
        if !var_annotation_boundary(&text, whole.end()) {
            continue;
        }
        if handled.contains(&captures.get(1).map_or(usize::MAX, |group| group.start())) {
            continue;
        }
        append_match_tokens(
            result,
            base,
            &captures,
            &[
                SEMANTIC_TOKEN_KEYWORD,
                SEMANTIC_TOKEN_TYPE,
                SEMANTIC_TOKEN_VARIABLE,
            ],
        );
    }
}

fn var_annotation_boundary(source: &str, end: usize) -> bool {
    if end == source.len() {
        return true;
    }
    matches!(
        source.as_bytes().get(end),
        Some(b' ' | b'\t' | b'\r' | b'\n' | b'#')
    )
}

fn append_annotation_tokens(
    result: &mut Vec<SemanticToken>,
    comment: &SyntaxNode,
    pattern: &Regex,
    token_types: &[u32],
) {
    let text = comment.text().to_string();
    let base = u32::from(comment.text_range().start());
    for captures in pattern.captures_iter(&text) {
        append_match_tokens(result, base, &captures, token_types);
    }
}

fn append_match_tokens(
    result: &mut Vec<SemanticToken>,
    base: u32,
    captures: &regex::Captures<'_>,
    token_types: &[u32],
) {
    for (group, &token_type) in token_types.iter().enumerate() {
        let Some(matched) = captures.get(group + 1) else {
            continue;
        };
        if matched.end() <= matched.start() {
            continue;
        }
        result.push(token(
            TextRange::new(
                TextSize::from(base + matched.start() as u32),
                TextSize::from(base + matched.end() as u32),
            ),
            token_type,
        ));
    }
}
