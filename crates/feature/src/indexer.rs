use std::path::Path;
use std::sync::Arc;

use swlsp_indexer::{
    DataIndexer, Error, Indexer, Mutation, ParsedFile, Result, ResultExt, Store,
    TransactionalClearer, TransactionalRemover,
};

use crate::{Feature, MissingFeatures, parse_feature_tree};

/// Indexes feature flags from YAML files whose path mentions `feature`.
pub struct FeatureIndexer {
    features: DataIndexer<Feature>,
}

impl FeatureIndexer {
    /// Uses the shared workspace `store`, or an isolated database in
    /// `config_dir` without one.
    pub fn new(config_dir: impl AsRef<Path>, store: Option<&Store>) -> Result<Self> {
        let features = DataIndexer::repository(
            config_dir.as_ref().join("feature_flags.db"),
            "feature.flags",
            store,
        )?;
        Ok(Self { features })
    }

    pub fn feature_by_name(&self, name: &str) -> Result<Arc<[Feature]>> {
        self.features.get_values_view(name)
    }

    pub fn all_features(&self) -> Result<Arc<[Feature]>> {
        self.features.get_all_values_view()
    }
}

impl Indexer for FeatureIndexer {
    fn id(&self) -> &str {
        "feature.indexer"
    }

    fn index(&self, file: &ParsedFile) -> Result<()> {
        let path = file.path();
        if !path.ends_with(".yaml") && !path.ends_with(".yml") {
            return Ok(());
        }
        if !path.to_lowercase().contains("feature") {
            return Ok(());
        }
        let features = match file.syntax_tree() {
            Some(tree) => parse_feature_tree(tree, file.line_index(), path),
            None => Err(MissingFeatures {
                file: path.to_owned(),
            }),
        }
        .map_err(|error| Error::other(error).context("parsing feature file"))?;
        // Later duplicates replace earlier ones.
        let mut items: Vec<(&str, &Feature)> = Vec::with_capacity(features.len());
        for feature in &features {
            match items.iter_mut().find(|(name, _)| *name == feature.name) {
                Some(existing) => existing.1 = feature,
                None => items.push((&feature.name, feature)),
            }
        }
        self.features
            .batch_save_items_in(file.mutation(), [(path, items)])
            .context("saving features")
    }

    fn removed_files(&self, paths: &[String]) -> Result<()> {
        self.features
            .batch_delete_by_file_paths(paths)
            .context("removing features")
    }

    fn close(&self) -> Result<()> {
        self.features.close()
    }

    fn clear(&self) -> Result<()> {
        self.features.clear()
    }

    fn as_transactional_remover(&self) -> Option<&dyn TransactionalRemover> {
        Some(self)
    }

    fn as_transactional_clearer(&self) -> Option<&dyn TransactionalClearer> {
        Some(self)
    }
}

impl TransactionalRemover for FeatureIndexer {
    fn removed_files_in(&self, paths: &[String], mutation: &Mutation) -> Result<()> {
        self.features
            .batch_delete_by_file_paths_in(Some(mutation), paths)
    }
}

impl TransactionalClearer for FeatureIndexer {
    fn clear_in(&self, mutation: &Mutation) -> Result<()> {
        self.features.clear_in(Some(mutation))
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    fn fixture() -> (String, Vec<u8>) {
        let path = concat!(env!("CARGO_MANIFEST_DIR"), "/testdata/feature.yaml").to_owned();
        let content = std::fs::read(&path).unwrap();
        (path, content)
    }

    #[test]
    fn indexes_features() {
        let dir = tempfile::tempdir().unwrap();
        let indexer = FeatureIndexer::new(dir.path(), None).unwrap();
        let (path, content) = fixture();
        indexer.index(&ParsedFile::new(path, content)).unwrap();

        assert_eq!(indexer.all_features().unwrap().len(), 8);
        let v650 = indexer.feature_by_name("v6.5.0.0").unwrap();
        assert_eq!(v650.len(), 1);
        assert_eq!(v650[0].name, "v6.5.0.0");
        assert_eq!(v650[0].line, 4);
        assert_eq!(
            indexer
                .feature_by_name("ACCESSIBILITY_TWEAKS")
                .unwrap()
                .len(),
            1
        );
        assert_eq!(
            indexer
                .feature_by_name("FLOW_EXECUTION_AFTER_BUSINESS_PROCESS")
                .unwrap()
                .len(),
            1
        );
        indexer.close().unwrap();
    }

    #[test]
    fn removed_files_drop_features() {
        let dir = tempfile::tempdir().unwrap();
        let indexer = FeatureIndexer::new(dir.path(), None).unwrap();
        let (path, content) = fixture();
        indexer
            .index(&ParsedFile::new(path.clone(), content))
            .unwrap();
        assert!(!indexer.all_features().unwrap().is_empty());
        indexer.removed_files(&[path]).unwrap();
        assert!(indexer.all_features().unwrap().is_empty());
        indexer.close().unwrap();
    }

    #[test]
    fn ignores_unrelated_paths_and_rejects_empty_feature_files() {
        let dir = tempfile::tempdir().unwrap();
        let indexer = FeatureIndexer::new(dir.path(), None).unwrap();
        indexer
            .index(&ParsedFile::new(
                "/p/config/services.yaml",
                b"a: b".to_vec(),
            ))
            .unwrap();
        indexer
            .index(&ParsedFile::new("/p/feature.json", b"{}".to_vec()))
            .unwrap();
        let error = indexer
            .index(&ParsedFile::new("/p/Feature/flags.yml", b"a: b".to_vec()))
            .unwrap_err();
        assert!(
            error.to_string().contains("could not find flags node"),
            "{error}"
        );
        indexer.close().unwrap();
    }
}
