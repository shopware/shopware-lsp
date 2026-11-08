use std::collections::{BTreeMap, HashSet};
use std::path::Path;
use std::sync::{Arc, PoisonError, RwLock};

use serde::{Deserialize, Serialize};
use swlsp_indexer::{
    DataIndexer, Indexer, Mutation, ParsedFile, Result, Store, TransactionalClearer,
    TransactionalRemover,
};
use swlsp_php_types::{equal_fold, go_to_lower};

use crate::parser::{Root, roots_in_php};

/// All configuration roots of one file.
#[derive(Clone, Debug, Default, PartialEq, Eq, Serialize, Deserialize)]
pub struct Record {
    pub file: String,
    pub roots: Vec<Root>,
}

type PathSet = Arc<RwLock<HashSet<String>>>;

pub struct Index {
    records: DataIndexer<Record>,
    paths: PathSet,
}

impl Index {
    pub fn new(config_dir: impl AsRef<Path>, store: Option<&Store>) -> Result<Self> {
        let records = DataIndexer::repository(
            config_dir.as_ref().join("symfony_config.db"),
            "symfony.configuration.roots",
            store,
        )?;
        let paths = match records.get_all_file_paths() {
            Ok(paths) => paths,
            Err(error) => {
                let _ = records.close();
                return Err(error);
            }
        };
        Ok(Self {
            records,
            paths: Arc::new(RwLock::new(paths.into_iter().collect())),
        })
    }

    /// Roots named `name` (case-insensitive), sorted by file and start.
    pub fn roots(&self, name: &str) -> Result<Vec<Root>> {
        if name.is_empty() {
            return Ok(Vec::new());
        }
        let records = self.records.get_all_values_view()?;
        let mut result: Vec<Root> = records
            .iter()
            .flat_map(|record| &record.roots)
            .filter(|root| equal_fold(&root.name, name))
            .cloned()
            .collect();
        result.sort_by(|left, right| {
            left.file
                .cmp(&right.file)
                .then(left.range.start().cmp(&right.range.start()))
        });
        Ok(result)
    }

    /// Every root name (the last spelling wins per case-folded name), sorted
    /// case-insensitively.
    pub fn names(&self) -> Result<Vec<String>> {
        let records = self.records.get_all_values_view()?;
        let mut seen: BTreeMap<String, String> = BTreeMap::new();
        for root in records.iter().flat_map(|record| &record.roots) {
            if !root.name.is_empty() {
                seen.insert(go_to_lower(&root.name), root.name.clone());
            }
        }
        Ok(seen.into_values().collect())
    }

    fn has_path(&self, path: &str) -> bool {
        self.paths
            .read()
            .unwrap_or_else(PoisonError::into_inner)
            .contains(path)
    }

    fn publish(
        &self,
        mutation: Option<&Mutation>,
        update: impl FnOnce(&mut HashSet<String>) + Send + 'static,
    ) -> Result<()> {
        let paths = self.paths.clone();
        let publish = move || update(&mut paths.write().unwrap_or_else(PoisonError::into_inner));
        match mutation {
            None => {
                publish();
                Ok(())
            }
            Some(mutation) => mutation.after_commit(publish),
        }
    }

    fn remove_paths(&self, paths: &[String], mutation: Option<&Mutation>) -> Result<()> {
        let paths = paths.to_vec();
        self.publish(mutation, move |set| {
            for path in &paths {
                set.remove(path);
            }
        })
    }
}

impl Indexer for Index {
    fn id(&self) -> &str {
        "symfony.configuration"
    }

    fn index(&self, file: &ParsedFile) -> Result<()> {
        if file.extension() != ".php" {
            return Ok(());
        }
        let path = file.path();
        let source = file.source();
        if !(source.contains("getConfigTreeBuilder") && source.contains("TreeBuilder"))
            && !self.has_path(path)
        {
            return Ok(());
        }
        let roots = file
            .syntax_tree()
            .map(|tree| roots_in_php(path, &tree.root()))
            .unwrap_or_default();
        let present = !roots.is_empty();
        let record = present.then(|| Record {
            file: path.to_owned(),
            roots,
        });
        self.records.batch_save_items_in(
            file.mutation(),
            [(path, record.iter().map(|record| ("configuration", record)))],
        )?;
        let path = path.to_owned();
        self.publish(file.mutation(), move |set| {
            if present {
                set.insert(path);
            } else {
                set.remove(&path);
            }
        })
    }

    fn removed_files(&self, paths: &[String]) -> Result<()> {
        self.records.batch_delete_by_file_paths(paths)?;
        self.remove_paths(paths, None)
    }

    fn close(&self) -> Result<()> {
        self.records.close()
    }

    fn clear(&self) -> Result<()> {
        self.records.clear()?;
        self.publish(None, HashSet::clear)
    }

    fn as_transactional_remover(&self) -> Option<&dyn TransactionalRemover> {
        Some(self)
    }

    fn as_transactional_clearer(&self) -> Option<&dyn TransactionalClearer> {
        Some(self)
    }
}

impl TransactionalRemover for Index {
    fn removed_files_in(&self, paths: &[String], mutation: &Mutation) -> Result<()> {
        self.records
            .batch_delete_by_file_paths_in(Some(mutation), paths)?;
        self.remove_paths(paths, Some(mutation))
    }
}

impl TransactionalClearer for Index {
    fn clear_in(&self, mutation: &Mutation) -> Result<()> {
        self.records.clear_in(Some(mutation))?;
        self.publish(Some(mutation), HashSet::clear)
    }
}
