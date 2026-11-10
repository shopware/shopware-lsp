use super::*;
use std::sync::Arc;
use swlsp_lsp::{
    Context, SemanticToken, SemanticTokensProvider, SemanticTokensRequest, TextDocument,
};
use swlsp_lsp_protocol as protocol;
use swlsp_lsp_protocol::{
    SEMANTIC_TOKEN_KEYWORD as KEYWORD, SEMANTIC_TOKEN_PROPERTY as PROPERTY,
    SEMANTIC_TOKEN_TYPE as TYPE, SEMANTIC_TOKEN_VARIABLE as VARIABLE,
};

fn request(uri: &str, source: &str, version: i32) -> SemanticTokensRequest {
    SemanticTokensRequest {
        params: protocol::SemanticTokensParams {
            text_document: protocol::TextDocumentIdentifier {
                uri: uri.to_owned(),
            },
            ..Default::default()
        },
        document: Arc::new(TextDocument::new(uri, source, version)),
    }
}

fn tokens(
    provider: &dyn SemanticTokensProvider,
    uri: &str,
    source: &str,
    version: i32,
) -> Vec<SemanticToken> {
    provider
        .get_semantic_tokens(&Context::background(), &request(uri, source, version))
        .expect("semantic tokens")
}

fn text<'a>(source: &'a str, token: &SemanticToken) -> &'a str {
    &source[token.range]
}

fn assert_tokens(source: &str, actual: &[SemanticToken], expected: &[(&str, u32)]) {
    let actual: Vec<(&str, u32)> = actual
        .iter()
        .map(|token| (text(source, token), token.token_type))
        .collect();
    assert_eq!(actual, expected);
}

// --- Twig UX Toolkit --------------------------------------------------------

#[test]
fn twig_ux_toolkit_semantic_tokens() {
    let source = r#"{# Regular comment #}
{# @var other \App\Entity\Other #}
{#
 @prop items App\Entity\Item[] List of items
 @prop value string|int|null The value
 @block content The item content
#}
{{ '@prop ignored string Not a comment' }}
{# @prop incomplete string #}
"#;
    let actual = tokens(
        &TwigUxToolkitProvider::new(),
        "file:///templates/components/Card.html.twig",
        source,
        1,
    );
    assert_tokens(
        source,
        &actual,
        &[
            ("@var", KEYWORD),
            ("other", VARIABLE),
            (r"\App\Entity\Other", TYPE),
            ("@prop", KEYWORD),
            ("items", PROPERTY),
            (r"App\Entity\Item[]", TYPE),
            ("@prop", KEYWORD),
            ("value", PROPERTY),
            ("string|int|null", TYPE),
            ("@block", KEYWORD),
            ("content", PROPERTY),
        ],
    );
}

#[test]
fn twig_var_semantic_tokens_support_both_orders_and_multiple_declarations() {
    let source = r#"{#
 @var items \App\Entity\User[]|\App\Entity\Admin[]
 @var \App\Entity\Category $category
 @var ShortName ambiguous
#}
{{ '@var ignored \App\Ignored' }}"#;
    let actual = tokens(
        &TwigUxToolkitProvider::new(),
        "file:///templates/catalog.html.twig",
        source,
        1,
    );
    assert_tokens(
        source,
        &actual,
        &[
            ("@var", KEYWORD),
            ("items", VARIABLE),
            (r"\App\Entity\User[]|\App\Entity\Admin[]", TYPE),
            ("@var", KEYWORD),
            (r"\App\Entity\Category", TYPE),
            ("$category", VARIABLE),
            ("@var", KEYWORD),
            ("ShortName", VARIABLE),
            ("ambiguous", TYPE),
        ],
    );
}

#[test]
fn twig_ux_toolkit_semantic_tokens_ignore_other_documents() {
    let actual = tokens(
        &TwigUxToolkitProvider::new(),
        "file:///templates/component.php",
        "<?php // @prop value string Description",
        1,
    );
    assert!(actual.is_empty());
}
