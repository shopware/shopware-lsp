//! Helpers shared by the PHP and service-configuration definition providers.

use swlsp_language::LanguageId;
use swlsp_lsp::SyntaxContext;
use swlsp_lsp_protocol as protocol;
use swlsp_php::{PhpContext, PhpIndex};

/// The request's PHP semantic context, computed per request because
/// `PhpContext` is not `Send` and cannot travel in [`swlsp_lsp::Context`]: the memoized document analysis, falling back
/// to a fresh analysis of the open document. `None` outside PHP documents.
pub fn php_context(php: Option<&PhpIndex>, request: &SyntaxContext) -> Option<PhpContext> {
    if request.language != Some(LanguageId::Php) {
        return None;
    }
    let php = php?;
    let document = request.document.as_ref()?;
    if let Ok(Some(state)) = swlsp_lsp_phpanalysis::for_document(php, document) {
        return Some(php.analyzed_snapshot_context(
            request.node.as_ref(),
            state.document.clone(),
            Some(state.snapshot.clone()),
        ));
    }
    let root = request.root.as_ref()?;
    let path = swlsp_uriutil::path(&document.uri).unwrap_or_default();
    Some(php.document_context(
        &path,
        i64::from(document.version),
        request.node.as_ref(),
        root,
        document.source(),
    ))
}

/// A zero-width location at the start of a one-based line without clamping
/// (route declarations and tagged XML services). Line 0 saturates at the
/// first line because protocol lines are unsigned.
pub fn unclamped_line_location(path: &str, line: u32) -> protocol::Location {
    let position = protocol::Position {
        line: line.saturating_sub(1),
        character: 0,
    };
    protocol::Location {
        uri: swlsp_uriutil::file_uri(path),
        range: protocol::Range {
            start: position,
            end: position,
        },
    }
}

/// A key identifying a location by URI and range, for deduplication.
pub fn location_key(location: &protocol::Location) -> (String, u32, u32, u32, u32) {
    (
        location.uri.clone(),
        location.range.start.line,
        location.range.start.character,
        location.range.end.line,
        location.range.end.character,
    )
}

#[cfg(any(test, feature = "testing"))]
pub mod test_support {
    use std::path::Path;
    use std::sync::Arc;

    use swlsp_indexer::{Indexer, ParsedFile};
    use swlsp_lsp::{
        Context, DefinitionRequest, GotoDefinitionProvider, SyntaxContext, TextDocument,
    };
    use swlsp_lsp_protocol as protocol;
    use swlsp_php::PhpIndex;
    use swlsp_syntax::{SyntaxNode, SyntaxNodeExt, TextSize};

    pub fn p(path: &Path) -> String {
        path.to_string_lossy().into_owned()
    }

    pub fn write(path: &Path, source: &str) {
        std::fs::create_dir_all(path.parent().unwrap()).unwrap();
        std::fs::write(path, source).unwrap();
    }

    pub fn php_index(dir: &Path) -> PhpIndex {
        PhpIndex::open(dir, None).unwrap()
    }

    pub fn index(indexer: &dyn Indexer, path: &str, source: &str) {
        indexer
            .index(&ParsedFile::new(path, source.as_bytes().to_vec()))
            .unwrap();
    }

    pub fn document(uri: &str, source: &str) -> Arc<TextDocument> {
        Arc::new(TextDocument::new(uri, source, 1))
    }

    /// The deepest node at `offset`.
    pub fn node_at(document: &TextDocument, offset: usize) -> SyntaxNode {
        document
            .root()
            .unwrap()
            .node_at(TextSize::from(offset as u32))
            .unwrap()
    }

    /// A request with the cursor at `offset` and an explicit node.
    pub fn request_with_node(
        document: &Arc<TextDocument>,
        node: Option<SyntaxNode>,
        offset: usize,
    ) -> DefinitionRequest {
        let (line, character) = document.line_index().position_utf16(offset as u32);
        let mut params = protocol::DefinitionParams::default();
        params.text_document.uri = document.uri.clone();
        params.position = protocol::Position { line, character };
        DefinitionRequest {
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

    /// The request at `offset` with the node from [`node_at`].
    pub fn request_at(document: &Arc<TextDocument>, offset: usize) -> DefinitionRequest {
        request_with_node(document, Some(node_at(document, offset)), offset)
    }

    pub fn definitions(
        provider: &dyn GotoDefinitionProvider,
        request: &DefinitionRequest,
    ) -> Vec<protocol::Location> {
        provider.get_definition(&Context::background(), request)
    }

    /// The source text a location covers.
    pub fn location_text<'a>(source: &'a str, location: &protocol::Location) -> &'a str {
        let lines = swlsp_syntax::LineIndex::new(source.to_owned());
        let start = lines.offset_utf16(location.range.start.line, location.range.start.character);
        let end = lines.offset_utf16(location.range.end.line, location.range.end.character);
        &source[start as usize..end as usize]
    }

    pub fn index_of(source: &str, needle: &str) -> usize {
        source
            .find(needle)
            .unwrap_or_else(|| panic!("{needle} not found"))
    }

    pub fn last_index_of(source: &str, needle: &str) -> usize {
        source
            .rfind(needle)
            .unwrap_or_else(|| panic!("{needle} not found"))
    }
}
