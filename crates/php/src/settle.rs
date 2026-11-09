//! Settling return types inferred during a bulk indexing run.
//!
//! A scanner run analyzes every file against the generation published
//! before the run and publishes the whole run at the end. Declarations
//! without a declared return type get one inferred from their `return`
//! statements, which can depend on other files: during a cold index those
//! files are not visible yet, while a later single-file reindex sees all of
//! them. Without settling, the persisted type of a file depended on how it
//! was last indexed.
//!
//! After a run has published, its files with inferred returns are analyzed
//! again against the complete generation; changed graphs are persisted and
//! published. Further passes follow chains of inferred functions across
//! files: they revisit only files calling a function or method whose
//! inferred return type the previous pass changed (matched by name), up to
//! [`MAXIMUM_PASSES`].

use std::collections::HashSet;
use std::collections::hash_map::DefaultHasher;
use std::hash::{Hash, Hasher};
use std::sync::Mutex;
use std::sync::atomic::{AtomicUsize, Ordering};

use swlsp_indexer::{CancelToken, Error, Result};
use swlsp_php_semantic::{Document, Flags, NameKind, WorkspaceGraph, WorkspaceGraphDetacher};
use swlsp_php_types::go_to_lower;

use crate::persistence::{PersistedWorkspaceGraph, encode_workspace_graph};
use crate::twig_context::{TwigTemplateContext, extract_twig_template_contexts};
use crate::{PhpIndex, lock};

/// Re-analysis passes after a run. The first settles dependencies on other
/// files of the run; later ones follow chains of inferred functions.
const MAXIMUM_PASSES: usize = 4;

/// A committed file whose graph holds inferred return types.
#[derive(Clone)]
pub(crate) struct Candidate {
    path: String,
    /// The analyzed content, so a file changed since is left to the watcher.
    content: u64,
    /// The persisted graph, to detect whether settling changed it.
    storage: u64,
    /// Hashed short names of the functions and methods the file calls.
    calls: Box<[u64]>,
    inferred: InferredReturns,
}

impl Candidate {
    /// The candidate of an analyzed file, when it has inferred return types.
    pub(crate) fn of(
        path: &str,
        content: &[u8],
        storage: &PersistedWorkspaceGraph,
        document: &Document,
    ) -> Option<Self> {
        let inferred = InferredReturns::of(document);
        if inferred.0.is_empty() {
            return None;
        }
        let mut calls: Vec<u64> = document
            .references
            .iter()
            .filter(|reference| matches!(reference.kind, NameKind::Function | NameKind::Member))
            .map(|reference| name_key(&reference.name))
            .collect();
        calls.sort_unstable();
        calls.dedup();
        Some(Candidate {
            path: path.to_owned(),
            content: hash(content),
            storage: storage_hash(storage),
            calls: calls.into_boxed_slice(),
            inferred,
        })
    }
}

/// Hashed short names and return types of the declarations of one file
/// whose return types were inferred, sorted.
#[derive(Clone, Default)]
struct InferredReturns(Box<[(u64, u64)]>);

impl InferredReturns {
    fn of(document: &Document) -> Self {
        let mut inferred: Vec<(u64, u64)> = document
            .symbols
            .iter()
            .filter(|symbol| symbol.flags.has(Flags::INFERRED_RETURN))
            .map(|symbol| {
                (
                    name_key(&symbol.name),
                    hash(symbol.return_type.to_string().as_bytes()),
                )
            })
            .collect();
        inferred.sort_unstable();
        inferred.dedup();
        InferredReturns(inferred.into_boxed_slice())
    }

    /// Adds the names whose inferred return type differs from `other`.
    fn changed_names(&self, other: &InferredReturns, changed: &mut HashSet<u64>) {
        for (left, right) in [(self, other), (other, self)] {
            for pair in left.0.iter() {
                if right.0.binary_search(pair).is_err() {
                    changed.insert(pair.0);
                }
            }
        }
    }
}

/// A candidate whose graph changed when analyzed again.
struct Settled {
    candidate: usize,
    graph: WorkspaceGraph,
    storage: PersistedWorkspaceGraph,
    twig_contexts: Vec<TwigTemplateContext>,
    inferred: InferredReturns,
}

pub(crate) fn settle(
    index: &PhpIndex,
    mut candidates: Vec<Candidate>,
    cancel: &CancelToken,
    workers: usize,
) -> Result<()> {
    let mut scheduled: Vec<usize> = (0..candidates.len()).collect();
    for _ in 0..MAXIMUM_PASSES {
        if scheduled.is_empty() {
            break;
        }
        cancel.check()?;
        let settled = reanalyze(index, &candidates, &scheduled, cancel, workers)?;
        if settled.is_empty() {
            break;
        }
        let mut changed = HashSet::new();
        for item in &settled {
            let candidate = &mut candidates[item.candidate];
            candidate.storage = storage_hash(&item.storage);
            candidate
                .inferred
                .changed_names(&item.inferred, &mut changed);
            candidate.inferred = item.inferred.clone();
        }
        persist_and_publish(index, &candidates, settled)?;
        scheduled = (0..candidates.len())
            .filter(|&position| {
                candidates[position]
                    .calls
                    .iter()
                    .any(|name| changed.contains(name))
            })
            .collect();
    }
    Ok(())
}

/// Analyzes the `scheduled` candidates against the current generation and
/// returns the ones whose graph changed, in candidate order.
fn reanalyze(
    index: &PhpIndex,
    candidates: &[Candidate],
    scheduled: &[usize],
    cancel: &CancelToken,
    workers: usize,
) -> Result<Vec<Settled>> {
    let next = AtomicUsize::new(0);
    let settled = Mutex::new(Vec::new());
    let errors = Mutex::new(Vec::new());
    std::thread::scope(|scope| {
        for _ in 0..workers.clamp(1, scheduled.len()) {
            scope.spawn(|| {
                while !cancel.is_cancelled() {
                    let Some(&position) = scheduled.get(next.fetch_add(1, Ordering::Relaxed))
                    else {
                        return;
                    };
                    match reanalyze_one(index, &candidates[position], position) {
                        Ok(Some(item)) => lock(&settled).push(item),
                        Ok(None) => {}
                        Err(error) => lock(&errors).push(error),
                    }
                }
            });
        }
    });
    cancel.check()?;
    let errors = errors
        .into_inner()
        .unwrap_or_else(|poison| poison.into_inner());
    if let Some(error) = Error::join(errors) {
        return Err(error);
    }
    let mut settled = settled
        .into_inner()
        .unwrap_or_else(|poison| poison.into_inner());
    settled.sort_by_key(|item| item.candidate);
    Ok(settled)
}

fn reanalyze_one(
    index: &PhpIndex,
    candidate: &Candidate,
    position: usize,
) -> Result<Option<Settled>> {
    let path = candidate.path.as_str();
    // A file removed or changed since the run is left to the watcher.
    let Ok(content) = std::fs::read(path) else {
        return Ok(None);
    };
    if hash(&content) != candidate.content {
        return Ok(None);
    }
    let source = String::from_utf8_lossy(&content);
    let parsed = swlsp_parser_php::parse(&source);
    let root = parsed.root();
    let document = index.analyze_against(&index.indexing_snapshot(), path, 0, &root, &source);
    let graph = WorkspaceGraph::project_borrowed(&document);
    let storage = encode_workspace_graph(&graph).map_err(|err| {
        Error::msg(format!(
            "encode settled PHP workspace graph for {path}: {err}"
        ))
    })?;
    if storage_hash(&storage) == candidate.storage {
        return Ok(None);
    }
    Ok(Some(Settled {
        candidate: position,
        twig_contexts: extract_twig_template_contexts(path, &root, &source, &document),
        inferred: InferredReturns::of(&document),
        graph,
        storage,
    }))
}

fn persist_and_publish(
    index: &PhpIndex,
    candidates: &[Candidate],
    settled: Vec<Settled>,
) -> Result<()> {
    let inner = &index.inner;
    let path = |item: &Settled| candidates[item.candidate].path.as_str();
    for item in &settled {
        let path = path(item);
        inner
            .semantic_store
            .ensure_document_details(path)
            .map_err(|err| {
                Error::msg(format!(
                    "pin previous PHP workspace graph for {path}: {err}"
                ))
            })?;
    }
    inner.workspace_graphs.batch_save_items_in(
        None,
        settled
            .iter()
            .map(|item| (path(item), [(path(item), &item.storage)])),
    )?;
    inner.twig_contexts.batch_save_items_in(
        None,
        settled.iter().map(|item| {
            (
                path(item),
                item.twig_contexts
                    .iter()
                    .map(|context| (context.template.as_str(), context)),
            )
        }),
    )?;
    let mut detacher = WorkspaceGraphDetacher::new();
    let graphs: Vec<WorkspaceGraph> = settled
        .into_iter()
        .map(|mut item| {
            inner.loader.forget(path(&item));
            detacher.detach_owned(&mut item.graph);
            item.graph
        })
        .collect();
    detacher.finish();
    inner
        .semantic_store
        .replace_canonical_workspace_graphs_owned(graphs);
    inner.revision.fetch_add(1, Ordering::SeqCst);
    Ok(())
}

/// The case-insensitive unqualified name, as functions and methods resolve.
fn name_key(name: &str) -> u64 {
    let name = name.rsplit('\\').next().unwrap_or(name);
    hash(go_to_lower(name).as_bytes())
}

fn storage_hash(storage: &PersistedWorkspaceGraph) -> u64 {
    let mut hasher = DefaultHasher::new();
    storage.format.hash(&mut hasher);
    storage.payload.0.hash(&mut hasher);
    hasher.finish()
}

fn hash(content: &[u8]) -> u64 {
    let mut hasher = DefaultHasher::new();
    content.hash(&mut hasher);
    hasher.finish()
}
