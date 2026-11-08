use std::collections::HashMap;
use std::path::Path;
use std::sync::{Arc, OnceLock, RwLock};

use swlsp_indexer::{
    DataIndexer, Error, Indexer, Mutation, ParsedFile, Prepared, PreparingIndexer, Result, Store,
    TransactionalClearer, TransactionalRemover,
};
use swlsp_syntax::SyntaxNode;
use swlsp_uriutil::filepath;

use crate::config::{CONFIG_LOCATIONS, configuration_root, default_config, parse_config_tree};
use crate::config_types::Config;

/// Called with the changed configuration path (empty after a clear).
pub type ChangeCallback = Arc<dyn Fn(&str) + Send + Sync>;

/// Owns the complete CLI extension configuration of every extension. Disk
/// snapshots persist in the shared store; editor overlays never modify the
/// persisted generation. Published snapshots are immutable.
pub struct ConfigIndex {
    repository: DataIndexer<Config>,
    shared: Arc<Shared>,
}

#[derive(Default)]
struct Shared {
    live: RwLock<HashMap<String, Arc<Config>>>,
    changed: RwLock<Option<ChangeCallback>>,
}

impl Shared {
    fn notify(&self, path: &str) {
        let changed = self
            .changed
            .read()
            .unwrap_or_else(|error| error.into_inner())
            .clone();
        if let Some(changed) = changed {
            changed(path);
        }
    }
}

/// The prepared value; `None` for files that are not configurations.
struct PreparedConfig(Option<Config>);

fn default_snapshot() -> Arc<Config> {
    static DEFAULT: OnceLock<Arc<Config>> = OnceLock::new();
    DEFAULT.get_or_init(|| Arc::new(default_config())).clone()
}

impl ConfigIndex {
    pub fn new(cache: impl AsRef<Path>, store: Option<&Store>) -> Result<Self> {
        let repository = DataIndexer::repository(
            cache.as_ref().join("extension_config.db"),
            "extension_config",
            store,
        )?;
        Ok(Self {
            repository,
            shared: Arc::default(),
        })
    }

    pub fn is_configuration(path: &str) -> bool {
        configuration_root(path).is_some()
    }

    /// The first existing configuration of the extension in `dir` in CLI
    /// precedence order (open documents first per location), or the
    /// defaults. Only the three supported paths are queried.
    pub fn get_config(&self, dir: &str) -> Result<Arc<Config>> {
        let live = self
            .shared
            .live
            .read()
            .unwrap_or_else(|error| error.into_inner());
        for location in CONFIG_LOCATIONS {
            let path = filepath::join(&[dir, location]);
            if let Some(config) = live.get(&path) {
                return Ok(config.clone());
            }
            let values = self
                .repository
                .get_values_view(&path)
                .map_err(|error| error.context(format!("query extension configuration {path}")))?;
            if let Some(config) = values.first() {
                return Ok(Arc::new(config.clone()));
            }
        }
        Ok(default_snapshot())
    }

    pub fn store_compliance_enabled(&self, dir: &str) -> Result<bool> {
        Ok(self.get_config(dir)?.validation.store_compliance)
    }

    pub fn set_on_change(&self, changed: Option<ChangeCallback>) {
        *self
            .shared
            .changed
            .write()
            .unwrap_or_else(|error| error.into_inner()) = changed;
    }

    pub fn update_live_document(&self, path: &str, root: &SyntaxNode) {
        let path = filepath::clean(path);
        let config = parse_config_tree(&path, root);
        self.shared
            .live
            .write()
            .unwrap_or_else(|error| error.into_inner())
            .insert(path.clone(), Arc::new(config));
        self.shared.notify(&path);
    }

    pub fn remove_live_document(&self, path: &str) {
        let path = filepath::clean(path);
        self.shared
            .live
            .write()
            .unwrap_or_else(|error| error.into_inner())
            .remove(&path);
        self.shared.notify(&path);
    }

    fn publish(&self, mutation: Option<&Mutation>, paths: Vec<String>) -> Result<()> {
        let shared = self.shared.clone();
        let publish = move || {
            for path in &paths {
                shared.notify(path);
            }
        };
        match mutation {
            Some(mutation) => mutation.after_commit(publish),
            None => {
                publish();
                Ok(())
            }
        }
    }

    fn remove_in(&self, paths: &[String], mutation: Option<&Mutation>) -> Result<()> {
        let configurations: Vec<String> = paths
            .iter()
            .filter(|path| Self::is_configuration(path))
            .map(|path| filepath::clean(path))
            .collect();
        if configurations.is_empty() {
            return Ok(());
        }
        self.repository
            .batch_delete_by_file_paths_in(mutation, &configurations)?;
        self.publish(mutation, configurations)
    }

    fn clear_in_mutation(&self, mutation: Option<&Mutation>) -> Result<()> {
        self.repository.clear_in(mutation)?;
        self.publish(mutation, vec![String::new()])
    }
}

impl Indexer for ConfigIndex {
    fn id(&self) -> &str {
        "extension.config"
    }

    fn index(&self, file: &ParsedFile) -> Result<()> {
        let prepared = self.prepare(file)?;
        self.index_prepared(file, prepared.as_ref())
    }

    fn removed_files(&self, paths: &[String]) -> Result<()> {
        self.remove_in(paths, None)
    }

    fn close(&self) -> Result<()> {
        self.set_on_change(None);
        self.repository.close()
    }

    fn clear(&self) -> Result<()> {
        self.clear_in_mutation(None)
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

impl PreparingIndexer for ConfigIndex {
    fn prepare(&self, file: &ParsedFile) -> Result<Option<Prepared>> {
        if !Self::is_configuration(file.path()) {
            return Ok(Some(Box::new(PreparedConfig(None))));
        }
        let config = match file.syntax_tree() {
            Some(tree) => parse_config_tree(file.path(), &tree.root()),
            None => {
                let mut config = default_config();
                config.storage_location = filepath::clean(file.path());
                config
            }
        };
        Ok(Some(Box::new(PreparedConfig(Some(config)))))
    }

    fn index_prepared(&self, file: &ParsedFile, prepared: Option<&Prepared>) -> Result<()> {
        let Some(prepared) = prepared.and_then(|value| value.downcast_ref::<PreparedConfig>())
        else {
            return Err(Error::msg(format!(
                "prepared extension configuration is required for {}",
                file.path()
            )));
        };
        let Some(config) = &prepared.0 else {
            return Ok(());
        };
        let path = filepath::clean(file.path());
        self.repository
            .batch_save_items_in(file.mutation(), [(&path, [(&path, config)])])?;
        self.publish(file.mutation(), vec![path])
    }
}

impl TransactionalRemover for ConfigIndex {
    fn removed_files_in(&self, paths: &[String], mutation: &Mutation) -> Result<()> {
        self.remove_in(paths, Some(mutation))
    }
}

impl TransactionalClearer for ConfigIndex {
    fn clear_in(&self, mutation: &Mutation) -> Result<()> {
        self.clear_in_mutation(Some(mutation))
    }
}

#[cfg(test)]
mod tests;
