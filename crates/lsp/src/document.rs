//! Open-document snapshots and the document manager.
//!
//! A [`TextDocument`] is an immutable snapshot: opening or changing a
//! document publishes a new `Arc<TextDocument>`; nothing is mutated in place.
//! Diagnostics cache freshness by pointer identity, providers hold a snapshot
//! for a whole request without locking, and [`TextDocument::memoized_analysis`]
//! can cache derived analyses safely.

use std::any::{Any, TypeId};
use std::collections::HashMap;
use std::fmt;
use std::sync::{Arc, Mutex, RwLock};

use swlsp_language::LanguageId;
use swlsp_syntax::{
    LineIndex, Parse, ParseError, SyntaxNode, SyntaxNodeExt, SyntaxToken, TextRange, TextSize, Tree,
};

use crate::context::lock;

type AnalysisKey = (TypeId, usize);
type AnalysisValue = (u64, Arc<dyn Any + Send + Sync>);

/// A document open in the editor.
pub struct TextDocument {
    pub uri: String,
    pub version: i32,
    /// `None` for files without a registered parser.
    pub language: Option<LanguageId>,
    source: Arc<str>,
    tree: Option<Tree>,
    parse_errors: Vec<ParseError>,
    line_index: Arc<LineIndex>,
    analysis: Mutex<HashMap<AnalysisKey, AnalysisValue>>,
}

impl fmt::Debug for TextDocument {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        f.debug_struct("TextDocument")
            .field("uri", &self.uri)
            .field("version", &self.version)
            .field("language", &self.language)
            .finish_non_exhaustive()
    }
}

/// Parses `source` with the built-in language registry, choosing the parser
/// by the extension of `path` (Go `Registry.ParsePath`).
pub fn parse_path(path: &str, source: &str) -> Option<(LanguageId, Parse)> {
    let language = LanguageId::from_path(&go_ext(path))?;
    Some((language, language.parse(source)?))
}

/// Go `filepath.Ext` used as a pseudo path for [`LanguageId::from_path`].
fn go_ext(path: &str) -> String {
    let name = path.rsplit(['/', '\\']).next().unwrap_or(path);
    match name.rfind('.') {
        Some(index) => format!("x{}", &name[index..]),
        None => String::new(),
    }
}

impl TextDocument {
    /// Parses a snapshot through the built-in language registry. The parse
    /// tree and line index share one copy of the source.
    pub fn new(uri: impl Into<String>, source: &str, version: i32) -> Self {
        let uri = uri.into();
        let (language, source, tree, parse_errors) = match parse_path(&uri, source) {
            Some((language, parse)) => {
                let shared = parse.tree.source_arc().clone();
                (Some(language), shared, Some(parse.tree), parse.errors)
            }
            None => (None, Arc::<str>::from(source), None, Vec::new()),
        };
        let line_index = Arc::new(LineIndex::new(source.clone()));
        Self {
            uri,
            version,
            language,
            source,
            tree,
            parse_errors,
            line_index,
            analysis: Mutex::new(HashMap::new()),
        }
    }

    /// The exact immutable source snapshot.
    #[inline]
    pub fn source(&self) -> &str {
        &self.source
    }

    pub fn source_arc(&self) -> &Arc<str> {
        &self.source
    }

    pub fn tree(&self) -> Option<&Tree> {
        self.tree.as_ref()
    }

    /// The red root of the syntax tree (cheap to create, thread-local).
    pub fn root(&self) -> Option<SyntaxNode> {
        self.tree.as_ref().map(Tree::root)
    }

    pub fn parse_errors(&self) -> &[ParseError] {
        &self.parse_errors
    }

    pub fn line_index(&self) -> &LineIndex {
        &self.line_index
    }

    pub fn line_index_arc(&self) -> &Arc<LineIndex> {
        &self.line_index
    }

    /// One request-independent analysis per (snapshot, workspace revision,
    /// owner, value type). A newer revision replaces the previous value so
    /// open documents do not retain stale semantic generations.
    ///
    /// `owner` identifies the producing service; use a stable address such
    /// as `Arc::as_ptr(&service) as usize` so independent features share the
    /// computed value.
    pub fn memoized_analysis<T: Any + Send + Sync>(
        &self,
        owner: usize,
        revision: u64,
        compute: impl FnOnce() -> T,
    ) -> Arc<T> {
        let key = (TypeId::of::<T>(), owner);
        let mut cache = lock(&self.analysis);
        if let Some((cached_revision, value)) = cache.get(&key)
            && *cached_revision == revision
            && let Ok(value) = value.clone().downcast::<T>()
        {
            return value;
        }
        let value = Arc::new(compute());
        cache.insert(key, (revision, value.clone()));
        value
    }
}

/// Receives immutable open-document snapshots after the manager published
/// them. Domain indexes use this narrow boundary for request-local overlays.
#[derive(Clone, Default)]
pub struct DocumentObserver {
    pub did_open_or_change: Option<DidOpenOrChange>,
    pub did_close: Option<DidClose>,
}

/// Observer callback for a published snapshot.
pub type DidOpenOrChange = Arc<dyn Fn(&Arc<TextDocument>) + Send + Sync>;
/// Observer callback for a closed URI.
pub type DidClose = Arc<dyn Fn(&str) + Send + Sync>;

#[derive(Default)]
struct ManagerState {
    documents: HashMap<String, Arc<TextDocument>>,
    observers: Vec<DocumentObserver>,
}

/// Owns the newest snapshot of every open document.
#[derive(Default)]
pub struct DocumentManager {
    state: RwLock<ManagerState>,
}

impl DocumentManager {
    pub fn new() -> Self {
        Self::default()
    }

    pub fn open_document(&self, uri: &str, text: &str, version: i32) {
        self.publish(Arc::new(TextDocument::new(uri, text, version)));
    }

    pub fn update_document(&self, uri: &str, text: &str, version: i32) {
        self.publish(Arc::new(TextDocument::new(uri, text, version)));
    }

    fn publish(&self, document: Arc<TextDocument>) {
        let observers = {
            let mut state = write(&self.state);
            state
                .documents
                .insert(document.uri.clone(), document.clone());
            state.observers.clone()
        };
        for observer in observers {
            if let Some(callback) = &observer.did_open_or_change {
                callback(&document);
            }
        }
    }

    /// Subscribes to lifecycle changes and immediately replays documents
    /// that are already open, making initialization order irrelevant.
    pub fn register_observer(&self, observer: DocumentObserver) {
        if observer.did_open_or_change.is_none() && observer.did_close.is_none() {
            return;
        }
        let documents: Vec<Arc<TextDocument>> = {
            let mut state = write(&self.state);
            state.observers.push(observer.clone());
            state.documents.values().cloned().collect()
        };
        if let Some(callback) = &observer.did_open_or_change {
            for document in &documents {
                callback(document);
            }
        }
    }

    pub fn close_document(&self, uri: &str) {
        let observers = {
            let mut state = write(&self.state);
            state.documents.remove(uri);
            state.observers.clone()
        };
        for observer in observers {
            if let Some(callback) = &observer.did_close {
                callback(uri);
            }
        }
    }

    pub fn get_document(&self, uri: &str) -> Option<Arc<TextDocument>> {
        read(&self.state).documents.get(uri).cloned()
    }

    pub fn get_document_text(&self, uri: &str) -> Option<Arc<str>> {
        self.get_document(uri)
            .map(|document| document.source_arc().clone())
    }

    /// The document and CST context at an LSP position.
    pub fn syntax_context(&self, uri: &str, line: u32, character: u32) -> Option<SyntaxContext> {
        let document = self.get_document(uri)?;
        Some(SyntaxContext::new(document, line, character))
    }

    /// Every open document (unordered).
    pub fn documents(&self) -> Vec<Arc<TextDocument>> {
        read(&self.state).documents.values().cloned().collect()
    }

    /// Drops every document and observer, notifying observers of each close.
    pub fn close(&self) {
        let (observers, uris) = {
            let mut state = write(&self.state);
            let uris: Vec<String> = state.documents.drain().map(|(uri, _)| uri).collect();
            (std::mem::take(&mut state.observers), uris)
        };
        for uri in &uris {
            for observer in &observers {
                if let Some(callback) = &observer.did_close {
                    callback(uri);
                }
            }
        }
    }
}

fn read<T>(lock: &RwLock<T>) -> std::sync::RwLockReadGuard<'_, T> {
    lock.read().unwrap_or_else(|poisoned| poisoned.into_inner())
}

fn write<T>(lock: &RwLock<T>) -> std::sync::RwLockWriteGuard<'_, T> {
    lock.write()
        .unwrap_or_else(|poisoned| poisoned.into_inner())
}

/// The immutable snapshot and generic CST position context of a request.
#[derive(Clone, Default)]
pub struct SyntaxContext {
    pub document: Option<Arc<TextDocument>>,
    pub language: Option<LanguageId>,
    pub root: Option<SyntaxNode>,
    /// The token under the cursor (moved left off trivia and EOF).
    pub token: Option<SyntaxToken>,
    /// The smallest node containing the cursor.
    pub node: Option<SyntaxNode>,
}

impl fmt::Debug for SyntaxContext {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        f.debug_struct("SyntaxContext")
            .field("document", &self.document)
            .field("language", &self.language)
            .field("token", &self.token)
            .field("node", &self.node)
            .finish()
    }
}

impl SyntaxContext {
    pub fn new(document: Arc<TextDocument>, line: u32, character: u32) -> Self {
        let (root, token, node) = syntax_at_position(&document, line, character);
        Self {
            language: document.language,
            document: Some(document),
            root,
            token,
            node,
        }
    }

    /// The exact source snapshot, empty without a document.
    pub fn source(&self) -> &str {
        self.document.as_deref().map_or("", TextDocument::source)
    }

    pub fn tree(&self) -> Option<&Tree> {
        self.document.as_deref()?.tree()
    }

    pub fn line_index(&self) -> Option<&LineIndex> {
        self.document.as_deref().map(TextDocument::line_index)
    }
}

fn syntax_at_position(
    document: &TextDocument,
    line: u32,
    character: u32,
) -> (Option<SyntaxNode>, Option<SyntaxToken>, Option<SyntaxNode>) {
    let Some(root) = document.root() else {
        return (None, None, None);
    };
    let offset = TextSize::from(document.line_index().offset_utf16(line, character));
    let root_start = root.text_range().start();
    let mut token = root.token_at(offset);
    let mut node = root.node_at(offset);

    // At EOF there is no token on the right side of the cursor. Use the final
    // token so incomplete trailing input still has useful context.
    if token.is_none() && offset > root_start {
        let previous = offset - TextSize::from(1);
        token = root.token_at(previous);
        node = root.node_at(previous);
    }
    while let Some(current) = token.clone() {
        if !current.kind().is_trivia() || current.text_range().start() <= root_start {
            break;
        }
        let previous_offset = current.text_range().start() - TextSize::from(1);
        let Some(previous) = root.token_at(previous_offset) else {
            break;
        };
        if previous == current {
            break;
        }
        token = Some(previous);
        node = root.node_at(previous_offset);
    }
    (Some(root), token, node)
}

/// The smallest element covering `range`, used as a diagnostic anchor.
pub(crate) fn diagnostic_element(
    root: &SyntaxNode,
    range: TextRange,
) -> Option<swlsp_syntax::SyntaxElement> {
    root.descendant_for_range(range)
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn go_ext_matches_filepath_ext() {
        assert_eq!(go_ext("file:///tmp/a.html.twig"), "x.twig");
        assert_eq!(go_ext("file:///tmp/dir.php/file"), "");
        assert_eq!(go_ext("noext"), "");
    }
}
