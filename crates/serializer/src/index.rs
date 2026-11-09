use std::collections::{BTreeMap, HashSet};
use std::path::Path;
use std::sync::{Arc, PoisonError, RwLock};

use serde::{Deserialize, Serialize};
use swlsp_indexer::{
    DataIndexer, Indexer, Mutation, ParsedFile, Result, Store, TransactionalClearer,
    TransactionalRemover,
};
use swlsp_php_types::{equal_fold, go_to_lower};
use swlsp_syntax::TextRange;

use crate::parser::{normalize_class, usages_in_document};

#[derive(Clone, Copy, Debug, Default, PartialEq, Eq, Hash, Serialize, Deserialize)]
pub enum TargetKind {
    /// `'App\Model'`
    #[default]
    String,
    /// `Model::class` or `Model::class . '[]'`
    ClassConstant,
}

/// One `deserialize()` call whose type argument names a class.
#[derive(Clone, Debug, PartialEq, Eq, Serialize, Deserialize)]
pub struct Usage {
    pub class: String,
    pub file: String,
    #[serde(with = "range_serde")]
    pub range: TextRange,
    pub kind: TargetKind,
}

/// All usages of one file.
#[derive(Clone, Debug, Default, PartialEq, Eq, Serialize, Deserialize)]
pub struct Record {
    pub file: String,
    pub usages: Vec<Usage>,
}

type PathSet = Arc<RwLock<HashSet<String>>>;

pub struct Index {
    records: DataIndexer<Record>,
    paths: PathSet,
}

impl Index {
    /// Advanced after every committed change to this index; inspections
    /// declare it as an input (`ProblemAnalyzer::input_revision`).
    pub fn revision(&self) -> u64 {
        self.records.revision()
    }

    pub fn new(config_dir: impl AsRef<Path>, store: Option<&Store>) -> Result<Self> {
        let records = DataIndexer::repository(
            config_dir.as_ref().join("serializer.db"),
            "symfony.serializer.usages",
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

    /// Usages whose class equals `class_name` case-insensitively, sorted by
    /// file and start offset.
    pub fn usages(&self, class_name: &str) -> Result<Vec<Usage>> {
        if class_name.is_empty() {
            return Ok(Vec::new());
        }
        let normalized = normalize_class(class_name);
        let records = self.records.get_all_values_view()?;
        let mut result: Vec<Usage> = records
            .iter()
            .flat_map(|record| &record.usages)
            .filter(|usage| equal_fold(&usage.class, normalized))
            .cloned()
            .collect();
        result.sort_by(|left, right| {
            left.file
                .cmp(&right.file)
                .then(left.range.start().cmp(&right.range.start()))
        });
        Ok(result)
    }

    /// Every target class (the last spelling wins per case-folded name),
    /// sorted case-insensitively.
    pub fn classes(&self) -> Result<Vec<String>> {
        let records = self.records.get_all_values_view()?;
        let mut seen: BTreeMap<String, String> = BTreeMap::new();
        for usage in records.iter().flat_map(|record| &record.usages) {
            if !usage.class.is_empty() {
                seen.insert(go_to_lower(&usage.class), usage.class.clone());
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
        "symfony.serializer"
    }

    fn index(&self, file: &ParsedFile) -> Result<()> {
        if file.extension() != ".php" {
            return Ok(());
        }
        let path = file.path();
        if !file.source().contains("deserialize") && !self.has_path(path) {
            return Ok(());
        }
        let usages = file
            .syntax_tree()
            .map(|tree| usages_in_document(path, &tree.root()))
            .unwrap_or_default();
        let present = !usages.is_empty();
        let record = present.then(|| Record {
            file: path.to_owned(),
            usages,
        });
        self.records.batch_save_items_in(
            file.mutation(),
            [(path, record.iter().map(|record| ("serializer", record)))],
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

mod range_serde {
    use serde::{Deserialize, Deserializer, Serialize, Serializer};
    use swlsp_syntax::TextRange;

    pub fn serialize<S: Serializer>(value: &TextRange, serializer: S) -> Result<S::Ok, S::Error> {
        (u32::from(value.start()), u32::from(value.end())).serialize(serializer)
    }

    pub fn deserialize<'de, D: Deserializer<'de>>(deserializer: D) -> Result<TextRange, D::Error> {
        let (start, end) = <(u32, u32)>::deserialize(deserializer)?;
        if start > end {
            return Err(serde::de::Error::custom("invalid text range"));
        }
        Ok(TextRange::new(start.into(), end.into()))
    }
}

impl swlsp_indexer::Revisioned for Index {
    fn revision(&self) -> u64 {
        Index::revision(self)
    }
}
