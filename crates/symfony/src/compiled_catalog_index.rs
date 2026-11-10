//! Feeds generated container and route metadata through the workspace
//! scanner, including transactional publication and deletion. Only selected
//! cache files are admitted; cache pools and generated container classes
//! remain excluded.

use std::borrow::Cow;
use std::collections::HashMap;
use std::path::Path;
use std::sync::{Arc, Mutex, PoisonError};

use serde::{Deserialize, Serialize};
use swlsp_indexer::{
    DataIndexer, Indexer, Mutation, ParsedFile, Prepared, PreparingIndexer, Result, Store,
    SupplementalPathIndexer, SupplementalSyntaxIndexer, TransactionalClearer, TransactionalRemover,
};

use crate::compiled_route_catalog::{
    CompiledRouteCatalog, is_compiled_route_file_name, parse_compiled_routes_tree,
};
use crate::container_catalog::{ContainerCatalog, is_compiled_container_file_name};
use crate::model::{ContainerTwigComponent, ContainerTwigGlobal, Parameter, Route, Service};
use crate::twig_components::parse_xml_twig_components_tree;
use crate::twig_globals::parse_xml_twig_globals_tree;
use crate::util::{base_name, clean_path, dir_path, join_path};
use crate::xml_services::parse_xml_services_tree;

/// One persisted compiled catalog file.
#[derive(Clone, Debug, Default, PartialEq, Eq, Serialize, Deserialize)]
#[serde(default)]
pub struct CompiledCatalogRecord {
    pub path: String,
    pub modified: i64,
    pub services: Vec<Service>,
    pub parameters: Vec<Parameter>,
    pub globals: Vec<ContainerTwigGlobal>,
    pub components: Vec<ContainerTwigComponent>,
    pub routes: Vec<Route>,
}

enum Target {
    Container(Arc<ContainerCatalog>),
    Routes(Arc<CompiledRouteCatalog>),
}

struct Shared {
    target: Target,
    records: Mutex<HashMap<String, CompiledCatalogRecord>>,
}

pub struct CompiledCatalogIndex {
    /// `<root>/var` and `<root>/app`, cleaned.
    bases: [String; 2],
    /// `<root>/var/cache` and `<root>/app/cache`, cleaned. Precomputed: the
    /// scanner asks about every workspace path that no language claims.
    caches: [String; 2],
    id: String,
    repository: DataIndexer<CompiledCatalogRecord>,
    shared: Arc<Shared>,
}

impl CompiledCatalogIndex {
    /// The compiled container index publishing into `catalog`.
    pub fn new_container(
        root: &str,
        cache: impl AsRef<Path>,
        catalog: Arc<ContainerCatalog>,
        store: Option<&Store>,
    ) -> Result<Self> {
        Self::new(
            root,
            cache,
            "symfony.compiled_container",
            Target::Container(catalog),
            store,
        )
    }

    /// The compiled route index publishing into `catalog`.
    pub fn new_routes(
        root: &str,
        cache: impl AsRef<Path>,
        catalog: Arc<CompiledRouteCatalog>,
        store: Option<&Store>,
    ) -> Result<Self> {
        Self::new(
            root,
            cache,
            "symfony.compiled_routes",
            Target::Routes(catalog),
            store,
        )
    }

    fn new(
        root: &str,
        cache: impl AsRef<Path>,
        id: &str,
        target: Target,
        store: Option<&Store>,
    ) -> Result<Self> {
        let repository = DataIndexer::repository(cache.as_ref().join("compiled.db"), id, store)?;
        let values: Vec<CompiledCatalogRecord> = match repository.get_all_values() {
            Ok(values) => values,
            Err(error) => {
                let _ = repository.close();
                return Err(error);
            }
        };
        let records: HashMap<String, CompiledCatalogRecord> = values
            .into_iter()
            .map(|record| (record.path.clone(), record))
            .collect();
        let shared = Arc::new(Shared {
            target,
            records: Mutex::new(records),
        });
        shared.publish(
            &shared
                .records
                .lock()
                .unwrap_or_else(PoisonError::into_inner),
        );
        Ok(Self {
            bases: ["var", "app"].map(|parent| join_path(&[root, parent])),
            caches: ["var", "app"].map(|parent| join_path(&[root, parent, "cache"])),
            id: id.to_owned(),
            repository,
            shared,
        })
    }

    fn is_container(&self) -> bool {
        matches!(self.shared.target, Target::Container(_))
    }

    fn is_cache(&self, path: &str) -> bool {
        self.caches.iter().any(|cache| cache == path)
    }

    /// `var`, `app`, their `cache` directories and the directories directly
    /// below those caches.
    fn should_enter(&self, path: &str) -> bool {
        let path = clean_cow(path);
        self.bases.iter().any(|base| *base == path)
            || self.is_cache(&path)
            || self.is_cache(&dir_path(&path))
    }

    /// Compiled catalogs live in a directory directly below a cache
    /// (`var/cache/<env>/<file>`).
    fn should_index(&self, path: &str) -> bool {
        let path = clean_cow(path);
        let Some(directory) = parent(&path) else {
            return false;
        };
        if !parent(directory).is_some_and(|cache| self.is_cache(cache)) {
            return false;
        }
        let path = path.as_ref();
        if self.is_container() {
            return is_compiled_container_file_name(base_name(path));
        }
        is_compiled_route_file_name(base_name(path))
    }

    fn prepare_record(&self, file: &ParsedFile) -> Option<CompiledCatalogRecord> {
        let path = file.path();
        if !self.should_index(path) {
            return None;
        }
        let mut record = CompiledCatalogRecord {
            path: path.to_owned(),
            ..CompiledCatalogRecord::default()
        };
        if let Ok(metadata) = std::fs::metadata(path)
            && let Ok(modified) = metadata.modified()
        {
            record.modified = match modified.duration_since(std::time::UNIX_EPOCH) {
                Ok(duration) => duration.as_nanos() as i64,
                Err(error) => -(error.duration().as_nanos() as i64),
            };
        }
        if !self.is_container() {
            record.routes = parse_compiled_routes_tree(path, file.syntax_tree());
            return Some(record);
        }
        let tree = file.syntax_tree()?;
        let (services, parameters) = parse_xml_services_tree(path, Some(tree), file.line_index());
        record.services = services;
        record.parameters = parameters;
        record.globals = parse_xml_twig_globals_tree(path, Some(tree));
        record.components = parse_xml_twig_components_tree(path, Some(tree));
        Some(record)
    }

    fn after(
        &self,
        mutation: Option<&Mutation>,
        update: impl FnOnce(&mut HashMap<String, CompiledCatalogRecord>) + Send + 'static,
    ) -> Result<()> {
        let shared = self.shared.clone();
        let publish = move || {
            let mut records = shared
                .records
                .lock()
                .unwrap_or_else(PoisonError::into_inner);
            update(&mut records);
            shared.publish(&records);
        };
        match mutation {
            Some(mutation) => mutation.after_commit(publish),
            None => {
                publish();
                Ok(())
            }
        }
    }
}

impl Shared {
    fn publish(&self, records: &HashMap<String, CompiledCatalogRecord>) {
        let mut sorted: Vec<&CompiledCatalogRecord> = records.values().collect();
        let dev =
            |record: &CompiledCatalogRecord| base_name(&dir_path(&record.path)).starts_with("dev");
        match &self.target {
            // A development container, then the newest one: a project may
            // only have compiled its test or static-analysis kernel.
            Target::Container(_) => sorted.sort_by(|a, b| {
                dev(b)
                    .cmp(&dev(a))
                    .then_with(|| b.modified.cmp(&a.modified))
                    .then_with(|| a.path.cmp(&b.path))
            }),
            Target::Routes(_) => sorted.sort_by(|a, b| {
                let modern = |record: &CompiledCatalogRecord| {
                    base_name(&record.path) == "url_generating_routes.php"
                };
                dev(b)
                    .cmp(&dev(a))
                    .then_with(|| modern(b).cmp(&modern(a)))
                    .then_with(|| b.modified.cmp(&a.modified))
                    .then_with(|| a.path.cmp(&b.path))
            }),
        }
        let empty = CompiledCatalogRecord::default();
        let record = sorted.first().copied().unwrap_or(&empty);
        match &self.target {
            Target::Routes(catalog) => catalog.publish(&record.path, record.routes.clone()),
            Target::Container(catalog) => {
                let mut state = catalog.write();
                state.container_path = record.path.clone();
                state.container_exists = !record.path.is_empty();
                state.services = record
                    .services
                    .iter()
                    .map(|service| (service.id.clone(), service.clone()))
                    .collect();
                state.parameters = record
                    .parameters
                    .iter()
                    .map(|parameter| (parameter.name.clone(), parameter.clone()))
                    .collect();
                state.twig_globals = record.globals.clone();
                state.twig_components = record.components.clone();
                state.revision += 1;
            }
        }
    }
}

impl Indexer for CompiledCatalogIndex {
    fn id(&self) -> &str {
        &self.id
    }

    fn index(&self, file: &ParsedFile) -> Result<()> {
        let prepared = self.prepare(file)?;
        self.index_prepared(file, prepared.as_ref())
    }

    fn removed_files(&self, paths: &[String]) -> Result<()> {
        self.remove(paths, None)
    }

    fn close(&self) -> Result<()> {
        self.repository.close()
    }

    fn clear(&self) -> Result<()> {
        self.clear_records(None)
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
}

impl CompiledCatalogIndex {
    fn remove(&self, paths: &[String], mutation: Option<&Mutation>) -> Result<()> {
        self.repository
            .batch_delete_by_file_paths_in(mutation, paths)?;
        let paths = paths.to_vec();
        self.after(mutation, move |records| {
            for path in &paths {
                records.remove(path);
            }
        })
    }

    fn clear_records(&self, mutation: Option<&Mutation>) -> Result<()> {
        self.repository.clear_in(mutation)?;
        self.after(mutation, |records| records.clear())
    }
}

impl PreparingIndexer for CompiledCatalogIndex {
    fn prepare(&self, file: &ParsedFile) -> Result<Option<Prepared>> {
        Ok(self
            .prepare_record(file)
            .map(|record| Box::new(record) as Prepared))
    }

    fn index_prepared(&self, file: &ParsedFile, prepared: Option<&Prepared>) -> Result<()> {
        let Some(record) = prepared.and_then(|value| value.downcast_ref::<CompiledCatalogRecord>())
        else {
            return Ok(());
        };
        let path = file.path().to_owned();
        self.repository.batch_save_items_in(
            file.mutation(),
            [(path.as_str(), [(path.as_str(), record)])],
        )?;
        let record = record.clone();
        self.after(file.mutation(), move |records| {
            records.insert(path, record);
        })
    }
}

impl SupplementalPathIndexer for CompiledCatalogIndex {
    fn should_enter_directory(&self, path: &str) -> bool {
        self.should_enter(path)
    }

    fn should_index_path(&self, path: &str) -> bool {
        self.should_index(path)
    }
}

impl SupplementalSyntaxIndexer for CompiledCatalogIndex {
    fn should_preparse_path(&self, path: &str) -> bool {
        self.should_index(path)
    }
}

impl TransactionalRemover for CompiledCatalogIndex {
    fn removed_files_in(&self, paths: &[String], mutation: &Mutation) -> Result<()> {
        self.remove(paths, Some(mutation))
    }
}

impl TransactionalClearer for CompiledCatalogIndex {
    fn clear_in(&self, mutation: &Mutation) -> Result<()> {
        self.clear_records(Some(mutation))
    }
}

/// `path` cleaned, borrowed when already clean (scanner paths are).
fn clean_cow(path: &str) -> Cow<'_, str> {
    let clean = path.starts_with('/')
        && (path.len() == 1 || !path.ends_with('/'))
        && !path
            .split('/')
            .skip(1)
            .any(|part| part.is_empty() || part == "." || part == "..");
    if clean || path == "/" {
        Cow::Borrowed(path)
    } else {
        Cow::Owned(clean_path(path))
    }
}

/// The parent of a clean absolute path, without allocating.
fn parent(path: &str) -> Option<&str> {
    match path.rfind('/') {
        Some(0) if path.len() > 1 => Some("/"),
        Some(0) | None => None,
        Some(index) => Some(&path[..index]),
    }
}
