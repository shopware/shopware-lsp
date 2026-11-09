use std::collections::{BTreeMap, HashMap};
use std::path::Path;
use std::sync::Arc;

use swlsp_indexer::{
    DataIndexer, Error, Indexer, Mutation, ParsedFile, Prepared, PreparingIndexer, Result, Store,
    TransactionalClearer, TransactionalRemover,
};
use swlsp_language::LanguageId;

use crate::{Snippet, parse_snippet_tree};

const ADMINISTRATION: &str = "/Resources/app/administration/";
const STOREFRONT_SNIPPETS: &str = "/Resources/snippet/";

/// Indexes Storefront (`Resources/snippet/`) and Administration
/// (`Resources/app/administration/**/snippet/*.json`) snippet files.
pub struct SnippetIndexer {
    frontend: DataIndexer<Snippet>,
    admin: DataIndexer<Snippet>,
}

impl SnippetIndexer {
    /// Advanced after every committed change to this index; inspections
    /// declare it as an input (`ProblemAnalyzer::input_revision`).
    pub fn revision(&self) -> u64 {
        self.frontend.revision() + self.admin.revision()
    }

    pub fn new(config_dir: impl AsRef<Path>, store: Option<&Store>) -> Result<Self> {
        let dir = config_dir.as_ref();
        let frontend =
            DataIndexer::repository(dir.join("frontend_snippet.db"), "snippets.frontend", store)?;
        let admin =
            match DataIndexer::repository(dir.join("admin_snippet.db"), "snippets.admin", store) {
                Ok(admin) => admin,
                Err(error) => {
                    let _ = frontend.close();
                    return Err(error);
                }
            };
        Ok(Self { frontend, admin })
    }

    pub fn frontend_snippet_keys(&self) -> Result<Arc<[String]>> {
        self.frontend.get_all_keys_view()
    }

    pub fn frontend_snippet(&self, key: &str) -> Result<Arc<[Snippet]>> {
        self.frontend.get_values_view(key)
    }

    pub fn all_frontend_snippets(&self) -> Result<Arc<[Snippet]>> {
        self.frontend.get_all_values_view()
    }

    pub fn admin_snippet_keys(&self) -> Result<Arc<[String]>> {
        self.admin.get_all_keys_view()
    }

    pub fn admin_snippet(&self, key: &str) -> Result<Arc<[Snippet]>> {
        self.admin.get_values_view(key)
    }

    pub fn all_admin_snippets(&self) -> Result<Arc<[Snippet]>> {
        self.admin.get_all_values_view()
    }

    /// Snippet keys mapped to their text, preferring English translations.
    pub fn frontend_snippets_with_text(&self) -> Result<HashMap<String, String>> {
        snippets_with_text(&self.frontend)
    }

    /// Snippet keys mapped to their text, preferring English translations.
    pub fn admin_snippets_with_text(&self) -> Result<HashMap<String, String>> {
        snippets_with_text(&self.admin)
    }

    /// Stores extracted snippets in the repository of their target.
    fn persist(&self, file: &ParsedFile, prepared: &PreparedSnippets) -> Result<()> {
        let Some((target, snippets)) = &prepared.0 else {
            return Ok(());
        };
        let repository = match target {
            SnippetTarget::Frontend => &self.frontend,
            SnippetTarget::Admin => &self.admin,
        };
        repository.batch_save_items_in(file.mutation(), [(file.path(), snippets)])
    }

    fn split_paths(paths: &[String]) -> (Vec<&str>, Vec<&str>) {
        let mut frontend = Vec::new();
        let mut admin = Vec::new();
        for path in paths {
            if path.contains(ADMINISTRATION) {
                admin.push(path.as_str());
            } else if path.contains(STOREFRONT_SNIPPETS) {
                frontend.push(path.as_str());
            }
        }
        (frontend, admin)
    }

    fn remove(&self, paths: &[String], mutation: Option<&Mutation>) -> Result<()> {
        let (frontend, admin) = Self::split_paths(paths);
        join(
            self.frontend
                .batch_delete_by_file_paths_in(mutation, &frontend),
            self.admin.batch_delete_by_file_paths_in(mutation, &admin),
        )
    }
}

/// The repository a snippet file belongs to.
#[derive(Clone, Copy)]
enum SnippetTarget {
    Frontend,
    Admin,
}

/// Snippets extracted from one file; `None` leaves the file alone.
struct PreparedSnippets(Option<(SnippetTarget, BTreeMap<String, Snippet>)>);

/// Parses one snippet file without writing anything.
fn extract_snippets(file: &ParsedFile) -> PreparedSnippets {
    let path = file.path();
    if path.contains("/_fixtures/") {
        return PreparedSnippets(None);
    }
    let target = if path.contains(STOREFRONT_SNIPPETS) {
        SnippetTarget::Frontend
    } else if is_admin_snippet_file(path) {
        SnippetTarget::Admin
    } else {
        return PreparedSnippets(None);
    };
    // Only JSON trees can contain snippet objects; other languages under a
    // snippet directory (legacy PHP snippet files) persist an empty set
    // without being parsed.
    let snippets = match file.language() {
        Some(LanguageId::Json) => match file.syntax_tree() {
            Some(tree) => parse_snippet_tree(tree, file.line_index(), path),
            None => BTreeMap::new(),
        },
        _ => BTreeMap::new(),
    };
    PreparedSnippets(Some((target, snippets)))
}

/// Whether `path` is an Administration snippet file: a `.json` file directly
/// inside a `snippet` directory below `Resources/app/administration/`.
pub fn is_admin_snippet_file(path: &str) -> bool {
    if !path.contains(ADMINISTRATION) {
        return false;
    }
    let mut segments = path.rsplit('/');
    let file_name = segments.next().unwrap_or_default();
    segments.next() == Some("snippet") && file_name.ends_with(".json")
}

fn snippets_with_text(repository: &DataIndexer<Snippet>) -> Result<HashMap<String, String>> {
    let snippets = repository.get_all_values_view()?;
    let mut result: HashMap<String, String> = HashMap::new();
    for snippet in snippets.iter() {
        let Some(existing) = result.get_mut(&snippet.key) else {
            result.insert(snippet.key.clone(), snippet.text.clone());
            continue;
        };
        let file = snippet.file.to_lowercase();
        let english = file.contains("en-gb")
            || file.contains("en_gb")
            || file.contains("/en.json")
            || file.contains("/en/");
        if english || existing.is_empty() {
            existing.clone_from(&snippet.text);
        }
    }
    Ok(result)
}

fn join(first: Result<()>, second: Result<()>) -> Result<()> {
    match Error::join([first.err(), second.err()].into_iter().flatten()) {
        Some(error) => Err(error),
        None => Ok(()),
    }
}

impl Indexer for SnippetIndexer {
    fn id(&self) -> &str {
        "snippet.indexer"
    }

    fn index(&self, file: &ParsedFile) -> Result<()> {
        self.persist(file, &extract_snippets(file))
    }

    fn removed_files(&self, paths: &[String]) -> Result<()> {
        self.remove(paths, None)
    }

    fn close(&self) -> Result<()> {
        join(self.frontend.close(), self.admin.close())
    }

    fn clear(&self) -> Result<()> {
        join(self.frontend.clear(), self.admin.clear())
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

impl PreparingIndexer for SnippetIndexer {
    fn prepare(&self, file: &ParsedFile) -> Result<Option<Prepared>> {
        Ok(Some(Box::new(extract_snippets(file))))
    }

    fn index_prepared(&self, file: &ParsedFile, prepared: Option<&Prepared>) -> Result<()> {
        match prepared.and_then(|value| value.downcast_ref::<PreparedSnippets>()) {
            Some(prepared) => self.persist(file, prepared),
            None => self.index(file),
        }
    }
}

impl TransactionalRemover for SnippetIndexer {
    fn removed_files_in(&self, paths: &[String], mutation: &Mutation) -> Result<()> {
        self.remove(paths, Some(mutation))
    }
}

impl TransactionalClearer for SnippetIndexer {
    fn clear_in(&self, mutation: &Mutation) -> Result<()> {
        join(
            self.frontend.clear_in(Some(mutation)),
            self.admin.clear_in(Some(mutation)),
        )
    }
}

impl swlsp_indexer::Revisioned for SnippetIndexer {
    fn revision(&self) -> u64 {
        SnippetIndexer::revision(self)
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn admin_snippet_file_detection() {
        let cases = [
            (
                "/project/src/Resources/app/administration/src/module/test/snippet/en-GB.json",
                true,
            ),
            (
                "/project/src/Resources/app/administration/src/module/test/snippet/en.json",
                true,
            ),
            (
                "/project/src/Resources/app/administration/src/module/test/snippet/de-DE.json",
                true,
            ),
            (
                "/project/src/Resources/app/administration/src/module/test/snippet/fr-FR.json",
                true,
            ),
            (
                "/project/custom/plugins/MyPlugin/src/Resources/app/administration/src/snippet/en-GB.json",
                true,
            ),
            (
                "/project/src/Resources/app/administration/src/module/test/snippet/README.md",
                false,
            ),
            (
                "/project/src/Resources/app/administration/src/module/test/en-GB.json",
                false,
            ),
            ("/project/src/Resources/snippet/en-GB.json", false),
            (
                "/project/src/Resources/app/storefront/src/snippet/en-GB.json",
                false,
            ),
        ];
        for (path, expected) in cases {
            assert_eq!(is_admin_snippet_file(path), expected, "{path}");
        }
    }

    #[test]
    fn indexes_frontend_and_admin_snippets() {
        let dir = tempfile::tempdir().unwrap();
        let indexer = SnippetIndexer::new(dir.path(), None).unwrap();
        let frontend_en = "/p/src/Resources/snippet/en_GB/storefront.en-GB.json";
        let frontend_de = "/p/src/Resources/snippet/de_DE/storefront.de-DE.json";
        let admin = "/p/src/Resources/app/administration/src/snippet/de-DE.json";
        let files = [
            (
                frontend_de,
                r#"{"general": {"title": "Titel", "empty": ""}}"#,
            ),
            (
                frontend_en,
                r#"{"general": {"title": "Title", "empty": ""}}"#,
            ),
            (admin, r#"{"sw-demo": {"label": "Bezeichnung"}}"#),
            (
                "/p/src/Resources/snippet/_fixtures/x.json",
                r#"{"fixture": "x"}"#,
            ),
            (
                "/p/src/Resources/snippet/de_DE/SnippetFile_de_DE.php",
                "<?php return [];",
            ),
        ];
        for (path, source) in files {
            indexer
                .index(&ParsedFile::new(path, source.as_bytes().to_vec()))
                .unwrap();
        }
        let mut keys = indexer.frontend_snippet_keys().unwrap().to_vec();
        keys.sort();
        keys.dedup();
        assert_eq!(keys, ["general.empty", "general.title"]);
        assert_eq!(indexer.frontend_snippet("general.title").unwrap().len(), 2);
        assert_eq!(
            indexer.frontend_snippets_with_text().unwrap()["general.title"],
            "Title"
        );
        assert_eq!(
            indexer.admin_snippets_with_text().unwrap()["sw-demo.label"],
            "Bezeichnung"
        );
        assert_eq!(indexer.admin_snippet("sw-demo.label").unwrap()[0].line, 1);

        indexer
            .removed_files(&[frontend_en.to_owned(), admin.to_owned()])
            .unwrap();
        assert_eq!(
            indexer.frontend_snippets_with_text().unwrap()["general.title"],
            "Titel"
        );
        assert!(indexer.all_admin_snippets().unwrap().is_empty());
        indexer.close().unwrap();
    }

    #[test]
    fn prepared_indexing_matches_direct_indexing() {
        let dir = tempfile::tempdir().unwrap();
        let direct = SnippetIndexer::new(dir.path().join("direct"), None).unwrap();
        let prepared = SnippetIndexer::new(dir.path().join("prepared"), None).unwrap();
        let files = [
            (
                "/p/src/Resources/snippet/en_GB/storefront.en-GB.json",
                r#"{"general": {"title": "Title"}}"#,
            ),
            (
                "/p/src/Resources/app/administration/src/snippet/de-DE.json",
                r#"{"sw-demo": {"label": "Bezeichnung"}}"#,
            ),
        ];
        for (path, source) in files {
            direct
                .index(&ParsedFile::new(path, source.as_bytes().to_vec()))
                .unwrap();
            let file = ParsedFile::new(path, source.as_bytes().to_vec());
            let data = prepared.prepare(&file).unwrap();
            prepared.index_prepared(&file, data.as_ref()).unwrap();
        }
        assert_eq!(direct.all_frontend_snippets().unwrap().len(), 1);
        assert_eq!(direct.all_admin_snippets().unwrap().len(), 1);
        assert_eq!(
            prepared.all_frontend_snippets().unwrap(),
            direct.all_frontend_snippets().unwrap()
        );
        assert_eq!(
            prepared.all_admin_snippets().unwrap(),
            direct.all_admin_snippets().unwrap()
        );
        direct.close().unwrap();
        prepared.close().unwrap();
    }
}
