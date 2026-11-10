use super::*;
use std::path::Path;
use std::sync::Arc;
use swlsp_indexer::{Indexer, ParsedFile};
use swlsp_lsp::{Context, ReferenceRequest, ReferencesProvider, SyntaxContext, TextDocument};
use swlsp_lsp_protocol as protocol;
use swlsp_syntax::{SyntaxNode, SyntaxNodeExt, TextSize};
use swlsp_uriutil::file_uri;

pub(crate) fn p(path: &Path) -> String {
    path.to_string_lossy().into_owned()
}

pub(crate) fn index(indexer: &dyn Indexer, path: &Path, source: &str) {
    indexer
        .index(&ParsedFile::new(p(path), source.as_bytes().to_vec()))
        .unwrap();
}

pub(crate) fn document(path: &Path, source: &str, version: i32) -> Arc<TextDocument> {
    Arc::new(TextDocument::new(file_uri(&p(path)), source, version))
}

pub(crate) fn node_at(document: &TextDocument, offset: usize) -> Option<SyntaxNode> {
    document.root()?.node_at(TextSize::from(offset as u32))
}

/// A request at byte `offset` with the deepest node at that offset.
pub(crate) fn request(
    document: &Arc<TextDocument>,
    offset: usize,
    include_declaration: bool,
) -> ReferenceRequest {
    request_with_node(
        document,
        offset,
        node_at(document, offset),
        include_declaration,
    )
}

pub(crate) fn request_with_node(
    document: &Arc<TextDocument>,
    offset: usize,
    node: Option<SyntaxNode>,
    include_declaration: bool,
) -> ReferenceRequest {
    let (line, character) = document.line_index().position_utf16(offset as u32);
    let mut params = protocol::ReferenceParams::default();
    params.text_document.uri = document.uri.clone();
    params.position = protocol::Position { line, character };
    params.context.include_declaration = include_declaration;
    ReferenceRequest {
        params,
        syntax: SyntaxContext {
            document: Some(document.clone()),
            language: document.language,
            root: document.root(),
            token: None,
            node,
        },
    }
}

pub(crate) fn references(
    provider: &dyn ReferencesProvider,
    request: &ReferenceRequest,
) -> Vec<protocol::Location> {
    provider
        .get_references(&Context::background(), request)
        .unwrap()
}

pub(crate) fn uris(locations: &[protocol::Location]) -> Vec<String> {
    locations
        .iter()
        .map(|location| location.uri.clone())
        .collect()
}

/// Asserts that the location URIs equal `expected`, ignoring order.
pub(crate) fn assert_uris_match(locations: &[protocol::Location], expected: &[String]) {
    let mut actual = uris(locations);
    let mut expected = expected.to_vec();
    actual.sort();
    expected.sort();
    assert_eq!(actual, expected);
}

#[test]
fn style_class_references_connect_scss_and_twig() {
    let root = tempfile::tempdir().unwrap();
    let cache = tempfile::tempdir().unwrap();
    let style_path = root.path().join("component.scss");
    let first = root.path().join("first.html.twig");
    let second = root.path().join("second.html.twig");
    let first_source = r#"<div class="sw-card"></div>"#;
    let index = Arc::new(swlsp_style::Index::new(cache.path(), None).unwrap());
    for (path, source) in [
        (&style_path, ".sw-card { color: red; }"),
        (&first, first_source),
        (&second, r#"<section class="layout sw-card"></section>"#),
    ] {
        self::index(index.as_ref(), path, source);
    }
    let document = document(&first, first_source, 1);
    let offset = first_source.find("sw-card").unwrap() + 2;
    let provider = StyleClassReferenceProvider::new(Some(index));
    let locations = references(&provider, &request(&document, offset, true));
    assert_uris_match(
        &locations,
        &[&style_path, &first, &second].map(|path| file_uri(&p(path))),
    );
    let locations = references(&provider, &request(&document, offset, false));
    assert_eq!(locations.len(), 2);
    assert_ne!(locations[0].uri, file_uri(&p(&style_path)));
}
