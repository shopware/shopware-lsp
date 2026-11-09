use std::path::Path;
use std::sync::{Arc, RwLock};

use swlsp_indexer::{
    DataIndexer, Error, Indexer, Mutation, ParsedFile, Prepared, PreparingIndexer, Result, Store,
    TransactionalClearer, TransactionalRemover,
};
use swlsp_php::PhpIndex;
use swlsp_php_binder::Binder;
use swlsp_php_semantic::Document;
use swlsp_uriutil::filepath;

use crate::bundle::{create_bundle_from_class, is_shopware_bundle, is_valid_for_index};
use crate::manifest::parse_manifest_tree;
use crate::paths::dir;
use crate::types::{ShopwareExtension, ShopwareExtensionType};

/// Indexes Shopware plugin/bundle classes and app manifests, keyed by
/// extension name.
pub struct ExtensionIndexer {
    repository: DataIndexer<ShopwareExtension>,
    php_index: RwLock<Option<PhpIndex>>,
}

/// One file's extensions; `None` for files this indexer does not own.
struct PreparedExtensions(Option<Vec<ShopwareExtension>>);

impl ExtensionIndexer {
    pub fn new(config_dir: impl AsRef<Path>, store: Option<&Store>) -> Result<Self> {
        let repository = DataIndexer::repository(
            config_dir.as_ref().join("extension.db"),
            "extensions",
            store,
        )?;
        Ok(Self {
            repository,
            php_index: RwLock::new(None),
        })
    }

    /// Reuses the PHP index's memoized analysis of each prepared file instead
    /// of binding PHP files a second time.
    pub fn set_php_index(&self, php_index: Option<PhpIndex>) {
        *self
            .php_index
            .write()
            .unwrap_or_else(|error| error.into_inner()) = php_index;
    }

    fn prepare_extensions(&self, file: &ParsedFile) -> Option<Vec<ShopwareExtension>> {
        if !is_valid_for_index(file.path()) {
            return None;
        }
        match file.extension() {
            ".php" => Some(self.prepare_bundle(file)),
            ".xml" => prepare_app(file),
            _ => None,
        }
    }

    fn prepare_bundle(&self, file: &ParsedFile) -> Vec<ShopwareExtension> {
        let php_index = self
            .php_index
            .read()
            .unwrap_or_else(|error| error.into_inner())
            .clone();
        let document: Arc<Document> = match (php_index, file.syntax_tree()) {
            (Some(php_index), _) => php_index.analyze_parsed_file(file),
            (None, Some(tree)) => Arc::new(Binder::new().bind(file.path(), 0, tree)),
            (None, None) => return Vec::new(),
        };
        document
            .symbols
            .iter()
            .find(|symbol| is_shopware_bundle(symbol))
            .map(create_bundle_from_class)
            .into_iter()
            .collect()
    }

    /// The app whose root contains `path` most specifically.
    pub fn find_app_for_file(&self, path: &str) -> Result<Option<ShopwareExtension>> {
        let extensions = self.get_all()?;
        let path = filepath::clean(path);
        let mut best: Option<&ShopwareExtension> = None;
        for current in extensions.iter() {
            if current.kind != ShopwareExtensionType::App {
                continue;
            }
            let root = filepath::clean(&current.path);
            match filepath::rel(&root, &path) {
                Ok(relative) if relative != ".." && !relative.starts_with("../") => {}
                _ => continue,
            }
            if best.is_none_or(|best| root.len() > best.path.len()) {
                best = Some(current);
            }
        }
        Ok(best.cloned())
    }

    pub fn find_by_name(&self, name: &str) -> Result<Option<ShopwareExtension>> {
        Ok(self.repository.get_values_view(name)?.first().cloned())
    }

    pub fn get_extension_by_name(&self, name: &str) -> Option<ShopwareExtension> {
        self.find_by_name(name).ok().flatten()
    }

    pub fn get_all(&self) -> Result<Arc<[ShopwareExtension]>> {
        self.repository.get_all_values_view()
    }

    /// The underlying repository (oracle and diagnostics access).
    pub fn repository(&self) -> &DataIndexer<ShopwareExtension> {
        &self.repository
    }
}

fn prepare_app(file: &ParsedFile) -> Option<Vec<ShopwareExtension>> {
    let path = file.path();
    if filepath::base(path) != "manifest.xml" {
        return None;
    }
    let manifest = file
        .syntax_tree()
        .and_then(|tree| parse_manifest_tree(path, tree));
    Some(
        manifest
            .map(|manifest| ShopwareExtension {
                name: manifest.name,
                kind: ShopwareExtensionType::App,
                path: dir(path),
                permissions: manifest.permissions,
            })
            .into_iter()
            .collect(),
    )
}

impl Indexer for ExtensionIndexer {
    fn id(&self) -> &str {
        "extension.indexer"
    }

    fn index(&self, file: &ParsedFile) -> Result<()> {
        let prepared = self.prepare(file)?;
        self.index_prepared(file, prepared.as_ref())
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

impl PreparingIndexer for ExtensionIndexer {
    fn prepare(&self, file: &ParsedFile) -> Result<Option<Prepared>> {
        Ok(Some(Box::new(PreparedExtensions(
            self.prepare_extensions(file),
        ))))
    }

    fn index_prepared(&self, file: &ParsedFile, prepared: Option<&Prepared>) -> Result<()> {
        let Some(prepared) = prepared.and_then(|value| value.downcast_ref::<PreparedExtensions>())
        else {
            return Err(Error::msg(format!(
                "prepared extensions are required for {}",
                file.path()
            )));
        };
        let Some(extensions) = &prepared.0 else {
            return Ok(());
        };
        let items = extensions
            .iter()
            .map(|extension| (extension.name.as_str(), extension));
        self.repository
            .batch_save_items_in(file.mutation(), [(file.path(), items)])
    }
}

impl TransactionalRemover for ExtensionIndexer {
    fn removed_files_in(&self, paths: &[String], mutation: &Mutation) -> Result<()> {
        self.repository
            .batch_delete_by_file_paths_in(Some(mutation), paths)
    }
}

impl TransactionalClearer for ExtensionIndexer {
    fn clear_in(&self, mutation: &Mutation) -> Result<()> {
        self.repository.clear_in(Some(mutation))
    }
}

/// Advanced by every committed change to the indexed extensions and by the
/// PHP index their classes resolve through.
impl swlsp_indexer::Revisioned for ExtensionIndexer {
    fn revision(&self) -> u64 {
        let php = self
            .php_index
            .read()
            .unwrap_or_else(std::sync::PoisonError::into_inner)
            .clone();
        self.repository.revision() + swlsp_indexer::Revisioned::revision(&php)
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn extension_indexer_reuses_prepared_php_analysis() {
        let php_cache = tempfile::tempdir().unwrap();
        let php_index = PhpIndex::open(php_cache.path(), None).unwrap();
        let cache = tempfile::tempdir().unwrap();
        let extension_index = ExtensionIndexer::new(cache.path(), None).unwrap();
        extension_index.set_php_index(Some(php_index.clone()));

        let file = ParsedFile::new(
            "/project/src/DemoPlugin.php",
            b"<?php\nnamespace App;\nuse Shopware\\Core\\Framework\\Plugin;\nfinal class DemoPlugin extends Plugin {}\n"
                .to_vec(),
        );
        let document = php_index.analyze_parsed_file(&file);
        let prepared = extension_index.prepare(&file).unwrap();
        assert!(Arc::ptr_eq(
            &document,
            &php_index.analyze_parsed_file(&file)
        ));
        extension_index
            .index_prepared(&file, prepared.as_ref())
            .unwrap();

        let indexed = extension_index.get_extension_by_name("DemoPlugin").unwrap();
        assert_eq!(indexed.kind, ShopwareExtensionType::Bundle);
        assert_eq!(indexed.path, file.path());
        assert_eq!(indexed.root_path(), "/project/src");
        assert_eq!(
            indexed.administration_source_path(),
            "/project/src/Resources/app/administration/src"
        );
        extension_index.close().unwrap();
        php_index.close().unwrap();
    }

    #[test]
    fn indexes_bundles_without_php_index_and_app_manifests() {
        let cache = tempfile::tempdir().unwrap();
        let index = ExtensionIndexer::new(cache.path(), None).unwrap();
        index
            .index(&ParsedFile::new(
                "/project/custom/plugins/Demo/src/DemoBundle.php",
                b"<?php\nnamespace Demo;\nclass DemoBundle extends \\Shopware\\Core\\Framework\\Bundle {}\n".to_vec(),
            ))
            .unwrap();
        index
            .index(&ParsedFile::new(
                "/project/custom/apps/Acme/manifest.xml",
                b"<manifest><meta><name>Acme</name></meta><permissions><read>product</read></permissions></manifest>"
                    .to_vec(),
            ))
            .unwrap();
        let bundle = index.get_extension_by_name("DemoBundle").unwrap();
        assert_eq!(
            bundle.storefront_views_path(),
            "/project/custom/plugins/Demo/src/Resources/views"
        );
        let app = index
            .find_app_for_file("/project/custom/apps/Acme/Resources/scripts/a.twig")
            .unwrap()
            .unwrap();
        assert_eq!(app.name, "Acme");
        assert_eq!(app.root_path(), "/project/custom/apps/Acme");
        assert_eq!(app.permissions.len(), 1);
        assert!(
            index
                .find_app_for_file("/project/custom/apps/Other/x")
                .unwrap()
                .is_none()
        );
        index
            .removed_files(&["/project/custom/apps/Acme/manifest.xml".to_owned()])
            .unwrap();
        assert!(index.get_extension_by_name("Acme").is_none());
        index.close().unwrap();
    }
}
