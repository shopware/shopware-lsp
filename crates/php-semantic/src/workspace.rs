//! Public handles for compact workspace graphs: projection, persistence,
//! batch canonicalization, and lazy summary restoration.

use std::fmt;
use std::sync::Arc;

use crate::canon::Canon;
use crate::codec;
use crate::document::Document;
use crate::packed::PackedDocument;
use crate::view::SymbolView;

/// An opaque, immutable declaration/reference graph of one file, ready for
/// workspace publication and persistence.
pub struct WorkspaceGraph {
    pub(crate) document: PackedDocument,
}

impl fmt::Debug for WorkspaceGraph {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        f.debug_struct("WorkspaceGraph")
            .field("path", &self.path())
            .field("symbols", &self.document.symbols.len())
            .field("references", &self.document.references.len())
            .finish()
    }
}

/// Error returned by a [`WorkspaceGraphLoader`].
#[derive(Clone, Debug, PartialEq, Eq)]
pub struct LoadError(String);

impl LoadError {
    pub fn new(message: impl Into<String>) -> Self {
        LoadError(message.into())
    }
}

impl fmt::Display for LoadError {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        f.write_str(&self.0)
    }
}

impl std::error::Error for LoadError {}

/// Resolves one persisted graph by source path. Warm cache restoration keeps
/// declaration cores and hierarchy edges resident and loads signatures and
/// metadata through this loader only when requested. The loader is called on
/// every detail access of an unpinned summary, so implementations should
/// cache recently used graphs.
pub trait WorkspaceGraphLoader: Send + Sync {
    fn load_workspace_graph(&self, path: &str) -> Result<Arc<WorkspaceGraph>, LoadError>;
}

impl WorkspaceGraph {
    /// Packs a document that is already a workspace projection (or any
    /// document whose full content should be retained) without
    /// canonicalizing its values.
    pub fn pack_owned(document: Document) -> WorkspaceGraph {
        WorkspaceGraph {
            document: PackedDocument::pack(&document, false),
        }
    }

    /// The retained workspace projection of a full semantic document,
    /// canonicalized on its own. File-local declarations and variable
    /// references are omitted.
    pub fn project(document: &Document) -> WorkspaceGraph {
        let mut graph = WorkspaceGraph::project_borrowed(document);
        let mut detacher = WorkspaceGraphDetacher::new();
        detacher.detach_owned(&mut graph);
        graph
    }

    /// The workspace projection without canonicalization; run it through a
    /// batch [`WorkspaceGraphDetacher`] before publication so equal values are
    /// shared across the batch.
    pub fn project_borrowed(document: &Document) -> WorkspaceGraph {
        WorkspaceGraph {
            document: PackedDocument::pack(document, true),
        }
    }

    pub fn path(&self) -> &str {
        &self.document.path
    }

    /// A defensive public document view of the graph.
    pub fn document(&self) -> Document {
        self.document.materialize()
    }

    /// Visits the compact declarations without materializing symbols.
    pub fn visit_symbol_views(&self, mut visit: impl FnMut(SymbolView<'_>) -> bool) -> bool {
        (0..self.document.symbols.len() as u32)
            .all(|index| visit(SymbolView::packed(&self.document, index)))
    }

    /// Serializes the graph in the versioned binary cache format.
    pub fn encode(&self) -> Vec<u8> {
        codec::encode(&self.document)
    }

    /// Decodes a graph written by [`WorkspaceGraph::encode`].
    pub fn decode(bytes: &[u8]) -> Result<WorkspaceGraph, codec::DecodeError> {
        WorkspaceGraphDecoder::new().decode(bytes)
    }

    pub(crate) fn packed(&self) -> &PackedDocument {
        &self.document
    }

    /// Number of retained declarations.
    pub fn symbol_count(&self) -> usize {
        self.document.symbols.len()
    }

    /// Number of retained references.
    pub fn reference_count(&self) -> usize {
        self.document.references.len()
    }

    /// Whether signatures and metadata are loaded on demand.
    pub fn is_summary(&self) -> bool {
        self.document.lazy.is_some()
    }
}

/// Canonicalizes graphs of one scanner batch so equal values are shared
/// across all committed files. Not safe for concurrent use.
#[derive(Default)]
pub struct WorkspaceGraphDetacher {
    canon: Canon,
}

impl WorkspaceGraphDetacher {
    pub fn new() -> Self {
        Self::default()
    }

    /// `expected_types` is an allocation hint.
    pub fn with_capacity(expected_types: usize) -> Self {
        WorkspaceGraphDetacher {
            canon: Canon::with_capacity(expected_types),
        }
    }

    pub fn detach_owned(&mut self, graph: &mut WorkspaceGraph) {
        graph.document.canonicalize(&mut self.canon);
    }

    /// Releases construction-only tables.
    pub fn finish(self) {}
}

/// Decodes a stream of graphs, sharing parsed types across them. Keep one
/// decoder per bounded restore phase and drop (or [`clear`](Self::clear)) it
/// afterwards.
#[derive(Default)]
pub struct WorkspaceGraphDecoder {
    pub(crate) canon: Canon,
    pub(crate) type_cache: rustc_hash::FxHashMap<Box<str>, swlsp_php_types::Type>,
}

impl WorkspaceGraphDecoder {
    pub fn new() -> Self {
        Self::default()
    }

    pub fn clear(&mut self) {
        *self = WorkspaceGraphDecoder::default();
    }

    /// Decodes a complete graph.
    pub fn decode(&mut self, bytes: &[u8]) -> Result<WorkspaceGraph, codec::DecodeError> {
        let document = codec::decode(bytes, self, None)?;
        Ok(WorkspaceGraph { document })
    }

    /// Decodes declaration cores, hierarchy edges, and references while
    /// deferring signatures and metadata to `loader`.
    pub fn decode_summary(
        &mut self,
        bytes: &[u8],
        loader: Arc<dyn WorkspaceGraphLoader>,
    ) -> Result<WorkspaceGraph, codec::DecodeError> {
        let document = codec::decode(bytes, self, Some(loader))?;
        Ok(WorkspaceGraph { document })
    }
}
