//! Request builders shared by provider tests (Go `request_test.go`).
#![allow(dead_code)]

use std::sync::Arc;

use swlsp_lsp::{CompletionRequest, SyntaxContext, TextDocument};
use swlsp_lsp_protocol as protocol;
use swlsp_syntax::{SyntaxNode, SyntaxNodeExt, TextSize};

/// Go `completionRequest`: a request for `node` at `offset`. `node` defaults
/// to the smallest node containing `offset`.
pub fn completion_request(
    document: Arc<TextDocument>,
    node: Option<SyntaxNode>,
    offset: u32,
) -> CompletionRequest {
    let root = document.root();
    let node = node.or_else(|| {
        root.as_ref()
            .and_then(|root| root.node_at(TextSize::from(offset)))
    });
    let (line, character) = document.line_index().position_utf16(offset);
    let mut params = protocol::CompletionParams::default();
    params.text_document.uri = document.uri.clone();
    params.position = protocol::Position { line, character };
    CompletionRequest {
        params,
        syntax: SyntaxContext {
            language: document.language,
            document: Some(document),
            root,
            token: None,
            node,
        },
    }
}

/// A request at `offset` resolved like the server does
/// ([`SyntaxContext::new`]: token moved left off trivia).
pub fn request_at(document: Arc<TextDocument>, offset: u32) -> CompletionRequest {
    let (line, character) = document.line_index().position_utf16(offset);
    let mut params = protocol::CompletionParams::default();
    params.text_document.uri = document.uri.clone();
    params.position = protocol::Position { line, character };
    CompletionRequest {
        params,
        syntax: SyntaxContext::new(document, line, character),
    }
}

/// A request for a document opened at `uri` with `source`, at the byte
/// offset of the `|` cursor marker (which is removed).
pub fn request_with_cursor(uri: &str, source_with_cursor: &str) -> CompletionRequest {
    let offset = source_with_cursor
        .find('|')
        .expect("source has a | cursor marker");
    let source = format!(
        "{}{}",
        &source_with_cursor[..offset],
        &source_with_cursor[offset + 1..]
    );
    request_at(Arc::new(TextDocument::new(uri, &source, 1)), offset as u32)
}
