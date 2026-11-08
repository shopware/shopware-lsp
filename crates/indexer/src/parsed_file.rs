//! The immutable per-file input shared by every indexer handling one file.

use std::any::{Any, TypeId};
use std::collections::HashMap;
use std::sync::{Arc, Mutex, OnceLock};

use swlsp_language::LanguageId;
use swlsp_syntax::{LineIndex, Tree};

use crate::filetypes::language_for_path;
use crate::store::lock;
use crate::workspace_symbols::WorkspaceSymbol;
use crate::{Mutation, paths};

type MemoizedSlot = Arc<OnceLock<Arc<dyn Any + Send + Sync>>>;

/// One source file prepared for indexing. Syntax and line data are built
/// lazily and at most once, so multiple indexers never repeat the same
/// frontend work.
///
/// Sources are decoded lossily as UTF-8; byte offsets refer to the decoded
/// text.
pub struct ParsedFile {
    path: String,
    source: Arc<str>,
    extension: String,
    language: Option<LanguageId>,
    syntax: OnceLock<Option<Tree>>,
    line_index: OnceLock<LineIndex>,
    memoized: Mutex<HashMap<(TypeId, usize), MemoizedSlot>>,
    mutation: Option<Arc<Mutation>>,
    workspace_symbols: Mutex<Vec<WorkspaceSymbol>>,
}

impl std::fmt::Debug for ParsedFile {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        f.debug_struct("ParsedFile")
            .field("path", &self.path)
            .field("language", &self.language)
            .finish()
    }
}

impl ParsedFile {
    /// Takes ownership of the file content, reusing the allocation when it is
    /// valid UTF-8.
    pub fn new(path: impl Into<String>, content: Vec<u8>) -> Self {
        let source: Arc<str> = match String::from_utf8(content) {
            Ok(text) => text.into(),
            Err(error) => String::from_utf8_lossy(error.as_bytes()).into(),
        };
        Self::from_source(path, source)
    }

    pub fn from_source(path: impl Into<String>, source: impl Into<Arc<str>>) -> Self {
        let path = path.into();
        let extension = paths::ext(&path).to_lowercase();
        let language = language_for_path(&path);
        Self {
            path,
            source: source.into(),
            extension,
            language,
            syntax: OnceLock::new(),
            line_index: OnceLock::new(),
            memoized: Mutex::new(HashMap::new()),
            mutation: None,
            workspace_symbols: Mutex::new(Vec::new()),
        }
    }

    pub fn path(&self) -> &str {
        &self.path
    }

    pub fn source(&self) -> &str {
        &self.source
    }

    pub fn source_arc(&self) -> &Arc<str> {
        &self.source
    }

    pub fn content(&self) -> &[u8] {
        self.source.as_bytes()
    }

    /// The lower-cased extension including its dot, or an empty string.
    pub fn extension(&self) -> &str {
        &self.extension
    }

    pub fn language(&self) -> Option<LanguageId> {
        self.language
    }

    /// The syntax tree of the file's language. Unsupported files (and
    /// languages without a ported parser) return `None`.
    pub fn syntax_tree(&self) -> Option<&Tree> {
        self.syntax
            .get_or_init(|| {
                let language = self.language?;
                // The tree shares this file's source; only the green tree's
                // token text is a second copy.
                language
                    .parse_shared(Arc::clone(&self.source))
                    .map(|parse| parse.tree)
            })
            .as_ref()
    }

    /// Whether the syntax tree has already been built.
    pub fn has_syntax_tree(&self) -> bool {
        self.syntax.get().is_some_and(Option::is_some)
    }

    pub fn line_index(&self) -> &LineIndex {
        self.line_index
            .get_or_init(|| LineIndex::new(self.source.clone()))
    }

    /// One file-lifetime derived value per `(T, key)`. Concurrent consumers
    /// share the same computation, which lets independent indexers reuse
    /// expensive read-only analysis. A component's address is normally the
    /// safest key because it also isolates separate workspace instances.
    pub fn memoized<T: Any + Send + Sync>(
        &self,
        key: usize,
        compute: impl FnOnce() -> T,
    ) -> Arc<T> {
        let slot = lock(&self.memoized)
            .entry((TypeId::of::<T>(), key))
            .or_default()
            .clone();
        let value = slot
            .get_or_init(|| Arc::new(compute()) as Arc<dyn Any + Send + Sync>)
            .clone();
        value
            .downcast::<T>()
            .expect("memoized slots are keyed by value type")
    }

    /// Releases cross-indexer analysis after the read-only preparation phase.
    pub(crate) fn clear_memoized(&self) {
        lock(&self.memoized).clear();
    }

    /// The workspace-store transaction assigned by the scanner; `None`
    /// outside a coordinated scanner run (and during preparation).
    pub fn mutation(&self) -> Option<&Mutation> {
        self.mutation.as_deref()
    }

    pub(crate) fn set_mutation(&mut self, mutation: Option<Arc<Mutation>>) {
        self.mutation = mutation;
    }

    /// Publishes declarations discovered while an indexer already processes
    /// this file. The scanner persists them in the same file transaction.
    pub fn add_workspace_symbols(&self, symbols: impl IntoIterator<Item = WorkspaceSymbol>) {
        lock(&self.workspace_symbols).extend(symbols);
    }

    pub(crate) fn take_workspace_symbols(&self) -> Vec<WorkspaceSymbol> {
        std::mem::take(&mut *lock(&self.workspace_symbols))
    }

    pub(crate) fn clear_workspace_symbols(&self) {
        lock(&self.workspace_symbols).clear();
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use std::sync::atomic::{AtomicU32, Ordering};

    #[test]
    fn reuses_syntax_and_line_index_across_threads() {
        let file = ParsedFile::new(
            "/project/Resources/config/services.xml",
            b"<container>\n<services/>\n</container>".to_vec(),
        );
        let results: Vec<(usize, usize)> = std::thread::scope(|scope| {
            let handles: Vec<_> = (0..16)
                .map(|_| {
                    scope.spawn(|| {
                        let tree = file.syntax_tree().expect("xml parses") as *const Tree as usize;
                        let index = file.line_index() as *const LineIndex as usize;
                        (tree, index)
                    })
                })
                .collect();
            handles
                .into_iter()
                .map(|handle| handle.join().unwrap())
                .collect()
        });
        assert!(results.iter().all(|result| *result == results[0]));
        assert_eq!(file.syntax_tree().unwrap().source(), file.source());
    }

    #[test]
    fn syntax_tree_shares_the_file_source() {
        for path in [
            "/p/a.json",
            "/p/a.js",
            "/p/a.xml",
            "/p/a.scss",
            "/p/a.php",
            "/p/a.html.twig",
            "/p/a.yaml",
            "/p/a.vue",
        ] {
            let file = ParsedFile::new(path, b"<template>x</template>\n".to_vec());
            let tree = file.syntax_tree().expect(path);
            assert!(Arc::ptr_eq(tree.source_arc(), file.source_arc()), "{path}");
            assert_eq!(tree.root().text().to_string(), file.source(), "{path}");
        }
    }

    #[test]
    fn unsupported_extension_does_not_parse() {
        let file = ParsedFile::new("/project/README.md", b"# Project".to_vec());
        assert!(file.syntax_tree().is_none());
        assert_eq!(file.extension(), ".md");
        assert_eq!(file.line_index().line_count(), 1);
    }

    #[test]
    fn invalid_utf8_is_decoded_lossily() {
        let file = ParsedFile::new("/project/a.json", vec![b'"', 0xff, b'"']);
        assert_eq!(file.source(), "\"\u{fffd}\"");
    }

    #[test]
    fn memoizes_derived_values_concurrently() {
        let file = ParsedFile::new("/project/src/Example.php", b"<?php".to_vec());
        let computations = AtomicU32::new(0);
        let values: Vec<Arc<u32>> = std::thread::scope(|scope| {
            let handles: Vec<_> = (0..32)
                .map(|_| {
                    scope.spawn(|| {
                        file.memoized(1, || computations.fetch_add(1, Ordering::SeqCst) + 1)
                    })
                })
                .collect();
            handles
                .into_iter()
                .map(|handle| handle.join().unwrap())
                .collect()
        });
        assert_eq!(computations.load(Ordering::SeqCst), 1);
        assert!(values.iter().all(|value| Arc::ptr_eq(value, &values[0])));

        file.clear_memoized();
        let recomputed = file.memoized(1, || computations.fetch_add(1, Ordering::SeqCst) + 1);
        assert_eq!(computations.load(Ordering::SeqCst), 2);
        assert!(!Arc::ptr_eq(&values[0], &recomputed));
    }

    #[test]
    fn memoizes_multiple_derived_values() {
        let file = ParsedFile::new("/project/src/Example.php", b"<?php".to_vec());
        let first = file.memoized(1, || "first".to_owned());
        let second = file.memoized(2, || "second".to_owned());
        let other_type = file.memoized(1, || 7_u64);
        assert!(Arc::ptr_eq(
            &first,
            &file.memoized(1, || -> String { panic!("first recomputed") })
        ));
        assert!(Arc::ptr_eq(
            &second,
            &file.memoized(2, || -> String { panic!("second recomputed") })
        ));
        assert_eq!(*other_type, 7);
        file.clear_memoized();
        assert_eq!(
            *file.memoized(1, || "first after clear".to_owned()),
            "first after clear"
        );
    }
}
