#![allow(dead_code)]

use std::path::Path;
use std::sync::Arc;

use swlsp_indexer::{Indexer, ParsedFile};
use swlsp_lsp::{Context, HoverProvider, HoverRequest, SyntaxContext, TextDocument};
use swlsp_lsp_protocol as protocol;
use swlsp_php::PhpIndex;
use swlsp_syntax::{SyntaxNodeExt, TextSize};

pub fn p(path: &Path) -> String {
    path.to_string_lossy().into_owned()
}

pub fn uri(path: &Path) -> String {
    swlsp_uriutil::file_uri(&p(path))
}

/// A request like the Go tests build by hand: the node at `offset`, no
/// token, and the hover position at `offset`.
pub fn request_at(document: TextDocument, offset: usize) -> HoverRequest {
    let document = Arc::new(document);
    let root = document.root();
    let node = root
        .as_ref()
        .and_then(|root| root.node_at(TextSize::from(offset as u32)));
    let (line, character) = document.line_index().position_utf16(offset as u32);
    let mut params = protocol::HoverParams::default();
    params.text_document.uri = document.uri.clone();
    params.position = protocol::Position { line, character };
    HoverRequest {
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

/// A request resolved through the production cursor lookup (token + node).
pub fn request_at_position(document: TextDocument, offset: usize) -> HoverRequest {
    let document = Arc::new(document);
    let (line, character) = document.line_index().position_utf16(offset as u32);
    let mut params = protocol::HoverParams::default();
    params.text_document.uri = document.uri.clone();
    params.position = protocol::Position { line, character };
    HoverRequest {
        params,
        syntax: SyntaxContext::new(document, line, character),
    }
}

pub fn hover(provider: &dyn HoverProvider, request: &HoverRequest) -> Option<protocol::Hover> {
    provider
        .get_hover(&Context::background(), request)
        .expect("hover")
}

pub fn php_index(dir: &Path, files: &[(&str, &str)]) -> PhpIndex {
    let php = PhpIndex::open(dir, None).unwrap();
    for (path, source) in files {
        php.index(&ParsedFile::new(*path, source.as_bytes().to_vec()))
            .unwrap();
    }
    php
}

pub fn index_file(indexer: &dyn Indexer, path: &str, source: &str) {
    indexer
        .index(&ParsedFile::new(path, source.as_bytes().to_vec()))
        .unwrap();
}

pub fn write(path: &Path, content: &str) {
    std::fs::create_dir_all(path.parent().unwrap()).unwrap();
    std::fs::write(path, content).unwrap();
}

pub fn value(hover: &protocol::Hover) -> &str {
    &hover.contents.value
}
