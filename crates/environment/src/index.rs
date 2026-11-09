use std::collections::{BTreeMap, HashMap, HashSet};
use std::path::Path;
use std::sync::{Arc, PoisonError, RwLock};

use swlsp_indexer::{
    DataIndexer, Indexer, Mutation, ParsedFile, Prepared, PreparingIndexer, Result, Store,
    SupplementalPathIndexer, TransactionalClearer, TransactionalRemover,
};

use crate::model::{Occurrence, OccurrenceKind, Variable};
use crate::parser::{
    extension, is_docker_compose_path, is_dockerfile_path, is_dot_env_path, parse_docker_compose,
    parse_dockerfile, parse_dot_env,
};
use crate::reference::{ENV_PREFIX, php_references, references};

type PathSet = Arc<RwLock<HashSet<String>>>;

/// Indexes environment declarations and Symfony references.
///
/// The in-memory set of paths with persisted occurrences lets a file that
/// stops being a candidate (for example a removed `%env(` reference) still
/// clear its old rows without a lookup for every rejected file.
pub struct Index {
    records: DataIndexer<Occurrence>,
    paths: PathSet,
}

/// A file's occurrences keyed for storage, prepared ahead of the write
/// transaction.
struct PreparedOccurrences {
    items: Vec<(String, Occurrence)>,
}

/// Environment files the scanner would not select on its own.
pub fn should_index_path(path: &str) -> bool {
    is_dot_env_path(path) || is_dockerfile_path(path)
}

impl Index {
    pub fn new(config_dir: impl AsRef<Path>, store: Option<&Store>) -> Result<Self> {
        let records = DataIndexer::repository(
            config_dir.as_ref().join("environment.db"),
            "symfony.environment.occurrences",
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

    /// Every variable with at least one occurrence, sorted by name.
    pub fn variables(&self) -> Result<Vec<Variable>> {
        let occurrences = self.records.get_all_values_view()?;
        let mut grouped: BTreeMap<&str, Variable> = BTreeMap::new();
        for occurrence in occurrences
            .iter()
            .filter(|occurrence| !occurrence.name.is_empty())
        {
            let variable = grouped.entry(&occurrence.name).or_insert_with(|| Variable {
                name: occurrence.name.clone(),
                ..Variable::default()
            });
            match occurrence.kind {
                OccurrenceKind::Declaration => variable.declarations.push(occurrence.clone()),
                OccurrenceKind::Reference => variable.references.push(occurrence.clone()),
            }
        }
        Ok(grouped
            .into_values()
            .map(|mut variable| {
                sort_occurrences(&mut variable.declarations);
                sort_occurrences(&mut variable.references);
                variable
            })
            .collect())
    }

    pub fn variable(&self, name: &str) -> Result<Option<Variable>> {
        Ok(self
            .variables()?
            .into_iter()
            .find(|variable| variable.name == name))
    }

    /// Names of variables with at least one declaration, sorted.
    pub fn names(&self) -> Result<Vec<String>> {
        Ok(self
            .variables()?
            .into_iter()
            .filter(|variable| !variable.declarations.is_empty())
            .map(|variable| variable.name)
            .collect())
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

    /// Collects the file's occurrences keyed for storage without writing
    /// anything; `None` for files that neither are candidates nor have
    /// persisted occurrences to clear.
    fn extract(&self, file: &ParsedFile) -> Option<PreparedOccurrences> {
        let path = file.path();
        if !environment_candidate(file) && !self.has_path(path) {
            return None;
        }
        let source = file.source();
        let mut occurrences = if is_dot_env_path(path) {
            parse_dot_env(source)
        } else if is_dockerfile_path(path) {
            parse_dockerfile(source)
        } else if is_docker_compose_path(path) {
            parse_docker_compose(source)
        } else {
            Vec::new()
        };
        if supports_symfony_env_reference(path) {
            occurrences.extend(
                references(source)
                    .into_iter()
                    .map(|reference| reference.into_occurrence()),
            );
            if extension(path).eq_ignore_ascii_case(".php")
                && let Some(tree) = file.syntax_tree()
            {
                occurrences.extend(
                    php_references(&tree.root())
                        .into_iter()
                        .map(|reference| reference.into_occurrence()),
                );
            }
        }

        // One item per occurrence key; a repeated key keeps the last value.
        let mut items: Vec<(String, Occurrence)> = Vec::with_capacity(occurrences.len());
        let mut positions: HashMap<String, usize> = HashMap::with_capacity(occurrences.len());
        for mut occurrence in occurrences {
            occurrence.file = path.to_owned();
            let key = occurrence_key(&occurrence);
            match positions.get(&key) {
                Some(&position) => items[position].1 = occurrence,
                None => {
                    positions.insert(key.clone(), items.len());
                    items.push((key, occurrence));
                }
            }
        }
        Some(PreparedOccurrences { items })
    }

    /// Writes prepared occurrences for `file` and publishes whether the path
    /// has any after commit.
    fn persist(&self, file: &ParsedFile, prepared: Option<&PreparedOccurrences>) -> Result<()> {
        let Some(PreparedOccurrences { items }) = prepared else {
            return Ok(());
        };
        let path = file.path();
        let present = !items.is_empty();
        self.records.batch_save_items_in(
            file.mutation(),
            [(
                path,
                items
                    .iter()
                    .map(|(key, occurrence)| (key.as_str(), occurrence)),
            )],
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
        "symfony.environment"
    }

    fn index(&self, file: &ParsedFile) -> Result<()> {
        self.persist(file, self.extract(file).as_ref())
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

    fn as_preparing(&self) -> Option<&dyn PreparingIndexer> {
        Some(self)
    }

    fn as_supplemental_path(&self) -> Option<&dyn SupplementalPathIndexer> {
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
        Ok(Some(Box::new(self.extract(file))))
    }

    fn index_prepared(&self, file: &ParsedFile, prepared: Option<&Prepared>) -> Result<()> {
        match prepared.and_then(|value| value.downcast_ref::<Option<PreparedOccurrences>>()) {
            Some(prepared) => self.persist(file, prepared.as_ref()),
            None => self.index(file),
        }
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

impl SupplementalPathIndexer for Index {
    fn should_enter_directory(&self, _path: &str) -> bool {
        false
    }

    fn should_index_path(&self, path: &str) -> bool {
        should_index_path(path)
    }
}

fn environment_candidate(file: &ParsedFile) -> bool {
    let path = file.path();
    if is_dot_env_path(path) || is_dockerfile_path(path) || is_docker_compose_path(path) {
        return true;
    }
    let source = file.source();
    supports_symfony_env_reference(path)
        && (source.contains(ENV_PREFIX)
            || extension(path).eq_ignore_ascii_case(".php")
                && (has_php_env_call_candidate(source)
                    || source.contains("Autowire") && source.contains("env")))
}

/// Whether `env` is followed (after optional white space) by `(`.
fn has_php_env_call_candidate(source: &str) -> bool {
    let bytes = source.as_bytes();
    let mut cursor = 0;
    while let Some(relative) = source[cursor..].find("env") {
        let mut next = cursor + relative + "env".len();
        while next < bytes.len() && matches!(bytes[next], b' ' | b'\t' | b'\r' | b'\n') {
            next += 1;
        }
        if bytes.get(next) == Some(&b'(') {
            return true;
        }
        cursor += relative + "env".len();
    }
    false
}

fn supports_symfony_env_reference(path: &str) -> bool {
    let extension = extension(path);
    [".php", ".yaml", ".yml", ".xml"]
        .iter()
        .any(|candidate| extension.eq_ignore_ascii_case(candidate))
}

fn occurrence_key(occurrence: &Occurrence) -> String {
    format!(
        "{}:{}:{}:{}:{}",
        occurrence.file,
        occurrence.kind.ordinal(),
        u32::from(occurrence.range.start()),
        u32::from(occurrence.range.end()),
        occurrence.name
    )
}

fn sort_occurrences(values: &mut [Occurrence]) {
    values.sort_by(|left, right| {
        left.file
            .cmp(&right.file)
            .then(left.range.start().cmp(&right.range.start()))
    });
}

#[cfg(test)]
mod tests {
    use super::*;

    fn index_file(index: &Index, path: &Path, source: &str) {
        index
            .index(&ParsedFile::new(
                path.to_str().unwrap(),
                source.as_bytes().to_vec(),
            ))
            .unwrap();
    }

    #[test]
    fn collects_environment_declarations_and_references() {
        let dir = tempfile::tempdir().unwrap();
        let root = dir.path();
        let index = Index::new(root, None).unwrap();
        let files = [
            (
                root.join(".env"),
                "\nAPP_ENV=dev\nexport DATABASE_URL = \"mysql://localhost/app\" # local database\nEMPTY=\n",
            ),
            (root.join("env.env"), "\nAPP_ENV=test\n"),
            (
                root.join("Dockerfile"),
                "\nENV APP_RUNTIME Runtime\nENV PHP_MEMORY_LIMIT=512M APP_DEBUG=1\n",
            ),
            (
                root.join("docker-compose.override.yaml"),
                "\nservices:\n  app:\n    environment:\n      DATABASE_HOST: database\n      - APP_SECRET=not-secret\n      - WORKER_ENABLED\n    labels:\n      example: value\n",
            ),
            (
                root.join("config").join("services.yaml"),
                "\nparameters:\n  database_url: '%env(resolve:DATABASE_URL)%'\n  memory_limit: '%env(int:PHP_MEMORY_LIMIT)%'\n",
            ),
            (
                root.join("src").join("Config.php"),
                "<?php\n#[Autowire('%env(default:app.env:APP_ENV)%')]\n#[Autowire(env: 'resolve:DATABASE_URL')]\nfinal class Config {}\nenv('bool:APP_ENV');\n",
            ),
        ];
        for (path, source) in &files {
            index_file(&index, path, source);
        }

        assert_eq!(
            index.names().unwrap(),
            [
                "APP_DEBUG",
                "APP_ENV",
                "APP_RUNTIME",
                "APP_SECRET",
                "DATABASE_HOST",
                "DATABASE_URL",
                "EMPTY",
                "PHP_MEMORY_LIMIT",
                "WORKER_ENABLED"
            ]
        );

        let database_url = index.variable("DATABASE_URL").unwrap().unwrap();
        assert_eq!(database_url.declarations.len(), 1);
        assert_eq!(database_url.declarations[0].value, "mysql://localhost/app");
        assert_eq!(database_url.references.len(), 2);
        assert_eq!(database_url.references[0].processors, ["resolve"]);
        assert_eq!(database_url.references[1].processors, ["resolve"]);

        let app_env = index.variable("APP_ENV").unwrap().unwrap();
        assert_eq!(app_env.declarations.len(), 2);
        assert_eq!(app_env.references.len(), 2);
        assert_eq!(app_env.references[0].processors, ["default", "app.env"]);
        assert_eq!(app_env.references[1].processors, ["bool"]);
        index.close().unwrap();
    }

    #[test]
    fn php_env_call_candidate_accepts_whitespace_before_call() {
        assert!(has_php_env_call_candidate("<?php env ('APP_ENV');"));
        assert!(has_php_env_call_candidate("<?php \\env\n('APP_ENV');"));
        assert!(!has_php_env_call_candidate("<?php $environment = 'dev';"));
    }

    #[test]
    fn restores_and_removes_environment_occurrences() {
        let dir = tempfile::tempdir().unwrap();
        let path = dir.path().join(".env.local");
        let index = Index::new(dir.path(), None).unwrap();
        index_file(&index, &path, "APP_SECRET=temporary");
        index.close().unwrap();

        let restored = Index::new(dir.path(), None).unwrap();
        let variable = restored.variable("APP_SECRET").unwrap().unwrap();
        assert_eq!(variable.declarations.len(), 1);
        restored
            .removed_files(&[path.to_str().unwrap().to_owned()])
            .unwrap();
        assert!(restored.variable("APP_SECRET").unwrap().is_none());
        restored.close().unwrap();
    }

    #[test]
    fn known_paths_clear_when_references_disappear() {
        let dir = tempfile::tempdir().unwrap();
        let path = dir.path().join("services.yaml");
        let index = Index::new(dir.path(), None).unwrap();
        index_file(&index, &path, "a: '%env(APP_ENV)%'");
        assert!(index.has_path(path.to_str().unwrap()));
        assert_eq!(
            index.variable("APP_ENV").unwrap().unwrap().references.len(),
            1
        );
        index_file(&index, &path, "a: b");
        assert!(index.variable("APP_ENV").unwrap().is_none());
        assert!(!index.has_path(path.to_str().unwrap()));
        index.close().unwrap();
    }

    #[test]
    fn coordinated_writes_publish_paths_after_commit() {
        let dir = tempfile::tempdir().unwrap();
        let store = Store::open(dir.path().join("indexes.db")).unwrap();
        let index = Index::new(dir.path(), Some(&store)).unwrap();
        let path = dir.path().join(".env");
        index_file(&index, &path, "APP_ENV=dev");
        let path = path.to_str().unwrap().to_owned();
        assert!(index.has_path(&path));

        let cancel = swlsp_indexer::CancelToken::new();
        let mutation = store.begin_mutation(&cancel).unwrap();
        index
            .removed_files_in(std::slice::from_ref(&path), &mutation)
            .unwrap();
        mutation.rollback().unwrap();
        assert!(index.has_path(&path));
        assert!(index.variable("APP_ENV").unwrap().is_some());

        let mutation = store.begin_mutation(&cancel).unwrap();
        index
            .removed_files_in(std::slice::from_ref(&path), &mutation)
            .unwrap();
        assert!(index.has_path(&path));
        mutation.commit().unwrap();
        assert!(!index.has_path(&path));
        assert!(index.variable("APP_ENV").unwrap().is_none());
        store.close().unwrap();
    }

    #[test]
    fn scanner_prepared_indexing_matches_direct_indexing() {
        let root = tempfile::tempdir().unwrap();
        let cache = tempfile::tempdir().unwrap();
        let files = [
            (
                root.path().join(".env"),
                "APP_ENV=dev\nDATABASE_URL=mysql://db\n",
            ),
            (
                root.path().join("config").join("services.yaml"),
                "parameters:\n  url: '%env(resolve:DATABASE_URL)%'\n",
            ),
            (
                root.path().join("src").join("Config.php"),
                "<?php\n#[Autowire(env: 'APP_ENV')]\nfinal class Config {}\n",
            ),
        ];
        for (path, source) in &files {
            std::fs::create_dir_all(path.parent().unwrap()).unwrap();
            std::fs::write(path, source).unwrap();
        }

        let store = Store::open(cache.path().join("indexes.db")).unwrap();
        let scanned = Arc::new(Index::new(cache.path(), Some(&store)).unwrap());
        let mut scanner = swlsp_indexer::FileScanner::new(
            root.path().to_str().unwrap(),
            cache.path().join("scanner.db"),
            Some(store.clone()),
        )
        .unwrap();
        scanner.add_indexer(scanned.clone());
        let cancel = swlsp_indexer::CancelToken::new();
        scanner.index_all(&cancel).unwrap();

        let direct_dir = tempfile::tempdir().unwrap();
        let direct = Index::new(direct_dir.path(), None).unwrap();
        for (path, source) in &files {
            index_file(&direct, path, source);
        }
        let scanned_variables = scanned.variables().unwrap();
        assert_eq!(scanned.names().unwrap(), ["APP_ENV", "DATABASE_URL"]);
        assert_eq!(scanned_variables, direct.variables().unwrap());

        // A tracked file that stops referencing variables is cleared through
        // the prepared path as well.
        let (services, _) = &files[1];
        std::fs::write(services, "parameters:\n  url: mysql\n").unwrap();
        scanner
            .index_files(&cancel, &[services.to_str().unwrap()])
            .unwrap();
        index_file(&direct, services, "parameters:\n  url: mysql\n");
        assert!(!scanned.has_path(services.to_str().unwrap()));
        assert_eq!(scanned.variables().unwrap(), direct.variables().unwrap());
        assert!(
            scanned
                .variable("DATABASE_URL")
                .unwrap()
                .unwrap()
                .references
                .is_empty()
        );

        scanner.close().unwrap();
        direct.close().unwrap();
        scanned.close().unwrap();
        store.close().unwrap();
    }

    #[test]
    fn supplemental_path_selection() {
        assert!(should_index_path("/project/.env"));
        assert!(should_index_path("/project/.env.test"));
        assert!(should_index_path("/project/config/env.env"));
        assert!(should_index_path("/project/Dockerfile"));
        assert!(should_index_path("/project/Dockerfile.dev"));
        assert!(!should_index_path("/project/services.yaml"));
        assert!(!should_index_path("/project/config.json"));
        let dir = tempfile::tempdir().unwrap();
        let index = Index::new(dir.path(), None).unwrap();
        assert!(!index.should_enter_directory("/project/vendor"));
        index.close().unwrap();
    }

    #[test]
    fn path_classification() {
        assert!(
            is_dot_env_path("/project/.ENV.local")
                && is_dockerfile_path("/project/DOCKERFILE.dev")
                && is_docker_compose_path("/project/Docker-Compose.override.YAML")
                && supports_symfony_env_reference("/project/config/services.XML")
        );
    }
}
