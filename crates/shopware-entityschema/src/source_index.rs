//! Source of class-based DAL candidates.
//!
//! Only files which syntactically look like class-based definitions or
//! extensions retain their source; the semantic index proves the effective
//! subtype later. This keeps request paths off the disk.

use std::path::Path;
use swlsp_syntax::SyntaxNodeExt as _;

use serde::{Deserialize, Serialize};
use swlsp_indexer::{
    DataIndexer, Indexer, Mutation, ParsedFile, Prepared, PreparingIndexer, Store,
    TransactionalClearer, TransactionalRemover,
};
use swlsp_parser_php::query as phpquery;
use swlsp_syntax::SyntaxNode;

use crate::model::short_class;
use crate::util::{qualify, trim_backslash};
use swlsp_uriutil::filepath;

pub const SOURCE_INDEX_ID: &str = "shopware.entity_schema.sources";

#[derive(Clone, Debug, Default, PartialEq, Eq, Serialize, Deserialize)]
#[serde(default)]
pub struct SourceDocument {
    pub path: String,
    pub source: String,
    pub classes: Vec<String>,
}

pub struct SourceIndex {
    documents: DataIndexer<SourceDocument>,
}

struct PreparedSource(Option<SourceDocument>);

impl SourceIndex {
    pub fn new(config_dir: impl AsRef<Path>, store: Option<&Store>) -> swlsp_indexer::Result<Self> {
        let documents = DataIndexer::repository(
            config_dir.as_ref().join("shopware_entity_schema.db"),
            SOURCE_INDEX_ID,
            store,
        )?;
        Ok(SourceIndex { documents })
    }

    fn prepare_document(&self, file: &ParsedFile) -> Option<SourceDocument> {
        if file.extension() != ".php" {
            return None;
        }
        let root = file.syntax_tree()?.root();
        let namespace = trim_backslash(&phpquery::namespace(&root)).to_owned();
        let classes: Vec<String> = phpquery::classes(&root)
            .iter()
            .filter(|class| class_based_source_candidate(class))
            .map(phpquery::class_name)
            .filter(|name| !name.is_empty())
            .map(|name| qualify(&namespace, &name))
            .collect();
        if classes.is_empty() {
            return None;
        }
        Some(SourceDocument {
            path: file.path().to_owned(),
            source: file.source().to_owned(),
            classes,
        })
    }

    /// The captured source of `path`, when it holds a DAL candidate.
    pub fn source(&self, path: &str) -> swlsp_indexer::Result<Option<String>> {
        if path.is_empty() {
            return Ok(None);
        }
        let path = filepath::clean(path);
        let documents = self.documents.get_values(&path)?;
        Ok(documents
            .into_iter()
            .find(|document| filepath::clean(&document.path) == path)
            .map(|document| document.source))
    }
}

pub(crate) fn class_based_source_candidate(class: &SyntaxNode) -> bool {
    for parent in phpquery::class_extends(class) {
        if matches!(
            short_class(&parent),
            "EntityDefinition"
                | "MappingEntityDefinition"
                | "EntityTranslationDefinition"
                | "EntityExtension"
                | "BulkEntityExtension"
        ) {
            return true;
        }
    }
    if class.text_string().contains("ENTITY_NAME") {
        return true;
    }
    phpquery::methods(class).iter().any(|method| {
        matches!(
            phpquery::method_name(method).as_str(),
            "defineFields"
                | "getEntityName"
                | "getDefinitionClass"
                | "extendFields"
                | "modifyFields"
                | "extendProtections"
                | "defineProtections"
                | "collect"
        )
    })
}

impl Indexer for SourceIndex {
    fn id(&self) -> &str {
        SOURCE_INDEX_ID
    }

    fn index(&self, file: &ParsedFile) -> swlsp_indexer::Result<()> {
        let prepared = self.prepare(file)?;
        self.index_prepared(file, prepared.as_ref())
    }

    fn removed_files(&self, paths: &[String]) -> swlsp_indexer::Result<()> {
        self.documents.batch_delete_by_file_paths(paths)
    }

    fn close(&self) -> swlsp_indexer::Result<()> {
        self.documents.close()
    }

    fn clear(&self) -> swlsp_indexer::Result<()> {
        self.documents.clear()
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

impl PreparingIndexer for SourceIndex {
    fn prepare(&self, file: &ParsedFile) -> swlsp_indexer::Result<Option<Prepared>> {
        Ok(Some(Box::new(PreparedSource(self.prepare_document(file)))))
    }

    fn index_prepared(
        &self,
        file: &ParsedFile,
        prepared: Option<&Prepared>,
    ) -> swlsp_indexer::Result<()> {
        if file.extension() != ".php" {
            return Ok(());
        }
        let document = prepared
            .and_then(|value| value.downcast_ref::<PreparedSource>())
            .and_then(|prepared| prepared.0.as_ref());
        let path = file.path();
        // Saving an empty set clears records of a file that stopped being a
        // candidate.
        self.documents.batch_save_items_in(
            file.mutation(),
            [(path, document.into_iter().map(|document| (path, document)))],
        )
    }
}

impl TransactionalRemover for SourceIndex {
    fn removed_files_in(&self, paths: &[String], mutation: &Mutation) -> swlsp_indexer::Result<()> {
        self.documents
            .batch_delete_by_file_paths_in(Some(mutation), paths)
    }
}

impl TransactionalClearer for SourceIndex {
    fn clear_in(&self, mutation: &Mutation) -> swlsp_indexer::Result<()> {
        self.documents.clear_in(Some(mutation))
    }
}

/// Advanced by every committed change to the indexed entity sources.
impl swlsp_indexer::Revisioned for SourceIndex {
    fn revision(&self) -> u64 {
        self.documents.revision()
    }
}
