//! The PHP workspace index.
//!
//! [`PhpIndex`] ties the PHP engine together: it binds, links and analyzes
//! files ([`PhpIndex::analyze_document`]), persists one compact workspace
//! graph per file in the shared SQLite store, publishes immutable semantic
//! snapshots after each committed transaction (one generation per scanner
//! batch), restores warm caches as lazily detailed summaries, and serves the
//! semantic queries, Twig template contexts, assistant-tag and embedded
//! language lookups used by LSP features.
//!
//! Request state is passed explicitly as a [`PhpContext`]. The persisted
//! graph payload is `swlsp-php-semantic`'s binary codec compressed as a
//! size-prefixed LZ4 block inside a `{format, payload}` MessagePack envelope.
//! Member listings sorted by case-insensitive name use a stable sort, so
//! names equal under case folding keep their declaration order.

mod assistant;
mod context;
mod embedded;
mod loader;
mod persistence;
mod return_inference;
mod semantic_api;
mod settle;
mod twig_context;

/// The revision PHP analyses depend on: published PHP data, the project
/// model and the data type extensions answer from.
impl swlsp_indexer::Revisioned for PhpIndex {
    fn revision(&self) -> u64 {
        PhpIndex::revision(self) + self.analysis_revision()
    }
}

#[cfg(test)]
mod tests;

use std::collections::HashMap;
use std::path::{Path, PathBuf};
use std::sync::atomic::{AtomicU64, Ordering};
use std::sync::{Arc, Mutex, MutexGuard, RwLock, RwLockReadGuard, RwLockWriteGuard};

use swlsp_indexer::{
    BatchIndexer, CancelToken, DataIndexer, Error, Indexer, Mutation, ParsedFile, Prepared,
    PreparingIndexer, Result, Store, SupplementalPathIndexer, SupplementalSyntaxIndexer,
    TransactionalClearer, TransactionalRemover, WorkspaceSymbol, WorkspaceSymbolContributor,
    WorkspaceSymbolKind, WorkspaceSymbolPosition, WorkspaceSymbolRange,
};
use swlsp_php_inference::{Builtins, Extension};
use swlsp_php_project::Model;
use swlsp_php_semantic::{
    Document, Store as SemanticStore, SymbolKind, WorkspaceGraph, WorkspaceGraphDetacher,
    WorkspaceGraphLoader, WorkspaceRestoreCapacity,
};

pub use assistant::{
    assistant_argument_reference, assistant_argument_tags, assistant_sibling_string_argument,
};
pub use context::{NameResolver, PhpContext, name_resolver_for};
pub use embedded::{
    EmbeddedCssSelector, EmbeddedJsonLiteral, EmbeddedLanguage, EmbeddedLanguageString,
    EmbeddedPhpString, EmbeddedXPathExpression, embedded_css_selectors, embedded_json_literals,
    embedded_language_strings, embedded_xpath_expressions,
};
pub use loader::WORKSPACE_GRAPH_DETAIL_CACHE_SIZE;
pub use persistence::{
    BorrowedWorkspaceGraph, EnvelopeError, PersistError, PersistedWorkspaceGraph,
    decode_persisted_workspace_graph_borrowed, encode_workspace_graph, inflate_payload,
};
pub use semantic_api::{compare_fold, reference_at, symbol_at};
pub use twig_context::{
    TwigRange, TwigTemplateContext, TwigTemplateVariable, guessed_controller_template,
};

use loader::WorkspaceGraphRepositoryLoader;

/// Below this many persisted graphs a restore decodes them in place:
/// starting threads would cost more than they save.
const PARALLEL_RESTORE_MINIMUM_DOCUMENTS: usize = 2_000;
const MAXIMUM_RESTORE_THREADS: usize = 4;

/// Decoding threads for restoring `documents` persisted graphs.
fn restore_threads(documents: usize) -> usize {
    if documents < PARALLEL_RESTORE_MINIMUM_DOCUMENTS {
        return 1;
    }
    std::thread::available_parallelism()
        .map_or(1, usize::from)
        .min(MAXIMUM_RESTORE_THREADS)
}

const WORKSPACE_RESTORE_STRINGS_PER_DOCUMENT: usize = 18;
const WORKSPACE_RESTORE_TYPE_NUMERATOR: usize = 5;
const WORKSPACE_RESTORE_TYPE_DENOMINATOR: usize = 4;

/// Repository namespace of the persisted compact graphs.
pub const WORKSPACE_GRAPH_NAMESPACE: &str = "php.semantic.workspace_graphs";
/// Repository namespace of the Twig template contexts.
pub const TWIG_CONTEXT_NAMESPACE: &str = "php.twig.template_contexts";

pub(crate) fn lock<T>(mutex: &Mutex<T>) -> MutexGuard<'_, T> {
    mutex.lock().unwrap_or_else(|poison| poison.into_inner())
}

fn read<T>(lock: &RwLock<T>) -> RwLockReadGuard<'_, T> {
    lock.read().unwrap_or_else(|poison| poison.into_inner())
}

fn write<T>(lock: &RwLock<T>) -> RwLockWriteGuard<'_, T> {
    lock.write().unwrap_or_else(|poison| poison.into_inner())
}

#[derive(Default)]
struct ProjectState {
    project: Option<Arc<Model>>,
    supplemental: Arc<SupplementalPaths>,
}

/// Composer source roots and files outside the default scan, cleaned once:
/// the scanner asks about every directory and file it would otherwise skip.
#[derive(Default)]
struct SupplementalPaths {
    roots: Vec<String>,
    files: Vec<String>,
    /// The directories of `files`.
    file_directories: Vec<String>,
}

#[derive(Default)]
struct BatchState {
    depth: usize,
    pending: HashMap<String, WorkspaceGraph>,
    detacher: Option<WorkspaceGraphDetacher>,
    /// Committed files of the running batch whose graphs carry return types
    /// inferred against the pre-batch generation.
    settle: Vec<settle::Candidate>,
    /// Candidates of the last finished batch, taken by
    /// [`BatchIndexer::settle_indexing_batch`].
    settling: Vec<settle::Candidate>,
}

pub(crate) struct Inner {
    pub(crate) workspace_graphs: Arc<DataIndexer<PersistedWorkspaceGraph>>,
    pub(crate) twig_contexts: DataIndexer<TwigTemplateContext>,
    pub(crate) semantic_store: SemanticStore,
    owned_store: Option<Store>,
    project: RwLock<ProjectState>,
    extensions: RwLock<Vec<Arc<dyn Extension>>>,
    revision: AtomicU64,
    batch: Mutex<BatchState>,
    pub(crate) twig_cache: Mutex<twig_context::TwigCache>,
    pub(crate) class_catalog: Mutex<Option<semantic_api::ClassCatalog>>,
    pub(crate) loader: Arc<WorkspaceGraphRepositoryLoader>,
}

/// Owns the persisted compact workspace graphs and publishes immutable
/// workspace snapshots. A document update becomes visible only after the
/// shared index transaction commits. Cloning shares the index.
#[derive(Clone)]
pub struct PhpIndex {
    pub(crate) inner: Arc<Inner>,
}

impl std::fmt::Debug for PhpIndex {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        f.debug_struct("PhpIndex")
            .field("revision", &self.revision())
            .finish()
    }
}

/// The prepared value of one PHP file: the graph to publish after commit,
/// its persisted encoding, and the file's Twig template contexts.
struct PreparedPhpDocument {
    graph: Arc<Mutex<Option<WorkspaceGraph>>>,
    storage: PersistedWorkspaceGraph,
    twig_contexts: Vec<TwigTemplateContext>,
    settle: Option<settle::Candidate>,
}

fn is_php_path(path: &str) -> bool {
    Path::new(path)
        .extension()
        .and_then(|extension| extension.to_str())
        .is_some_and(|extension| extension.eq_ignore_ascii_case("php"))
}

/// Loads the Composer project model of `root` and the runtime stub
/// document matching its PHP version and extensions.
fn load_project<S: AsRef<str>>(
    root: &Path,
    enabled: &[S],
    disabled: &[S],
) -> Result<(ProjectState, Document)> {
    let mut model = swlsp_php_project::load(root).map_err(Error::other)?;
    model.configure_extensions(enabled, disabled);
    let stub_extensions = model.stub_extensions();
    model.loaded_extensions = Some(swlsp_php_stubs::selected_extensions(
        &stub_extensions,
        &[&model.disabled_extensions],
    ));
    let files: Vec<String> = model.files.iter().map(|file| clean_path(file)).collect();
    let supplemental = SupplementalPaths {
        roots: model
            .source_roots()
            .iter()
            .map(|root| clean_path(root))
            .collect(),
        file_directories: files.iter().map(|file| parent_directory(file)).collect(),
        files,
    };
    let stubs = swlsp_php_stubs::document_for_extensions(
        model.php_version,
        &stub_extensions,
        &[&model.disabled_extensions],
    );
    let project = ProjectState {
        project: Some(Arc::new(model)),
        supplemental: Arc::new(supplemental),
    };
    Ok((project, stubs))
}

fn persist_error(error: PersistError) -> Error {
    Error::other(error)
}

impl PhpIndex {
    /// Opens the PHP repositories in `store`, or in an owned
    /// `<config_dir>/php.db` when no workspace store is given, and restores
    /// every persisted graph as a lazily detailed summary.
    pub fn open(config_dir: impl AsRef<Path>, store: Option<&Store>) -> Result<PhpIndex> {
        Self::open_with(config_dir, store, None)
    }

    /// Opens the index like [`PhpIndex::open`] and configures the Composer
    /// project of `root` before restoring, so the persisted graphs and the
    /// runtime stubs publish as one semantic generation instead of two.
    pub fn open_project<S: AsRef<str>>(
        config_dir: impl AsRef<Path>,
        store: Option<&Store>,
        root: &Path,
        enabled_extensions: &[S],
        disabled_extensions: &[S],
    ) -> Result<PhpIndex> {
        let (project, stubs) = load_project(root, enabled_extensions, disabled_extensions)?;
        Self::open_with(config_dir, store, Some((project, stubs)))
    }

    fn open_with(
        config_dir: impl AsRef<Path>,
        store: Option<&Store>,
        project: Option<(ProjectState, Document)>,
    ) -> Result<PhpIndex> {
        let db_path = config_dir.as_ref().join("php.db");
        let (store, owned_store) = match store {
            Some(store) => (store.clone(), None),
            None => {
                let store =
                    Store::open(&db_path).map_err(|err| err.context("create PHP index store"))?;
                (store.clone(), Some(store))
            }
        };
        let close_owned = |error: Error| {
            if let Some(owned) = &owned_store {
                let _ = owned.close();
            }
            error
        };
        // Graph payloads are LZ4 blocks already.
        let workspace_graphs = DataIndexer::in_store(&store, WORKSPACE_GRAPH_NAMESPACE)
            .map_err(|err| close_owned(err.context("create PHP workspace graph repository")))?
            .without_value_compression();
        let twig_contexts = DataIndexer::in_store(&store, TWIG_CONTEXT_NAMESPACE)
            .map_err(|err| close_owned(err.context("create PHP Twig context repository")))?;
        let workspace_graphs = Arc::new(workspace_graphs);
        let loader = Arc::new(WorkspaceGraphRepositoryLoader::new(
            workspace_graphs.clone(),
        ));
        let index = PhpIndex {
            inner: Arc::new(Inner {
                workspace_graphs,
                twig_contexts,
                semantic_store: SemanticStore::new(),
                owned_store,
                project: RwLock::new(ProjectState::default()),
                extensions: RwLock::new(vec![Arc::new(Builtins)]),
                revision: AtomicU64::new(0),
                batch: Mutex::new(BatchState::default()),
                twig_cache: Mutex::new(twig_context::TwigCache::default()),
                class_catalog: Mutex::new(None),
                loader,
            }),
        };
        index.inner.semantic_store.set_return_inference(Arc::new(
            return_inference::IndexReturnInference {
                inner: Arc::downgrade(&index.inner),
            },
        ));
        // Cached detail graphs pin the interner generation they were decoded
        // in; drop them when the semantic store collects so it can be freed.
        let loader = index.inner.loader.clone();
        index
            .inner
            .semantic_store
            .on_atom_collection(move || loader.clear());
        let stubs = project.map(|(project, stubs)| {
            *write(&index.inner.project) = project;
            stubs
        });
        if let Err(error) = index.restore(stubs.as_slice()) {
            let _ = index.close();
            return Err(error);
        }
        if stubs.is_some() {
            index.inner.revision.fetch_add(1, Ordering::SeqCst);
        }
        Ok(index)
    }

    fn restore(&self, documents: &[Document]) -> Result<()> {
        let inner = &self.inner;
        let count = inner
            .workspace_graphs
            .count_all_values()
            .map_err(|err| err.context("count PHP workspace graph repository"))?;
        let capacity = WorkspaceRestoreCapacity {
            documents: count,
            strings: count * WORKSPACE_RESTORE_STRINGS_PER_DOCUMENT,
            types: (count * WORKSPACE_RESTORE_TYPE_NUMERATOR)
                .div_ceil(WORKSPACE_RESTORE_TYPE_DENOMINATOR),
        };
        let loader: Arc<dyn WorkspaceGraphLoader> = inner.loader.clone();
        let decode = |decoder: &mut swlsp_php_semantic::WorkspaceGraphDecoder, encoded: &[u8]| {
            decode_persisted_workspace_graph_borrowed(encoded)
                .and_then(|persisted| persisted.decode_summary(decoder, loader.clone()))
                .map_err(persist_error)
        };
        let threads = restore_threads(count);
        let result = if threads > 1 {
            inner.semantic_store.restore_workspace_graphs_parallel(
                capacity,
                documents,
                threads,
                |accept| {
                    inner.workspace_graphs.visit_all_encoded_values(|encoded| {
                        accept(encoded.to_vec());
                        Ok(())
                    })
                },
                decode,
            )
        } else {
            inner.semantic_store.restore_workspace_graphs_decoded_with(
                capacity,
                documents,
                |decoder, accept| {
                    inner.workspace_graphs.visit_all_encoded_values(|encoded| {
                        accept(decode(decoder, encoded)?);
                        Ok(())
                    })
                },
            )
        };
        // Restoration is the only high-volume decode phase; release the
        // pooled decompression buffer while the server is idle.
        persistence::release_codec_pool();
        result
            .map(|_| ())
            .map_err(|err| err.context("load PHP workspace graph repository"))
    }

    /// Loads Composer roots and version information and publishes the
    /// matching runtime stub document.
    pub fn configure_project(&self, root: &Path) -> Result<()> {
        self.configure_project_with_extensions::<&str>(root, &[], &[])
    }

    pub fn configure_project_with_extensions<S: AsRef<str>>(
        &self,
        root: &Path,
        enabled: &[S],
        disabled: &[S],
    ) -> Result<()> {
        let (project, stubs) = load_project(root, enabled, disabled)?;
        *write(&self.inner.project) = project;
        self.inner.semantic_store.replace(&stubs);
        self.inner.revision.fetch_add(1, Ordering::SeqCst);
        Ok(())
    }

    /// The configured Composer project model.
    pub fn project(&self) -> Option<Arc<Model>> {
        read(&self.inner.project).project.clone()
    }

    /// A stable identity of this index, shared by its clones. Use it to key
    /// per-index memoized analysis.
    pub fn identity(&self) -> usize {
        Arc::as_ptr(&self.inner) as usize
    }

    /// A counter advanced by every published change.
    pub fn revision(&self) -> u64 {
        self.inner.revision.load(Ordering::SeqCst)
    }

    /// Registers a framework-specific call inference provider.
    pub fn register_type_extension(&self, extension: Arc<dyn Extension>) {
        write(&self.inner.extensions).push(extension);
    }

    /// The revision analyses of this index depend on: the published
    /// semantic snapshot plus every registered type extension (the Symfony
    /// extension answers from the service index). Memoize document analyses
    /// under it, not under the snapshot revision alone.
    pub fn analysis_revision(&self) -> u64 {
        let extensions: u64 = read(&self.inner.extensions)
            .iter()
            .map(|extension| extension.revision())
            .sum();
        self.semantic_snapshot().revision + extensions
    }

    pub(crate) fn type_extensions(&self) -> Vec<Arc<dyn Extension>> {
        let extensions = read(&self.inner.extensions);
        let mut result: Vec<Arc<dyn Extension>> = Vec::with_capacity(extensions.len() + 2);
        result.push(Arc::new(swlsp_php_inference::AttributeContracts));
        result.extend(extensions.iter().cloned());
        result.push(Arc::new(swlsp_php_inference::CallContracts));
        result
    }

    /// Whether `class_name` is `target_name`, extends it, or implements it.
    pub fn is_subtype_of(&self, class_name: &str, target_name: &str) -> bool {
        if class_name.is_empty() || target_name.is_empty() {
            return false;
        }
        self.semantic_snapshot()
            .is_subtype_of(class_name, target_name)
    }

    fn supplemental_paths(&self) -> Option<Arc<SupplementalPaths>> {
        let state = read(&self.inner.project);
        state.project.as_ref()?;
        Some(state.supplemental.clone())
    }

    fn prepare_document(&self, file: &ParsedFile) -> Result<Option<PreparedPhpDocument>> {
        if file.extension() != ".php" {
            return Ok(None);
        }
        let path = file.path();
        self.inner
            .semantic_store
            .ensure_document_details(path)
            .map_err(|err| {
                Error::msg(format!(
                    "pin previous PHP workspace graph for {path}: {err}"
                ))
            })?;
        let document = self.analyze_parsed_file(file);
        let graph = WorkspaceGraph::project_borrowed(&document);
        let storage = encode_workspace_graph(&graph)
            .map_err(|err| Error::msg(format!("encode PHP workspace graph for {path}: {err}")))?;
        let settle = settle::Candidate::of(path, file.content(), &storage, &document);
        let twig_contexts = match file.syntax_tree() {
            Some(tree) => twig_context::extract_twig_template_contexts(
                path,
                &tree.root(),
                tree.source(),
                &document,
            ),
            None => Vec::new(),
        };
        Ok(Some(PreparedPhpDocument {
            graph: Arc::new(Mutex::new(Some(graph))),
            storage,
            twig_contexts,
            settle,
        }))
    }

    fn index_prepared_document(
        &self,
        file: &ParsedFile,
        prepared: Option<&PreparedPhpDocument>,
    ) -> Result<()> {
        if file.extension() != ".php" {
            return Ok(());
        }
        let path = file.path().to_owned();
        let Some(prepared) = prepared else {
            return Err(Error::msg(format!(
                "prepared PHP document is required for {path}"
            )));
        };
        let mutation = file.mutation();
        self.inner.workspace_graphs.batch_save_items_in(
            mutation,
            [(path.as_str(), [(path.as_str(), &prepared.storage)])],
        )?;
        self.inner.twig_contexts.batch_save_items_in(
            mutation,
            [(
                path.as_str(),
                prepared
                    .twig_contexts
                    .iter()
                    .map(|context| (context.template.as_str(), context)),
            )],
        )?;
        let inner = self.inner.clone();
        let graph = prepared.graph.clone();
        let settle = prepared.settle.clone();
        let publish = move || {
            let Some(graph) = lock(&graph).take() else {
                return;
            };
            inner.loader.forget(&path);
            inner.publish_workspace_graph(graph, settle);
            inner.revision.fetch_add(1, Ordering::SeqCst);
        };
        match mutation {
            Some(mutation) => mutation.after_commit(publish),
            None => {
                publish();
                Ok(())
            }
        }
    }

    fn workspace_symbols_for(
        &self,
        file: &ParsedFile,
        prepared: Option<&PreparedPhpDocument>,
    ) -> Result<Vec<WorkspaceSymbol>> {
        if file.extension() != ".php" {
            return Ok(Vec::new());
        }
        let missing = || {
            Error::msg(format!(
                "prepared PHP document is required for {}",
                file.path()
            ))
        };
        let prepared = prepared.ok_or_else(missing)?;
        let graph = lock(&prepared.graph);
        let graph = graph.as_ref().ok_or_else(missing)?;
        Ok(workspace_symbols(file, graph))
    }

    fn ensure_document_details(&self, paths: &[String]) -> Result<()> {
        for path in paths {
            self.inner
                .semantic_store
                .ensure_document_details(path)
                .map_err(|err| {
                    Error::msg(format!(
                        "pin previous PHP workspace graph for {path}: {err}"
                    ))
                })?;
        }
        Ok(())
    }

    /// Releases all resources. The index must not be used afterwards.
    pub fn close(&self) -> Result<()> {
        {
            let mut batch = lock(&self.inner.batch);
            *batch = BatchState::default();
        }
        self.inner.semantic_store.clear();
        self.inner.loader.clear();
        let mut errors = Vec::new();
        if let Err(error) = self.inner.workspace_graphs.close() {
            errors.push(error);
        }
        if let Err(error) = self.inner.twig_contexts.close() {
            errors.push(error);
        }
        if let Some(store) = &self.inner.owned_store
            && let Err(error) = store.close()
        {
            errors.push(error);
        }
        Error::join(errors).map_or(Ok(()), Err)
    }

    fn reset_semantic_store(&self) {
        self.inner.reset_semantic_store(self.project().as_deref());
    }
}

impl Inner {
    fn publish_workspace_graph(
        &self,
        mut graph: WorkspaceGraph,
        settle: Option<settle::Candidate>,
    ) {
        {
            let mut batch = lock(&self.batch);
            if batch.depth > 0 {
                if let Some(detacher) = &mut batch.detacher {
                    detacher.detach_owned(&mut graph);
                }
                batch.pending.insert(graph.path().to_owned(), graph);
                batch.settle.extend(settle);
                return;
            }
        }
        let mut detacher = WorkspaceGraphDetacher::new();
        detacher.detach_owned(&mut graph);
        detacher.finish();
        self.semantic_store
            .replace_canonical_workspace_graphs_owned(vec![graph]);
    }

    fn reset_semantic_store(&self, project: Option<&Model>) {
        self.semantic_store.clear();
        if let Some(model) = project {
            let stub_extensions = model.stub_extensions();
            self.semantic_store
                .replace(&swlsp_php_stubs::document_for_extensions(
                    model.php_version,
                    &stub_extensions,
                    &[&model.disabled_extensions],
                ));
        }
        self.revision.fetch_add(1, Ordering::SeqCst);
    }

    fn remove_published(&self, paths: &[String]) {
        for path in paths {
            self.loader.forget(path);
        }
        let paths: Vec<&str> = paths.iter().map(String::as_str).collect();
        self.semantic_store.remove(&paths);
        self.revision.fetch_add(1, Ordering::SeqCst);
    }
}

fn workspace_symbol_is_container(kind: SymbolKind) -> bool {
    matches!(
        kind,
        SymbolKind::Class | SymbolKind::Interface | SymbolKind::Trait | SymbolKind::Enum
    )
}

fn workspace_symbol_kind(kind: SymbolKind) -> Option<(WorkspaceSymbolKind, i32)> {
    use swlsp_indexer::{
        WORKSPACE_SYMBOL_PRIORITY_PHP_GLOBAL as GLOBAL,
        WORKSPACE_SYMBOL_PRIORITY_PHP_MEMBER as MEMBER, WORKSPACE_SYMBOL_PRIORITY_PHP_TYPE as TYPE,
    };
    Some(match kind {
        SymbolKind::Class => (WorkspaceSymbolKind::CLASS, TYPE),
        SymbolKind::Interface => (WorkspaceSymbolKind::INTERFACE, TYPE),
        SymbolKind::Trait => (WorkspaceSymbolKind::STRUCT, TYPE),
        SymbolKind::Enum => (WorkspaceSymbolKind::ENUM, TYPE),
        SymbolKind::Function => (WorkspaceSymbolKind::FUNCTION, GLOBAL),
        SymbolKind::GlobalConstant => (WorkspaceSymbolKind::CONSTANT, GLOBAL),
        SymbolKind::Method => (WorkspaceSymbolKind::METHOD, MEMBER),
        SymbolKind::Property => (WorkspaceSymbolKind::PROPERTY, MEMBER),
        SymbolKind::ClassConstant => (WorkspaceSymbolKind::CONSTANT, MEMBER),
        SymbolKind::EnumCase => (WorkspaceSymbolKind::ENUM_MEMBER, MEMBER),
        SymbolKind::TypeAlias => (WorkspaceSymbolKind::TYPE_PARAMETER, MEMBER),
        _ => return None,
    })
}

/// Workspace symbols from the compact graph produced by preparation, so
/// catalog population needs no decode from SQLite.
fn workspace_symbols(file: &ParsedFile, graph: &WorkspaceGraph) -> Vec<WorkspaceSymbol> {
    let mut containers: HashMap<String, String> = HashMap::new();
    graph.visit_symbol_views(|view| {
        if workspace_symbol_is_container(view.kind()) {
            let fully_qualified = view.fully_qualified();
            let name = fully_qualified
                .strip_prefix('\\')
                .unwrap_or(fully_qualified);
            let name = if name.is_empty() { view.name() } else { name };
            containers.insert(view.id().to_owned(), name.to_owned());
        }
        true
    });
    let line_index = file.line_index();
    let position = |offset: u32| {
        let (line, character) = line_index.position_utf16(offset);
        WorkspaceSymbolPosition {
            line: line as i32,
            character: character as i32,
        }
    };
    let mut result = Vec::with_capacity(containers.len() * 2);
    graph.visit_symbol_views(|view| {
        let Some((kind, priority)) = workspace_symbol_kind(view.kind()) else {
            return true;
        };
        if view.name().is_empty() {
            return true;
        }
        let mut range = view.selection_range();
        if range == Default::default() {
            range = view.range();
        }
        let fully_qualified = view.fully_qualified();
        let fully_qualified = fully_qualified
            .strip_prefix('\\')
            .unwrap_or(fully_qualified);
        let mut container = containers
            .get(view.container())
            .cloned()
            .unwrap_or_default();
        if container.is_empty()
            && workspace_symbol_is_container(view.kind())
            && let Some(separator) = fully_qualified.rfind('\\')
        {
            container = fully_qualified[..separator].to_owned();
        }
        let aliases = if workspace_symbol_is_container(view.kind())
            || matches!(
                view.kind(),
                SymbolKind::Function | SymbolKind::GlobalConstant
            ) {
            vec![fully_qualified.to_owned()]
        } else {
            Vec::new()
        };
        result.push(WorkspaceSymbol {
            name: view.name().to_owned(),
            container_name: container,
            aliases,
            path: file.path().to_owned(),
            domain: "php".to_owned(),
            kind,
            priority,
            range: WorkspaceSymbolRange {
                start: position(u32::from(range.start())),
                end: position(u32::from(range.end())),
            },
        });
        true
    });
    result
}

fn clean_path(path: &Path) -> String {
    swlsp_php_project::clean(path)
        .to_string_lossy()
        .into_owned()
}

fn clean_str(path: &str) -> String {
    clean_path(Path::new(path))
}

/// The directory of a clean path, `.` when it has none.
fn parent_directory(path: &str) -> String {
    match Path::new(path).parent() {
        Some(parent) if !parent.as_os_str().is_empty() => parent.to_string_lossy().into_owned(),
        _ => ".".to_owned(),
    }
}

/// [`path_within_root`] for already clean paths, without allocating when
/// both are absolute.
fn clean_within(root: &str, path: &str) -> bool {
    let separator = std::path::MAIN_SEPARATOR;
    if root == path
        || (path.len() > root.len()
            && path.starts_with(root)
            && (root.ends_with(separator) || path[root.len()..].starts_with(separator)))
    {
        return true;
    }
    if Path::new(root).is_absolute() && Path::new(path).is_absolute() {
        return false;
    }
    path_within_root(root, path)
}

/// Whether `path` is `root` or below it, comparing cleaned paths.
fn path_within_root(root: &str, path: &str) -> bool {
    let root = clean_str(root);
    let path = clean_str(path);
    if root == path {
        return true;
    }
    let separator = std::path::MAIN_SEPARATOR;
    if path.len() > root.len()
        && path.starts_with(&root)
        && (root.ends_with(separator) || path[root.len()..].starts_with(separator))
    {
        return true;
    }
    let (root_path, path_path) = (PathBuf::from(&root), PathBuf::from(&path));
    if root_path.is_absolute() && path_path.is_absolute() {
        return false;
    }
    if root_path.is_absolute() != path_path.is_absolute() {
        return false;
    }
    // Both relative: `path` is below `root` unless it escapes it with `..`.
    let root_parts: Vec<_> = root_path.components().collect();
    let path_parts: Vec<_> = path_path.components().collect();
    let common = root_parts
        .iter()
        .zip(&path_parts)
        .take_while(|(left, right)| left == right)
        .count();
    let current = |parts: &[std::path::Component<'_>]| {
        parts.len() == 1 && parts[0] == std::path::Component::CurDir
    };
    if current(&root_parts) {
        return !path_parts
            .first()
            .is_some_and(|part| *part == std::path::Component::ParentDir);
    }
    common == root_parts.len()
}

impl Indexer for PhpIndex {
    fn id(&self) -> &str {
        "php.index"
    }

    fn index(&self, file: &ParsedFile) -> Result<()> {
        let prepared = self.prepare_document(file)?;
        self.index_prepared_document(file, prepared.as_ref())
    }

    fn removed_files(&self, paths: &[String]) -> Result<()> {
        self.ensure_document_details(paths)?;
        let errors: Vec<Error> = [
            self.inner
                .workspace_graphs
                .batch_delete_by_file_paths(paths),
            self.inner.twig_contexts.batch_delete_by_file_paths(paths),
        ]
        .into_iter()
        .filter_map(Result::err)
        .collect();
        if let Some(error) = Error::join(errors) {
            return Err(error);
        }
        self.inner.remove_published(paths);
        Ok(())
    }

    fn close(&self) -> Result<()> {
        PhpIndex::close(self)
    }

    fn clear(&self) -> Result<()> {
        let errors: Vec<Error> = [
            self.inner.workspace_graphs.clear(),
            self.inner.twig_contexts.clear(),
        ]
        .into_iter()
        .filter_map(Result::err)
        .collect();
        if let Some(error) = Error::join(errors) {
            return Err(error);
        }
        self.inner.loader.clear();
        self.reset_semantic_store();
        Ok(())
    }

    fn as_preparing(&self) -> Option<&dyn PreparingIndexer> {
        Some(self)
    }

    fn as_supplemental_path(&self) -> Option<&dyn SupplementalPathIndexer> {
        Some(self)
    }

    fn as_supplemental_syntax(&self) -> Option<&dyn SupplementalSyntaxIndexer> {
        Some(self)
    }

    fn as_transactional_remover(&self) -> Option<&dyn TransactionalRemover> {
        Some(self)
    }

    fn as_transactional_clearer(&self) -> Option<&dyn TransactionalClearer> {
        Some(self)
    }

    fn as_batch(&self) -> Option<&dyn BatchIndexer> {
        Some(self)
    }

    fn as_workspace_symbol_contributor(&self) -> Option<&dyn WorkspaceSymbolContributor> {
        Some(self)
    }
}

impl PreparingIndexer for PhpIndex {
    fn prepare(&self, file: &ParsedFile) -> Result<Option<Prepared>> {
        Ok(self
            .prepare_document(file)?
            .map(|prepared| Box::new(prepared) as Prepared))
    }

    fn index_prepared(&self, file: &ParsedFile, prepared: Option<&Prepared>) -> Result<()> {
        let prepared = prepared.and_then(|value| value.downcast_ref::<PreparedPhpDocument>());
        self.index_prepared_document(file, prepared)
    }
}

impl WorkspaceSymbolContributor for PhpIndex {
    fn workspace_symbols(
        &self,
        file: &ParsedFile,
        prepared: Option<&Prepared>,
    ) -> Result<Vec<WorkspaceSymbol>> {
        let prepared = prepared.and_then(|value| value.downcast_ref::<PreparedPhpDocument>());
        self.workspace_symbols_for(file, prepared)
    }
}

impl SupplementalPathIndexer for PhpIndex {
    /// Selectively reopens Composer source roots inside globally skipped
    /// directories such as `tests/`.
    fn should_enter_directory(&self, path: &str) -> bool {
        let Some(supplemental) = self.supplemental_paths() else {
            return false;
        };
        let path = clean_str(path);
        let overlaps = |root: &String| clean_within(root, &path) || clean_within(&path, root);
        supplemental.roots.iter().any(overlaps)
            || supplemental.file_directories.iter().any(overlaps)
    }

    fn should_index_path(&self, path: &str) -> bool {
        if !is_php_path(path) {
            return false;
        }
        let Some(supplemental) = self.supplemental_paths() else {
            return false;
        };
        let path = clean_str(path);
        supplemental
            .roots
            .iter()
            .any(|root| clean_within(root, &path))
            || supplemental.files.contains(&path)
    }
}

impl SupplementalSyntaxIndexer for PhpIndex {
    fn should_preparse_path(&self, path: &str) -> bool {
        SupplementalPathIndexer::should_index_path(self, path)
    }
}

impl TransactionalRemover for PhpIndex {
    fn removed_files_in(&self, paths: &[String], mutation: &Mutation) -> Result<()> {
        self.ensure_document_details(paths)?;
        let errors: Vec<Error> = [
            self.inner
                .workspace_graphs
                .batch_delete_by_file_paths_in(Some(mutation), paths),
            self.inner
                .twig_contexts
                .batch_delete_by_file_paths_in(Some(mutation), paths),
        ]
        .into_iter()
        .filter_map(Result::err)
        .collect();
        if let Some(error) = Error::join(errors) {
            return Err(error);
        }
        let inner = self.inner.clone();
        let paths = paths.to_vec();
        mutation.after_commit(move || inner.remove_published(&paths))
    }
}

impl TransactionalClearer for PhpIndex {
    fn clear_in(&self, mutation: &Mutation) -> Result<()> {
        let errors: Vec<Error> = [
            self.inner.workspace_graphs.clear_in(Some(mutation)),
            self.inner.twig_contexts.clear_in(Some(mutation)),
        ]
        .into_iter()
        .filter_map(Result::err)
        .collect();
        if let Some(error) = Error::join(errors) {
            return Err(error);
        }
        let inner = self.inner.clone();
        let project = self.project();
        mutation.after_commit(move || {
            inner.loader.clear();
            inner.reset_semantic_store(project.as_deref());
        })
    }
}

impl BatchIndexer for PhpIndex {
    /// Defers immutable workspace publication until all file transactions of
    /// the scanner run have committed or rolled back.
    fn begin_indexing_batch(&self, candidate_files: &[String]) {
        let mut batch = lock(&self.inner.batch);
        if batch.depth == 0 {
            let php_files = candidate_files
                .iter()
                .filter(|path| is_php_path(path))
                .count();
            batch.pending = HashMap::new();
            batch.settle.clear();
            batch.settling.clear();
            batch.detacher = Some(WorkspaceGraphDetacher::with_capacity(
                php_files * WORKSPACE_RESTORE_STRINGS_PER_DOCUMENT,
            ));
        }
        batch.depth += 1;
    }

    /// Publishes every committed PHP document of the batch in one
    /// generation. Nested batches are supported.
    fn settle_indexing_batch(&self, cancel: &CancelToken, workers: usize) -> Result<()> {
        let candidates = std::mem::take(&mut lock(&self.inner.batch).settling);
        settle::settle(self, candidates, cancel, workers)
    }

    fn end_indexing_batch(&self) -> Result<()> {
        let graphs: Vec<WorkspaceGraph> = {
            let mut batch = lock(&self.inner.batch);
            if batch.depth == 0 {
                return Err(Error::msg("PHP indexing batch is not active"));
            }
            batch.depth -= 1;
            if batch.depth > 0 {
                return Ok(());
            }
            if let Some(detacher) = batch.detacher.take() {
                detacher.finish();
            }
            let settle = std::mem::take(&mut batch.settle);
            // One changed file was already analyzed against everything else.
            batch.settling = if batch.pending.len() > 1 {
                settle
            } else {
                Vec::new()
            };
            std::mem::take(&mut batch.pending).into_values().collect()
        };
        // Compression is frequent during a scanner batch but rare while the
        // server is idle.
        persistence::release_codec_pool();
        if !graphs.is_empty() {
            self.inner
                .semantic_store
                .replace_canonical_workspace_graphs_owned(graphs);
        }
        Ok(())
    }
}

/// Parses a PHP file and returns its CST debug tree and parse errors.
pub fn debug_ast(path: &Path) -> std::io::Result<(String, Vec<String>)> {
    let content = std::fs::read(path)?;
    let source = String::from_utf8_lossy(&content);
    let parsed = swlsp_parser_php::parse(&source);
    Ok((
        swlsp_syntax::debug_tree(&parsed.root()),
        parsed.errors.iter().map(ToString::to_string).collect(),
    ))
}
