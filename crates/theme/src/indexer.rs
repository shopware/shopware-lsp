use std::collections::BTreeMap;
use std::path::Path;
use std::sync::Arc;

use swlsp_indexer::{
    DataIndexer, Error, Indexer, Mutation, ParsedFile, Result, Store, TransactionalClearer,
    TransactionalRemover,
};

use crate::parser::parse_theme_config_tree;
use crate::types::ThemeConfigField;

/// Indexes the `config.fields` of `theme.json` files by field key.
pub struct ThemeConfigIndexer {
    repository: DataIndexer<ThemeConfigField>,
}

/// Whether `path` is indexed as a theme configuration (any name ending in
/// `theme.json`).
pub fn is_theme_file(path: &str) -> bool {
    path.ends_with("theme.json")
}

impl ThemeConfigIndexer {
    pub fn new(config_dir: impl AsRef<Path>, store: Option<&Store>) -> Result<Self> {
        let repository = DataIndexer::repository(
            config_dir.as_ref().join("theme_config.db"),
            "theme.config",
            store,
        )?;
        Ok(Self { repository })
    }

    /// Every indexed field key.
    pub fn theme_config_fields(&self) -> Result<Arc<[String]>> {
        self.repository.get_all_keys_view()
    }

    /// The fields declared under `key` across themes.
    pub fn theme_config_field(&self, key: &str) -> Result<Arc<[ThemeConfigField]>> {
        self.repository.get_values_view(key)
    }

    pub fn all_theme_config_fields(&self) -> Result<Arc<[ThemeConfigField]>> {
        self.repository.get_all_values_view()
    }

    /// The underlying repository (diagnostics and tests).
    pub fn repository(&self) -> &DataIndexer<ThemeConfigField> {
        &self.repository
    }
}

impl Indexer for ThemeConfigIndexer {
    fn id(&self) -> &str {
        "theme.indexer"
    }

    fn index(&self, file: &ParsedFile) -> Result<()> {
        let path = file.path();
        if !is_theme_file(path) {
            return Ok(());
        }
        let Some(tree) = file.syntax_tree() else {
            return Err(Error::msg("JSON root is not an object"));
        };
        let fields = parse_theme_config_tree(tree, file.line_index(), path)
            .map_err(|error| Error::msg(error.to_string()))?;
        // Later duplicates of a key replace earlier ones.
        let fields: BTreeMap<&str, &ThemeConfigField> = fields
            .iter()
            .map(|field| (field.key.as_str(), field))
            .collect();
        self.repository
            .batch_save_items_in(file.mutation(), [(path, fields)])
    }

    fn removed_files(&self, paths: &[String]) -> Result<()> {
        self.repository.batch_delete_by_file_paths(paths)
    }

    fn close(&self) -> Result<()> {
        self.repository.close()
    }

    fn clear(&self) -> Result<()> {
        self.repository.clear()
    }

    fn as_transactional_remover(&self) -> Option<&dyn TransactionalRemover> {
        Some(self)
    }

    fn as_transactional_clearer(&self) -> Option<&dyn TransactionalClearer> {
        Some(self)
    }
}

impl TransactionalRemover for ThemeConfigIndexer {
    fn removed_files_in(&self, paths: &[String], mutation: &Mutation) -> Result<()> {
        self.repository
            .batch_delete_by_file_paths_in(Some(mutation), paths)
    }
}

impl TransactionalClearer for ThemeConfigIndexer {
    fn clear_in(&self, mutation: &Mutation) -> Result<()> {
        self.repository.clear_in(Some(mutation))
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn theme_config_indexer() {
        let cache = tempfile::tempdir().unwrap();
        let indexer = ThemeConfigIndexer::new(cache.path(), None).unwrap();
        let bytes =
            std::fs::read(concat!(env!("CARGO_MANIFEST_DIR"), "/testdata/theme.json")).unwrap();
        let file_path = "testdata/theme.json";
        indexer.index(&ParsedFile::new(file_path, bytes)).unwrap();

        assert!(!indexer.theme_config_fields().unwrap().is_empty());
        let fields = indexer
            .theme_config_field("sw-color-brand-primary")
            .unwrap();
        assert!(!fields.is_empty());
        assert_eq!(fields[0].label["en-GB"], "Primary colour");
        assert_eq!(fields[0].kind, "color");
        assert!(!indexer.all_theme_config_fields().unwrap().is_empty());

        indexer.removed_files(&[file_path.to_owned()]).unwrap();
        assert!(indexer.theme_config_fields().unwrap().is_empty());
        indexer.close().unwrap();
    }
}
