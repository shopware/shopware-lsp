//! DBAL type registration persistence and queries (port of Go `index.go`).

use std::collections::HashSet;
use std::path::Path;
use std::sync::{Arc, Mutex, PoisonError, RwLock};

use swlsp_indexer::{
    DataIndexer, Error, Indexer, Mutation, ParsedFile, Prepared, PreparingIndexer, Result, Store,
    TransactionalClearer, TransactionalRemover,
};
use swlsp_php::PhpIndex;
use swlsp_php_types::{equal_fold, go_to_lower};

use crate::candidate::{TYPE_CANDIDATE, doctrine_candidates};
use crate::helpers::{go_trim_space, normalize_class};
use crate::type_config::{TypeRegistration, type_registrations_in_document};
use crate::types::{TypeDeclaration, sort_declarations, type_declarations};

type PreparedRegistrations = Vec<(String, TypeRegistration)>;

#[derive(Default)]
struct TypeCache {
    revision: u64,
    built_at: u64,
    generation: u64,
    types: Option<Vec<TypeDeclaration>>,
}

/// Persists custom DBAL type registrations and queries the shared Shopware
/// DAL index for physical table and column metadata.
pub struct Index {
    type_registrations: DataIndexer<TypeRegistration>,
    pub(crate) dal: RwLock<Option<Arc<swlsp_shopware_dal::Index>>>,
    paths: Arc<RwLock<HashSet<String>>>,
    type_cache: Arc<Mutex<TypeCache>>,
}

impl Index {
    pub fn new(cache: impl AsRef<Path>, store: Option<&Store>) -> Result<Self> {
        let type_registrations = DataIndexer::repository(
            cache.as_ref().join("doctrine_types.db"),
            "symfony.doctrine.type_registrations",
            store,
        )?;
        let paths = match type_registrations.get_all_file_paths() {
            Ok(paths) => paths,
            Err(error) => {
                let _ = type_registrations.close();
                return Err(error);
            }
        };
        Ok(Self {
            type_registrations,
            dal: RwLock::new(None),
            paths: Arc::new(RwLock::new(paths.into_iter().collect())),
            type_cache: Arc::new(Mutex::new(TypeCache::default())),
        })
    }

    pub fn set_dal_index(&self, schema: Arc<swlsp_shopware_dal::Index>) {
        *self.dal.write().unwrap_or_else(PoisonError::into_inner) = Some(schema);
    }

    pub(crate) fn dal(&self) -> Option<Arc<swlsp_shopware_dal::Index>> {
        self.dal
            .read()
            .unwrap_or_else(PoisonError::into_inner)
            .clone()
    }

    fn had_types(&self, path: &str) -> bool {
        self.paths
            .read()
            .unwrap_or_else(PoisonError::into_inner)
            .contains(path)
    }

    fn prepare_registrations(&self, file: &ParsedFile) -> Option<PreparedRegistrations> {
        if doctrine_candidates(file) & TYPE_CANDIDATE == 0 && !self.had_types(file.path()) {
            return None;
        }
        let mut values: PreparedRegistrations = Vec::new();
        if let Some(tree) = file.syntax_tree() {
            let registrations = type_registrations_in_document(file.path(), &tree.root());
            for (position, registration) in registrations.into_iter().enumerate() {
                if registration.name.is_empty() || registration.class.is_empty() {
                    continue;
                }
                values.push((
                    format!("{}#{position}", go_to_lower(&registration.name)),
                    registration,
                ));
            }
        }
        Some(values)
    }

    fn save(&self, file: &ParsedFile, values: &PreparedRegistrations) -> Result<()> {
        self.type_registrations.batch_save_items_in(
            file.mutation(),
            [(
                file.path(),
                values.iter().map(|(key, value)| (key.as_str(), value)),
            )],
        )?;
        let paths = self.paths.clone();
        let path = file.path().to_owned();
        let present = !values.is_empty();
        self.publish(file.mutation(), move || {
            let mut paths = paths.write().unwrap_or_else(PoisonError::into_inner);
            if present {
                paths.insert(path);
            } else {
                paths.remove(&path);
            }
        })
    }

    /// Applies `change` and invalidates the merged type cache, after commit
    /// when a mutation is active.
    fn publish(
        &self,
        mutation: Option<&Mutation>,
        change: impl FnOnce() + Send + 'static,
    ) -> Result<()> {
        let cache = self.type_cache.clone();
        let publish = move || {
            change();
            let mut cache = cache.lock().unwrap_or_else(PoisonError::into_inner);
            cache.generation += 1;
            cache.types = None;
        };
        match mutation {
            Some(mutation) => mutation.after_commit(publish),
            None => {
                publish();
                Ok(())
            }
        }
    }

    fn remove_paths(&self, paths: &[String], mutation: Option<&Mutation>) -> Result<()> {
        self.type_registrations
            .batch_delete_by_file_paths_in(mutation, paths)?;
        let published = self.paths.clone();
        let paths = paths.to_vec();
        self.publish(mutation, move || {
            let mut published = published.write().unwrap_or_else(PoisonError::into_inner);
            for path in &paths {
                published.remove(path);
            }
        })
    }

    fn clear_all(&self, mutation: Option<&Mutation>) -> Result<()> {
        self.type_registrations.clear_in(mutation)?;
        let published = self.paths.clone();
        self.publish(mutation, move || {
            published
                .write()
                .unwrap_or_else(PoisonError::into_inner)
                .clear();
        })
    }

    /// Conventional DBAL type classes merged with configured registrations,
    /// sorted by name and class. Cached per PHP snapshot revision and
    /// registration generation.
    pub fn type_declarations(&self, php_index: &PhpIndex) -> Vec<TypeDeclaration> {
        let revision = php_index.semantic_snapshot().revision;
        let mut cache = self
            .type_cache
            .lock()
            .unwrap_or_else(PoisonError::into_inner);
        if let Some(types) = &cache.types
            && cache.revision == revision
            && cache.built_at == cache.generation
        {
            return types.clone();
        }
        let mut result = type_declarations(php_index);
        if let Ok(registrations) = self.type_registrations.get_all_values_view() {
            let mut seen: HashSet<String> = result.iter().map(type_declaration_key).collect();
            for registration in registrations.iter() {
                let mut declaration = TypeDeclaration {
                    name: registration.name.clone(),
                    class: registration.class.clone(),
                    file: registration.file.clone(),
                    range: registration.class_range,
                };
                if let Some(symbol) = php_index.find_class(&registration.class) {
                    declaration.file = symbol.path.to_string();
                    declaration.range = symbol.selection_range;
                }
                if !seen.insert(type_declaration_key(&declaration)) {
                    continue;
                }
                result.push(declaration);
            }
            sort_declarations(&mut result);
        }
        cache.types = Some(result.clone());
        cache.revision = revision;
        cache.built_at = cache.generation;
        result
    }

    /// Registrations named `name` (case-insensitive), by file and position.
    pub fn type_registrations(&self, name: &str) -> Result<Vec<TypeRegistration>> {
        if go_trim_space(name).is_empty() {
            return Ok(Vec::new());
        }
        let values = self.type_registrations.get_all_values_view()?;
        let mut result: Vec<TypeRegistration> = values
            .iter()
            .filter(|registration| equal_fold(&registration.name, name))
            .cloned()
            .collect();
        result.sort_by(|left, right| {
            left.file
                .cmp(&right.file)
                .then_with(|| left.name_range.start().cmp(&right.name_range.start()))
        });
        Ok(result)
    }

    /// Every persisted registration (diagnostics and oracle use).
    pub fn all_type_registrations(&self) -> Result<Vec<TypeRegistration>> {
        self.type_registrations.get_all_values()
    }
}

fn type_declaration_key(declaration: &TypeDeclaration) -> String {
    go_to_lower(&format!(
        "{}|{}",
        go_trim_space(&declaration.name),
        normalize_class(&declaration.class)
    ))
}

impl Indexer for Index {
    fn id(&self) -> &str {
        "symfony.doctrine"
    }

    fn index(&self, file: &ParsedFile) -> Result<()> {
        match self.prepare_registrations(file) {
            Some(values) => self.save(file, &values),
            None => Ok(()),
        }
    }

    fn removed_files(&self, paths: &[String]) -> Result<()> {
        self.remove_paths(paths, None)
    }

    fn close(&self) -> Result<()> {
        self.type_registrations.close()
    }

    fn clear(&self) -> Result<()> {
        self.clear_all(None)
    }

    fn as_preparing(&self) -> Option<&dyn PreparingIndexer> {
        Some(self)
    }

    fn as_transactional_remover(&self) -> Option<&dyn TransactionalRemover> {
        Some(self)
    }

    fn as_transactional_clearer(&self) -> Option<&dyn TransactionalClearer> {
        Some(self)
    }
}

impl PreparingIndexer for Index {
    fn prepare(&self, file: &ParsedFile) -> Result<Option<Prepared>> {
        Ok(self
            .prepare_registrations(file)
            .map(|values| Box::new(values) as Prepared))
    }

    fn index_prepared(&self, file: &ParsedFile, prepared: Option<&Prepared>) -> Result<()> {
        let Some(prepared) = prepared else {
            return Ok(());
        };
        let Some(values) = prepared.downcast_ref::<PreparedRegistrations>() else {
            return Err(Error::msg(
                "invalid DBAL type registration preparation".to_owned(),
            ));
        };
        self.save(file, values)
    }
}

impl TransactionalRemover for Index {
    fn removed_files_in(&self, paths: &[String], mutation: &Mutation) -> Result<()> {
        self.remove_paths(paths, Some(mutation))
    }
}

impl TransactionalClearer for Index {
    fn clear_in(&self, mutation: &Mutation) -> Result<()> {
        self.clear_all(Some(mutation))
    }
}
