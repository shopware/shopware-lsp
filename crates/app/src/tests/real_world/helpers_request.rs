//! Port of Go `real_world_helpers_request_test.go`.

#[allow(unused_imports)]
use super::indexing_main::record_failure as soft;

/// Records a soft failure (Go `t.Errorf` in the soft oracle run) and
/// continues with the zero value.
#[allow(dead_code)]
fn soft_default<T: Default>(message: String) -> T {
    soft(message);
    T::default()
}

macro_rules! soft_assert {
    ($cond:expr, $($arg:tt)+) => {
        if !$cond {
            soft(format!($($arg)+));
        }
    };
}

use std::sync::Arc;

use swlsp_lsp::{
    CodeActionRequest, CompletionProvider, CompletionRequest, Context, DefinitionRequest,
    HoverRequest, ReferenceRequest, RenameRequest, SemanticTokensProvider, SemanticTokensRequest,
    SignatureHelpRequest, SyntaxContext, TextDocument,
};
use swlsp_lsp_protocol as protocol;
use swlsp_php::PhpIndex;
use swlsp_syntax::{SyntaxNode, SyntaxNodeExt, TextSize};
use swlsp_twig::TwigIndexer;

/// A new open-document snapshot (Go `lsp.NewTextDocument`).
pub fn new_document(uri: impl Into<String>, source: &str, version: i32) -> Arc<TextDocument> {
    Arc::new(TextDocument::new(uri, source, version))
}

pub fn file_uri(path: &str) -> String {
    swlsp_uriutil::file_uri(path)
}

pub fn position(document: &TextDocument, offset: u32) -> protocol::Position {
    let (line, character) = document.line_index().position_utf16(offset);
    protocol::Position { line, character }
}

pub fn node_at(document: &TextDocument, offset: u32) -> Option<SyntaxNode> {
    document.root()?.node_at(TextSize::from(offset))
}

/// The node at `offset`, falling back to `offset - 1` (Go helpers' EOF rule).
pub fn node_at_or_before(document: &TextDocument, offset: u32) -> Option<SyntaxNode> {
    node_at(document, offset).or_else(|| {
        if offset > 0 {
            node_at(document, offset - 1)
        } else {
            None
        }
    })
}

pub fn syntax(
    document: &Arc<TextDocument>,
    node: Option<SyntaxNode>,
    with_token_at: Option<u32>,
) -> SyntaxContext {
    let root = document.root();
    let token = with_token_at.and_then(|offset| {
        root.as_ref()
            .and_then(|root| root.token_at(TextSize::from(offset)))
    });
    SyntaxContext {
        language: document.language,
        document: Some(document.clone()),
        root,
        token,
        node,
    }
}

fn completion_at_needle(
    document: &Arc<TextDocument>,
    source: &str,
    needle: &str,
) -> Option<CompletionRequest> {
    let found = source.rfind(needle)?;
    let offset = (found + needle.len()) as u32;
    let mut params = protocol::CompletionParams::default();
    params.text_document.uri = document.uri.clone();
    params.position = position(document, offset);
    let node = node_at(document, offset.saturating_sub(1));
    Some(CompletionRequest {
        params,
        syntax: syntax(document, node, None),
    })
}

pub fn real_world_php_attribute_completions(
    php: &PhpIndex,
    source: &str,
    needle: &str,
) -> Vec<protocol::CompletionItem> {
    let document = new_document("file:///real-world-php-attribute.php", source, 1);
    let Some(request) = completion_at_needle(&document, source, needle) else {
        return Vec::new();
    };
    swlsp_lsp_completion::PhpAttributeCompletionProvider::new(Some(php.clone()))
        .get_completions(&Context::background(), &request)
}

pub fn real_world_twig_completions(
    root: &str,
    twig: &Arc<TwigIndexer>,
    php: &PhpIndex,
    source: &str,
    needle: &str,
) -> Vec<protocol::CompletionItem> {
    let document = new_document("file:///real-world-loop.html.twig", source, 1);
    let Some(request) = completion_at_needle(&document, source, needle) else {
        return Vec::new();
    };
    swlsp_lsp_completion::TwigCompletionProvider::new(
        root,
        Some(twig.clone()),
        None,
        Some(php.clone()),
    )
    .get_completions(&Context::background(), &request)
}

pub fn real_world_completion_labels(items: &[protocol::CompletionItem]) -> Vec<String> {
    let mut labels: Vec<String> = items.iter().map(|item| item.label.clone()).collect();
    labels.sort();
    labels
}

pub fn real_world_completion_request(
    document: &Arc<TextDocument>,
    offset: u32,
) -> CompletionRequest {
    let mut params = protocol::CompletionParams::default();
    params.text_document.uri = document.uri.clone();
    params.position = position(document, offset);
    CompletionRequest {
        params,
        syntax: syntax(document, node_at_or_before(document, offset), None),
    }
}

pub fn real_world_signature_request(
    document: &Arc<TextDocument>,
    offset: u32,
) -> SignatureHelpRequest {
    let mut params = protocol::SignatureHelpParams::default();
    params.text_document.uri = document.uri.clone();
    params.position = position(document, offset);
    let mut context = syntax(document, node_at_or_before(document, offset), None);
    // Go leaves Language unset for signature requests.
    context.language = None;
    SignatureHelpRequest {
        params,
        syntax: context,
    }
}

pub fn real_world_completion_by_label(
    items: &[protocol::CompletionItem],
    label: &str,
) -> protocol::CompletionItem {
    items
        .iter()
        .find(|item| item.label == label)
        .cloned()
        .unwrap_or_else(|| {
            soft_default(format!(
                "completion {label:?} not found in {:?}",
                real_world_completion_labels(items)
            ))
        })
}

pub fn real_world_twig_ux_semantic_token_snapshot(ctx: &Context) -> Vec<String> {
    let document = new_document(
        "file:///real-world-ux-annotations.html.twig",
        "{# @prop title string Visible title #}\n{# @block content Main content #}",
        1,
    );
    let tokens = swlsp_lsp_semantic::TwigUxToolkitProvider::new()
        .get_semantic_tokens(
            ctx,
            &SemanticTokensRequest {
                params: protocol::SemanticTokensParams::default(),
                document: document.clone(),
            },
        )
        .expect("semantic tokens");
    assert_eq!(5, tokens.len(), "{tokens:?}");
    let source = document.source();
    let result: Vec<String> = tokens
        .iter()
        .map(|token| {
            let start: usize = token.range.start().into();
            let end: usize = token.range.end().into();
            format!("{}:{}", token.token_type, &source[start..end])
        })
        .collect();
    assert_eq!(
        vec![
            format!("{}:@prop", protocol::SEMANTIC_TOKEN_KEYWORD),
            format!("{}:title", protocol::SEMANTIC_TOKEN_PROPERTY),
            format!("{}:string", protocol::SEMANTIC_TOKEN_TYPE),
            format!("{}:@block", protocol::SEMANTIC_TOKEN_KEYWORD),
            format!("{}:content", protocol::SEMANTIC_TOKEN_PROPERTY),
        ],
        result
    );
    result
}

pub fn real_world_definition_request(
    document: &Arc<TextDocument>,
    node: Option<SyntaxNode>,
    offset: u32,
) -> DefinitionRequest {
    let mut params = protocol::DefinitionParams::default();
    params.text_document.uri = document.uri.clone();
    params.position = position(document, offset);
    DefinitionRequest {
        params,
        syntax: syntax(document, node, None),
    }
}

/// [`real_world_definition_request`] with the node at `offset`.
pub fn real_world_definition_request_at(
    document: &Arc<TextDocument>,
    offset: u32,
) -> DefinitionRequest {
    real_world_definition_request(document, node_at(document, offset), offset)
}

pub fn real_world_reference_request(
    document: &Arc<TextDocument>,
    offset: u32,
    include_declaration: bool,
) -> ReferenceRequest {
    let mut params = protocol::ReferenceParams::default();
    params.text_document.uri = document.uri.clone();
    params.position = position(document, offset);
    params.context.include_declaration = include_declaration;
    ReferenceRequest {
        params,
        syntax: syntax(document, node_at(document, offset), Some(offset)),
    }
}

pub fn real_world_rename_request(
    document: &Arc<TextDocument>,
    offset: u32,
    new_name: &str,
) -> RenameRequest {
    let mut params = protocol::RenameParams::default();
    params.text_document.uri = document.uri.clone();
    params.position = position(document, offset);
    params.new_name = new_name.to_owned();
    RenameRequest {
        params,
        syntax: syntax(document, node_at(document, offset), Some(offset)),
    }
}

pub fn real_world_hover_request(document: &Arc<TextDocument>, offset: u32) -> HoverRequest {
    let mut params = protocol::HoverParams::default();
    params.text_document.uri = document.uri.clone();
    params.position = position(document, offset);
    HoverRequest {
        params,
        syntax: syntax(document, node_at(document, offset), Some(offset)),
    }
}

pub fn require_route_definition_path(locations: &[protocol::Location], path: &str) {
    require_location_uri(locations, path);
}

pub fn require_location_uri(locations: &[protocol::Location], path: &str) {
    let uri = file_uri(path);
    soft_assert!(
        locations.iter().any(|location| location.uri == uri),
        "location {uri:?} not found in {locations:#?}"
    );
}

pub fn real_world_code_action_request(
    document: &Arc<TextDocument>,
    needle: &str,
) -> CodeActionRequest {
    assert!(document.root().is_some(), "document has no syntax tree");
    let offset = document
        .source()
        .find(needle)
        .unwrap_or_else(|| panic!("{needle:?} not in document")) as u32;
    let start = position(document, offset);
    let mut params = protocol::CodeActionParams::default();
    params.text_document.uri = document.uri.clone();
    params.range = protocol::Range {
        start,
        end: protocol::Position {
            line: start.line,
            character: start.character + needle.len() as u32,
        },
    };
    CodeActionRequest {
        params,
        syntax: syntax(document, node_at(document, offset), None),
    }
}
