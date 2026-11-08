//! Find-references providers (port of Go `internal/lsp/reference`).
//!
//! Go reads the request's PHP semantic state from the enriched
//! `context.Context`; Rust providers holding a PHP index derive the same
//! memoized state with [`php_context`] for PHP documents.

mod admin;
mod asset;
mod controller;
mod doctrine;
mod environment;
mod event;
mod live_component_event;
mod messenger;
mod route;
mod serializer;
mod style;
mod twig_component;
mod twig_constant;
mod twig_macro;
mod twig_php;
mod twig_template;

pub use admin::AdminReferenceProvider;
pub use asset::AssetReferenceProvider;
pub use controller::ControllerReferenceProvider;
pub use doctrine::DoctrineReferenceProvider;
pub use environment::EnvironmentReferenceProvider;
pub use event::EventReferenceProvider;
pub use live_component_event::LiveComponentEventReferenceProvider;
pub use messenger::MessengerReferenceProvider;
pub use route::RouteReferenceProvider;
pub use serializer::SerializerReferenceProvider;
pub use style::StyleClassReferenceProvider;
pub use twig_component::TwigComponentReferenceProvider;
pub use twig_constant::TwigConstantReferenceProvider;
pub use twig_macro::TwigMacroReferenceProvider;
pub use twig_php::TwigPhpReferenceProvider;
pub use twig_template::TwigTemplateReferenceProvider;

use std::collections::{HashMap, HashSet};
use std::sync::Arc;

use swlsp_language::LanguageId;
use swlsp_lsp::{ReferenceRequest, SyntaxContext, protocol_range};
use swlsp_lsp_protocol as protocol;
use swlsp_php::{PhpContext, PhpIndex};
use swlsp_php_semantic::{Document, Snapshot};
use swlsp_syntax::{LineIndex, TextRange};
use swlsp_uriutil::filepath;

#[cfg(test)]
mod tests;

/// The byte offset of the request position, `None` without a document.
pub(crate) fn request_offset(request: &ReferenceRequest) -> Option<u32> {
    let line_index = request.line_index()?;
    Some(line_index.offset_utf16(
        request.params.position.line,
        request.params.position.character,
    ))
}

/// The request document's file path (Go `uriutil.Path`).
pub(crate) fn request_path(request: &ReferenceRequest) -> Result<String, swlsp_uriutil::UriError> {
    swlsp_uriutil::path(&request.params.text_document.uri)
}

/// Lowercased Go `filepath.Ext`.
pub(crate) fn lower_ext(path: &str) -> String {
    filepath::ext(path).to_lowercase()
}

/// The request's PHP semantic context, as the Go server's PHP context
/// enricher attaches it to every PHP request.
pub(crate) fn php_context(php: Option<&PhpIndex>, syntax: &SyntaxContext) -> Option<PhpContext> {
    if syntax.language != Some(LanguageId::Php) {
        return None;
    }
    let php = php?;
    let document = syntax.document.as_ref()?;
    let state = swlsp_lsp_phpanalysis::for_document(php, document)
        .ok()
        .flatten()?;
    Some(php.analyzed_snapshot_context(
        syntax.node.as_ref(),
        state.document.clone(),
        Some(state.snapshot.clone()),
    ))
}

/// The semantic document and snapshot for a PHP request: the request
/// context, else the indexed document, else a fresh analysis.
pub(crate) fn php_semantic_state(
    php: &PhpIndex,
    request: &ReferenceRequest,
    path: &str,
) -> Option<(Arc<Document>, Arc<Snapshot>)> {
    if let Some(context) = php_context(Some(php), request) {
        return Some((context.document, context.snapshot));
    }
    if let Some(document) = php.semantic_document(path) {
        return Some((Arc::new(document), php.semantic_snapshot()));
    }
    let document = request.document.as_ref()?;
    let root = request.root.as_ref()?;
    let analyzed =
        Arc::new(php.analyze_document(path, i64::from(document.version), root, document.source()));
    let snapshot = php.semantic_snapshot().with_document(&analyzed);
    Some((analyzed, snapshot))
}

/// Line indexes of files read from disk during one request.
#[derive(Default)]
pub(crate) struct FileLines {
    files: HashMap<String, Option<Arc<LineIndex>>>,
}

impl FileLines {
    pub(crate) fn get(&mut self, path: &str) -> Option<Arc<LineIndex>> {
        self.files
            .entry(path.to_owned())
            .or_insert_with(|| {
                std::fs::read(path).ok().map(|source| {
                    Arc::new(LineIndex::new(
                        String::from_utf8_lossy(&source).into_owned(),
                    ))
                })
            })
            .clone()
    }

    /// The location of `range` in the file at `path`; `None` when the file
    /// cannot be read.
    pub(crate) fn location(&mut self, path: &str, range: TextRange) -> Option<protocol::Location> {
        let lines = self.get(path)?;
        Some(protocol::Location {
            uri: swlsp_uriutil::file_uri(path),
            range: protocol_range(&lines, range),
        })
    }
}

/// A location in the request document.
pub(crate) fn document_location(
    request: &ReferenceRequest,
    line_index: &LineIndex,
    range: TextRange,
) -> protocol::Location {
    protocol::Location {
        uri: request.params.text_document.uri.clone(),
        range: protocol_range(line_index, range),
    }
}

/// Drops exact duplicate locations, keeping the first occurrence.
pub(crate) fn unique_locations(locations: Vec<protocol::Location>) -> Vec<protocol::Location> {
    let mut seen = HashSet::with_capacity(locations.len());
    locations
        .into_iter()
        .filter(|location| seen.insert(location.clone()))
        .collect()
}

/// Sorts by URI and start position, keeping the order of equal starts.
pub(crate) fn sort_locations(locations: &mut [protocol::Location]) {
    locations.sort_by(|left, right| {
        left.uri
            .cmp(&right.uri)
            .then(left.range.start.line.cmp(&right.range.start.line))
            .then(left.range.start.character.cmp(&right.range.start.character))
    });
}

/// A zero-width location at the start of the 1-based `line` (clamped to 1).
pub(crate) fn line_location(path: &str, line: i64) -> protocol::Location {
    let line = (line.max(1) - 1) as u32;
    let position = protocol::Position { line, character: 0 };
    protocol::Location {
        uri: swlsp_uriutil::file_uri(path),
        range: protocol::Range {
            start: position,
            end: position,
        },
    }
}

/// A semantic symbol's selection range, else its full range.
pub(crate) fn symbol_range(symbol: &swlsp_php_semantic::Symbol) -> TextRange {
    if symbol.selection_range.is_empty() {
        symbol.range
    } else {
        symbol.selection_range
    }
}
