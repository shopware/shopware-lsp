//! Helpers shared by the Twig-family definition providers.

use std::collections::HashSet;

use swlsp_lsp_protocol as protocol;

use swlsp_syntax::{LineIndex, TextRange};
use swlsp_uriutil::file_uri;

use crate::common::protocol_range;

/// Go `uriutil.Path` with the error ignored.
pub(crate) fn uri_path(uri: &str) -> String {
    swlsp_uriutil::path(uri).unwrap_or_default()
}

/// Go `componentFileLocation`: reads `path` and converts a byte range.
pub(crate) fn component_file_location(path: &str, range: TextRange) -> Option<protocol::Location> {
    let source = std::fs::read(path).ok()?;
    let lines = LineIndex::new(String::from_utf8_lossy(&source).into_owned());
    Some(protocol::Location {
        uri: file_uri(path),
        range: protocol_range(&lines, range),
    })
}

/// Go `twigVariableLocation`: like [`component_file_location`] but rejects
/// an empty path.
pub(crate) fn twig_variable_location(path: &str, range: TextRange) -> Option<protocol::Location> {
    if path.is_empty() {
        return None;
    }
    component_file_location(path, range)
}

/// Go `uniqueComponentLocations`: first location per URI and range.
pub(crate) fn unique_component_locations(
    locations: Vec<protocol::Location>,
) -> Vec<protocol::Location> {
    let mut seen = HashSet::with_capacity(locations.len());
    locations
        .into_iter()
        .filter(|location| {
            seen.insert((
                location.uri.clone(),
                location.range.start.line,
                location.range.start.character,
                location.range.end.line,
                location.range.end.character,
            ))
        })
        .collect()
}

/// A zero-width location at the start of a zero-based line.
pub(crate) fn zero_line_location(path: &str, line: u32) -> protocol::Location {
    let position = protocol::Position { line, character: 0 };
    protocol::Location {
        uri: file_uri(path),
        range: protocol::Range {
            start: position,
            end: position,
        },
    }
}

pub(crate) use crate::php_common::php_context;

#[cfg(test)]
pub(crate) mod test_support {
    use std::path::Path;
    use std::sync::Arc;

    use swlsp_indexer::{Indexer, ParsedFile};
    use swlsp_lsp::{DefinitionRequest, SyntaxContext, TextDocument};
    use swlsp_lsp_protocol as protocol;
    use swlsp_php::PhpIndex;
    use swlsp_syntax::{SyntaxNodeExt, TextSize};
    use swlsp_twig::TwigIndexer;

    pub(crate) fn p(path: &Path) -> String {
        path.to_string_lossy().into_owned()
    }

    pub(crate) fn write(path: &str, source: &str) {
        std::fs::create_dir_all(Path::new(path).parent().unwrap()).unwrap();
        std::fs::write(path, source).unwrap();
    }

    pub(crate) fn php_index(cache: &Path) -> PhpIndex {
        PhpIndex::open(cache, None).unwrap()
    }

    pub(crate) fn twig_index(cache: &Path) -> Arc<TwigIndexer> {
        Arc::new(TwigIndexer::new(cache, None).unwrap())
    }

    pub(crate) fn index(indexer: &dyn Indexer, path: &str, source: &str) {
        indexer
            .index(&ParsedFile::new(path, source.as_bytes().to_vec()))
            .unwrap();
    }

    pub(crate) fn index_php(php: &PhpIndex, path: &str, source: &str) {
        php.index(&ParsedFile::new(path, source.as_bytes().to_vec()))
            .unwrap();
    }

    /// A request whose node is the node at `node_offset` and whose cursor is
    /// at `position` (Go tests sometimes leave the position at 0:0).
    pub(crate) fn request_with_position(
        document: TextDocument,
        node_offset: u32,
        position: protocol::Position,
    ) -> DefinitionRequest {
        let document = Arc::new(document);
        let root = document.root();
        let node = root
            .as_ref()
            .and_then(|root| root.node_at(TextSize::from(node_offset)));
        let mut params = protocol::DefinitionParams::default();
        params.text_document.uri = document.uri.clone();
        params.position = position;
        DefinitionRequest {
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

    /// A request with the node and cursor at `offset`.
    pub(crate) fn request_at(document: TextDocument, offset: u32) -> DefinitionRequest {
        let (line, character) = document.line_index().position_utf16(offset);
        request_with_position(document, offset, protocol::Position { line, character })
    }

    pub(crate) fn offset_of(source: &str, needle: &str) -> u32 {
        source.find(needle).expect(needle) as u32
    }
}
