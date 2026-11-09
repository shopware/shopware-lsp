//! Semantic queries over the published workspace generation (port of
//! `semantic_api.go`).

use std::cmp::Ordering;
use std::collections::HashSet;
use std::path::Path;
use std::sync::Arc;

use swlsp_indexer::ParsedFile;
use swlsp_php_binder::Binder;
use swlsp_php_inference::{Analyzer, link_members_owned};
use swlsp_php_resolver::MemberResolver;
use swlsp_php_semantic::{Document, Flags, Reference, Snapshot, Symbol, SymbolKind};
use swlsp_php_types::{Type, go_to_lower};
use swlsp_syntax::{SyntaxNode, TextRange, Tree};

use crate::{PhpIndex, lock};

/// The source class-like catalog of one snapshot generation.
pub(crate) struct ClassCatalog {
    snapshot: Arc<Snapshot>,
    symbols: Arc<[Symbol]>,
    names: Arc<[String]>,
}

fn named_class(name: &str) -> Type {
    Type::named(name.strip_prefix('\\').unwrap_or(name), Vec::new())
}

impl PhpIndex {
    /// The current immutable workspace generation.
    pub fn semantic_snapshot(&self) -> Arc<Snapshot> {
        self.inner.semantic_store.snapshot()
    }

    /// Collects the semantic interner now (see
    /// [`swlsp_php_semantic::Store::collect_atoms`]). Publishing collects
    /// automatically; this is for profiling and tests.
    pub fn collect_semantic_atoms(&self) -> bool {
        self.inner.semantic_store.collect_atoms()
    }

    /// The current generation without on-demand return inference: what
    /// indexing analyzes against, so preparing a file never analyzes other
    /// files and persists only what it was given.
    pub(crate) fn indexing_snapshot(&self) -> Arc<Snapshot> {
        self.semantic_snapshot().without_return_inference()
    }

    /// Binds, links and analyzes an unsaved document against a workspace
    /// generation that contains the document's own declarations. `source`
    /// is the complete text of the tree `root` belongs to. Return types
    /// inferred in other files are those of the current generation.
    pub fn analyze_document(
        &self,
        path: &str,
        version: i64,
        root: &SyntaxNode,
        source: &str,
    ) -> Document {
        self.analyze_against(&self.semantic_snapshot(), path, version, root, source)
    }

    /// [`PhpIndex::analyze_document`] against the generation `base`.
    pub(crate) fn analyze_against(
        &self,
        base: &Arc<Snapshot>,
        path: &str,
        version: i64,
        root: &SyntaxNode,
        source: &str,
    ) -> Document {
        let mut document = Binder::new().bind_node(path, version, Some(root), source);
        let file_name = Path::new(path)
            .file_name()
            .map(|name| name.to_string_lossy())
            .unwrap_or_default();
        if swlsp_php_types::equal_fold(&file_name, ".phpstorm.meta.php") {
            document.call_contracts = swlsp_php_phpstormmeta::parse(root);
        }
        let snapshot = base.with_declarations(&document);
        document.workspace_revision = snapshot.revision;
        let document = swlsp_php_binder::link_owned(document, &snapshot);
        let document = Analyzer::new(Some(snapshot.clone()), self.type_extensions())
            .analyze_owned_with_source(document, root, source);
        link_members_owned(document, &snapshot, root)
    }

    /// [`PhpIndex::analyze_document`] for a complete tree.
    pub fn analyze_tree(&self, path: &str, version: i64, tree: &Tree) -> Document {
        self.analyze_document(path, version, &tree.root(), tree.source())
    }

    /// The fully linked document shared by every indexer preparing the same
    /// immutable file. The scanner releases it before persistence, so
    /// semantic documents never become long-lived index state. It is
    /// analyzed against [`PhpIndex::indexing_snapshot`].
    pub fn analyze_parsed_file(&self, file: &ParsedFile) -> Arc<Document> {
        let key = self.identity();
        file.memoized(key, || match file.syntax_tree() {
            Some(tree) => self.analyze_against(
                &self.indexing_snapshot(),
                file.path(),
                0,
                &tree.root(),
                tree.source(),
            ),
            None => Document {
                path: file.path().into(),
                ..Document::default()
            },
        })
    }

    /// Reconstructs a fully linked document for an indexed path from source.
    /// The index persists only compact graphs; scopes and flow facts are
    /// rebuilt on demand.
    pub fn semantic_document(&self, path: &str) -> Option<Document> {
        if !self.semantic_snapshot().has_document(path) {
            return None;
        }
        let content = std::fs::read(path).ok()?;
        let source = String::from_utf8_lossy(&content);
        let parsed = swlsp_parser_php::parse(&source);
        Some(self.analyze_tree(path, 0, &parsed.tree))
    }

    /// The preferred class-like declaration for a fully qualified name.
    /// Source declarations win over internal runtime stubs.
    pub fn find_class(&self, name: &str) -> Option<Symbol> {
        let candidates = self.semantic_snapshot().classes(name);
        let preferred = candidates
            .iter()
            .position(|candidate| !candidate.flags.has(Flags::INTERNAL))
            .unwrap_or(0);
        candidates.into_iter().nth(preferred)
    }

    /// Methods named `method_name` of a class, including inherited and trait
    /// methods, with parameter types specialized for generic ancestors.
    pub fn find_methods(&self, class_name: &str, method_name: &str) -> Vec<Symbol> {
        if class_name.is_empty() || method_name.is_empty() {
            return Vec::new();
        }
        let snapshot = self.semantic_snapshot();
        MemberResolver::new(&snapshot)
            .methods(&named_class(class_name), method_name)
            .into_iter()
            .map(|member| member.symbol)
            .collect()
    }

    /// Properties named `property_name` (with or without `$`).
    pub fn find_properties(&self, class_name: &str, property_name: &str) -> Vec<Symbol> {
        if class_name.is_empty() || property_name.is_empty() {
            return Vec::new();
        }
        let snapshot = self.semantic_snapshot();
        MemberResolver::new(&snapshot)
            .properties(
                &named_class(class_name),
                property_name.strip_prefix('$').unwrap_or(property_name),
            )
            .into_iter()
            .map(|member| member.symbol)
            .collect()
    }

    /// Class constants or enum cases named `constant_name`.
    pub fn find_constants(&self, class_name: &str, constant_name: &str) -> Vec<Symbol> {
        if class_name.is_empty() || constant_name.is_empty() {
            return Vec::new();
        }
        let snapshot = self.semantic_snapshot();
        MemberResolver::new(&snapshot)
            .constants(&named_class(class_name), constant_name)
            .into_iter()
            .map(|member| member.symbol)
            .collect()
    }

    /// Declarations of a fully qualified global constant.
    pub fn find_global_constants(&self, name: &str) -> Vec<Symbol> {
        if name.is_empty() {
            return Vec::new();
        }
        self.semantic_snapshot().constants(name)
    }

    fn members_of_kinds(&self, class_name: &str, kinds: &[SymbolKind]) -> Vec<Symbol> {
        if class_name.is_empty() {
            return Vec::new();
        }
        let snapshot = self.semantic_snapshot();
        let mut result: Vec<Symbol> = MemberResolver::new(&snapshot)
            .all(&named_class(class_name))
            .into_iter()
            .map(|member| member.symbol)
            .filter(|symbol| kinds.contains(&symbol.kind))
            .collect();
        result.sort_unstable_by(|left, right| {
            compare_fold(&left.name, &right.name)
                .then_with(|| left.name.cmp(&right.name))
                .then_with(|| compare_declaration(left, right))
        });
        result
    }

    /// Every constant and enum case visible on a class.
    pub fn constants(&self, class_name: &str) -> Vec<Symbol> {
        self.members_of_kinds(
            class_name,
            &[SymbolKind::ClassConstant, SymbolKind::EnumCase],
        )
    }

    /// Every property visible on a class.
    pub fn properties(&self, class_name: &str) -> Vec<Symbol> {
        self.members_of_kinds(class_name, &[SymbolKind::Property])
    }

    /// Every method visible on a class.
    pub fn methods(&self, class_name: &str) -> Vec<Symbol> {
        self.members_of_kinds(class_name, &[SymbolKind::Method])
    }

    /// All constant declarations in display-name/path order.
    pub fn constant_symbols(&self) -> Vec<Symbol> {
        let snapshot = self.semantic_snapshot();
        let mut result: Vec<(String, Symbol)> = snapshot
            .all_symbols()
            .into_iter()
            .filter(|symbol| {
                matches!(
                    symbol.kind,
                    SymbolKind::GlobalConstant | SymbolKind::ClassConstant | SymbolKind::EnumCase
                )
            })
            .map(|symbol| (constant_display_name(&snapshot, &symbol), symbol))
            .collect();
        result.sort_unstable_by(|(left_name, left), (right_name, right)| {
            compare_fold(left_name, right_name)
                .then_with(|| left_name.cmp(right_name))
                .then_with(|| compare_declaration(left, right))
        });
        result.into_iter().map(|(_, symbol)| symbol).collect()
    }

    /// A global or class constant as written in Symfony configuration.
    pub fn constant_symbol_name(&self, symbol: &Symbol) -> String {
        constant_display_name(&self.semantic_snapshot(), symbol)
    }

    /// Source class-like declarations in name/path order. Internal runtime
    /// stubs are omitted.
    pub fn class_symbols(&self) -> Vec<Symbol> {
        self.class_symbols_view().to_vec()
    }

    /// The generation-scoped immutable class catalog, shared between
    /// requests instead of rematerialized per document.
    pub fn class_symbols_view(&self) -> Arc<[Symbol]> {
        self.class_catalog().0
    }

    pub fn class_names(&self) -> Vec<String> {
        self.class_names_view().to_vec()
    }

    pub fn class_names_view(&self) -> Arc<[String]> {
        self.class_catalog().1
    }

    fn class_catalog(&self) -> (Arc<[Symbol]>, Arc<[String]>) {
        let snapshot = self.semantic_snapshot();
        let mut catalog = lock(&self.inner.class_catalog);
        if let Some(current) = catalog.as_ref()
            && Arc::ptr_eq(&current.snapshot, &snapshot)
        {
            return (current.symbols.clone(), current.names.clone());
        }
        let mut views: Vec<_> = snapshot
            .global_class_views()
            .into_iter()
            .filter(|view| {
                let flags = view.flags();
                !(flags.has(Flags::INTERNAL)
                    || flags.has(Flags::SYNTHETIC) && !flags.has(Flags::CLASS_ALIAS))
            })
            .collect();
        // Declarations with the same name in the same file (e.g. an
        // interface and its `class_alias`) are ordered by position, then ID.
        views.sort_unstable_by(|left, right| {
            compare_fold(left.fully_qualified(), right.fully_qualified())
                .then_with(|| left.fully_qualified().cmp(right.fully_qualified()))
                .then_with(|| left.path().cmp(right.path()))
                .then_with(|| left.range().start().cmp(&right.range().start()))
                .then_with(|| left.id().cmp(right.id()))
        });
        let symbols: Vec<Symbol> = views.iter().map(|view| view.materialize()).collect();
        let mut seen = HashSet::with_capacity(symbols.len());
        let names: Vec<String> = symbols
            .iter()
            .filter(|symbol| seen.insert(go_to_lower(&symbol.fully_qualified)))
            .map(|symbol| symbol.fully_qualified.to_string())
            .collect();
        let symbols: Arc<[Symbol]> = symbols.into();
        let names: Arc<[String]> = names.into();
        *catalog = Some(ClassCatalog {
            snapshot: snapshot.clone(),
            symbols: symbols.clone(),
            names: names.clone(),
        });
        (symbols, names)
    }

    /// Class-like declarations of one file.
    pub fn class_symbols_in(&self, path: &str) -> Vec<Symbol> {
        self.semantic_snapshot()
            .symbols_in(path)
            .into_iter()
            .filter(Symbol::is_class_like)
            .collect()
    }
}

fn constant_display_name(snapshot: &Snapshot, symbol: &Symbol) -> String {
    if symbol.kind == SymbolKind::GlobalConstant {
        return symbol
            .fully_qualified
            .strip_prefix('\\')
            .unwrap_or(&symbol.fully_qualified)
            .to_owned();
    }
    match snapshot.symbol(symbol.container.as_str()) {
        Some(container) => format!(
            "{}::{}",
            container
                .fully_qualified
                .strip_prefix('\\')
                .unwrap_or(&container.fully_qualified),
            symbol.name
        ),
        None => symbol.name.to_string(),
    }
}

/// Case-insensitive name order: ASCII folds byte-wise; any non-ASCII byte in
/// the common prefix compares the Unicode-lowercased strings (Go
/// `compareFold`).
/// Orders two declarations that tie on their displayed name by source
/// position, then by their stable ID.
fn compare_declaration(left: &Symbol, right: &Symbol) -> Ordering {
    left.path
        .cmp(&right.path)
        .then_with(|| left.range.start().cmp(&right.range.start()))
        .then_with(|| left.id.cmp(&right.id))
}

pub fn compare_fold(left: &str, right: &str) -> Ordering {
    let (left_bytes, right_bytes) = (left.as_bytes(), right.as_bytes());
    for (&left_byte, &right_byte) in left_bytes.iter().zip(right_bytes) {
        if left_byte >= 0x80 || right_byte >= 0x80 {
            return go_to_lower(left).cmp(&go_to_lower(right));
        }
        let ordering = left_byte
            .to_ascii_lowercase()
            .cmp(&right_byte.to_ascii_lowercase());
        if ordering != Ordering::Equal {
            return ordering;
        }
    }
    left_bytes.len().cmp(&right_bytes.len())
}

fn range_contains_cursor(range: TextRange, offset: u32) -> bool {
    let (start, end) = (u32::from(range.start()), u32::from(range.end()));
    (start <= offset && offset < end) || (offset == end && end > start)
}

/// The declaration or linked name reference at a byte offset.
pub fn symbol_at(document: &Document, snapshot: &Snapshot, offset: u32) -> Option<Symbol> {
    if let Some(reference) = document.references.iter().find(|reference| {
        range_contains_cursor(reference.range, offset) && !reference.resolved.is_empty()
    }) {
        return snapshot.symbol(reference.resolved.as_str());
    }
    let mut best: Option<&Symbol> = None;
    let mut best_width = u32::MAX;
    for symbol in &document.symbols {
        let mut range = symbol.selection_range;
        if range.is_empty() {
            range = symbol.range;
        }
        let width = u32::from(range.len());
        if !range_contains_cursor(range, offset) || width > best_width {
            continue;
        }
        best = Some(symbol);
        best_width = width;
    }
    best.cloned()
}

/// The first reference containing a byte offset.
pub fn reference_at(document: &Document, offset: u32) -> Option<&Reference> {
    document
        .references
        .iter()
        .find(|reference| range_contains_cursor(reference.range, offset))
}
