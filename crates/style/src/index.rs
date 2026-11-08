use std::collections::BTreeMap;
use std::path::Path;

use swlsp_indexer::{
    DataIndexer, Error, Indexer, Mutation, ParsedFile, Prepared, PreparingIndexer, Result, Store,
    TransactionalClearer, TransactionalRemover,
};
use swlsp_syntax::LineIndex;
use swlsp_uriutil::filepath;

use crate::classes::{class_declarations, class_usages};
use crate::model::{ByteRange, ClassCatalog, ClassOccurrence, SourcePosition};
use crate::variables::{
    VariableCatalog, VariableOccurrence, analyze_variables, normalize_variable_name,
    theme_variables,
};

/// Static CSS class declarations and usages plus global SCSS variable and
/// theme configuration declarations, keyed by name.
pub struct Index {
    declarations: DataIndexer<ClassCatalog>,
    usages: DataIndexer<ClassCatalog>,
    variables: DataIndexer<VariableCatalog>,
}

#[derive(Default)]
struct PreparedStyles {
    declarations: Vec<ClassOccurrence>,
    usages: Vec<ClassOccurrence>,
    variable_declarations: Vec<VariableOccurrence>,
    selected: bool,
}

fn join(results: impl IntoIterator<Item = Result<()>>) -> Result<()> {
    match Error::join(results.into_iter().filter_map(Result::err)) {
        Some(error) => Err(error),
        None => Ok(()),
    }
}

fn source_positions(lines: &LineIndex, range: ByteRange) -> (SourcePosition, SourcePosition) {
    let position = |offset: u32| {
        let (line, character) = lines.position_utf16(offset);
        SourcePosition { line, character }
    };
    (position(range.start), position(range.end))
}

impl Index {
    pub fn new(config_dir: impl AsRef<Path>, store: Option<&Store>) -> Result<Self> {
        let db = config_dir.as_ref().join("styles.db");
        let declarations = DataIndexer::repository(&db, "style.class_declarations", store)?;
        let usages = match DataIndexer::repository(&db, "style.class_usages", store) {
            Ok(usages) => usages,
            Err(error) => {
                let _ = declarations.close();
                return Err(error);
            }
        };
        let variables = match DataIndexer::repository(&db, "style.variable_declarations", store) {
            Ok(variables) => variables,
            Err(error) => {
                let _ = declarations.close();
                let _ = usages.close();
                return Err(error);
            }
        };
        Ok(Self {
            declarations,
            usages,
            variables,
        })
    }

    fn prepare_styles(&self, file: &ParsedFile) -> Result<PreparedStyles> {
        let extension = file.extension();
        let is_theme_config = filepath::base(file.path()).eq_ignore_ascii_case("theme.json");
        let mut prepared = PreparedStyles {
            selected: matches!(extension, ".scss" | ".twig" | ".html" | ".vue") || is_theme_config,
            ..PreparedStyles::default()
        };
        if !prepared.selected {
            return Ok(prepared);
        }
        let Some(tree) = file.syntax_tree() else {
            return Ok(prepared);
        };
        let root = tree.root();
        let lines = file.line_index();
        if matches!(extension, ".scss" | ".vue") {
            prepared.declarations = class_declarations(file.path(), &root);
            for occurrence in &mut prepared.declarations {
                (occurrence.start, occurrence.end) = source_positions(lines, occurrence.range);
            }
        }
        if extension == ".scss" {
            prepared.variable_declarations =
                analyze_variables(file.path(), &root).global_declarations;
            for occurrence in &mut prepared.variable_declarations {
                (occurrence.start, occurrence.end) = source_positions(lines, occurrence.range);
            }
        }
        if is_theme_config {
            let fields = swlsp_theme::parse_theme_config_tree(tree, lines, file.path())
                .map_err(Error::msg)?;
            prepared.variable_declarations = theme_variables(&fields);
        }
        if matches!(extension, ".twig" | ".html" | ".vue") {
            prepared.usages = class_usages(file.path(), &root);
            for occurrence in &mut prepared.usages {
                (occurrence.start, occurrence.end) = source_positions(lines, occurrence.range);
            }
        }
        Ok(prepared)
    }

    /// Declarations of class `name`, sorted by file and offset.
    pub fn declarations(&self, name: &str) -> Result<Vec<ClassOccurrence>> {
        if name.is_empty() {
            return Ok(Vec::new());
        }
        Ok(flatten_catalogs(&self.declarations.get_values_view(name)?))
    }

    /// Markup usages of class `name`, sorted by file and offset.
    pub fn usages(&self, name: &str) -> Result<Vec<ClassOccurrence>> {
        if name.is_empty() {
            return Ok(Vec::new());
        }
        Ok(flatten_catalogs(&self.usages.get_values_view(name)?))
    }

    /// Global declarations of variable `name` (Sass-normalized).
    pub fn variable_declarations(&self, name: &str) -> Result<Vec<VariableOccurrence>> {
        if name.is_empty() {
            return Ok(Vec::new());
        }
        let catalogs = self
            .variables
            .get_values_view(&normalize_variable_name(name))?;
        let mut result: Vec<VariableOccurrence> = catalogs
            .iter()
            .flat_map(|catalog| catalog.occurrences.iter().cloned())
            .collect();
        result.sort_by(|left, right| {
            (&left.file, left.range.start).cmp(&(&right.file, right.range.start))
        });
        Ok(result)
    }

    /// Whether a declaration exists outside `excluded_path`. Excluding the
    /// current document lets an unsaved removal win over its stale on-disk
    /// index entry.
    pub fn has_variable_declaration(&self, name: &str, excluded_path: &str) -> Result<bool> {
        if name.is_empty() {
            return Ok(false);
        }
        let catalogs = self
            .variables
            .get_values_view(&normalize_variable_name(name))?;
        let excluded = filepath::clean(excluded_path);
        Ok(catalogs
            .iter()
            .any(|catalog| excluded == "." || filepath::clean(&catalog.file) != excluded))
    }

    /// The class declaration repository (oracle and diagnostics access).
    pub fn declaration_repository(&self) -> &DataIndexer<ClassCatalog> {
        &self.declarations
    }

    /// The class usage repository (oracle and diagnostics access).
    pub fn usage_repository(&self) -> &DataIndexer<ClassCatalog> {
        &self.usages
    }

    /// The variable declaration repository (oracle and diagnostics access).
    pub fn variable_repository(&self) -> &DataIndexer<VariableCatalog> {
        &self.variables
    }

    fn remove(&self, paths: &[String], mutation: Option<&Mutation>) -> Result<()> {
        join([
            self.declarations
                .batch_delete_by_file_paths_in(mutation, paths),
            self.usages.batch_delete_by_file_paths_in(mutation, paths),
            self.variables
                .batch_delete_by_file_paths_in(mutation, paths),
        ])
    }

    fn clear_all(&self, mutation: Option<&Mutation>) -> Result<()> {
        join([
            self.declarations.clear_in(mutation),
            self.usages.clear_in(mutation),
            self.variables.clear_in(mutation),
        ])
    }
}

/// Groups occurrences by name in source order (Go `catalogWrite`).
fn class_catalogs(path: &str, occurrences: &[ClassOccurrence]) -> BTreeMap<String, ClassCatalog> {
    let mut catalogs: BTreeMap<String, ClassCatalog> = BTreeMap::new();
    for occurrence in occurrences
        .iter()
        .filter(|occurrence| !occurrence.name.is_empty())
    {
        let catalog = catalogs
            .entry(occurrence.name.clone())
            .or_insert_with(|| ClassCatalog {
                name: occurrence.name.clone(),
                file: path.to_owned(),
                occurrences: Vec::new(),
            });
        catalog.occurrences.push(occurrence.clone());
    }
    catalogs
}

/// Groups variable occurrences by normalized name in source order.
fn variable_catalogs(
    path: &str,
    occurrences: &[VariableOccurrence],
) -> BTreeMap<String, VariableCatalog> {
    let mut catalogs: BTreeMap<String, VariableCatalog> = BTreeMap::new();
    for occurrence in occurrences {
        let name = normalize_variable_name(&occurrence.name);
        if name.is_empty() {
            continue;
        }
        let catalog = catalogs
            .entry(name.clone())
            .or_insert_with(|| VariableCatalog {
                name,
                file: path.to_owned(),
                occurrences: Vec::new(),
            });
        catalog.occurrences.push(occurrence.clone());
    }
    catalogs
}

fn flatten_catalogs(catalogs: &[ClassCatalog]) -> Vec<ClassOccurrence> {
    let mut result: Vec<ClassOccurrence> = catalogs
        .iter()
        .flat_map(|catalog| catalog.occurrences.iter().cloned())
        .collect();
    result.sort_by(|left, right| {
        (&left.file, left.range.start).cmp(&(&right.file, right.range.start))
    });
    result
}

impl Indexer for Index {
    fn id(&self) -> &str {
        "style.classes"
    }

    fn index(&self, file: &ParsedFile) -> Result<()> {
        let prepared = self.prepare(file)?;
        self.index_prepared(file, prepared.as_ref())
    }

    fn removed_files(&self, paths: &[String]) -> Result<()> {
        self.remove(paths, None)
    }

    fn close(&self) -> Result<()> {
        join([
            self.declarations.close(),
            self.usages.close(),
            self.variables.close(),
        ])
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
        Ok(Some(Box::new(self.prepare_styles(file)?)))
    }

    fn index_prepared(&self, file: &ParsedFile, prepared: Option<&Prepared>) -> Result<()> {
        let Some(prepared) = prepared.and_then(|value| value.downcast_ref::<PreparedStyles>())
        else {
            return Ok(());
        };
        if !prepared.selected {
            return Ok(());
        }
        let path = file.path();
        let mutation = file.mutation();
        self.declarations.batch_save_items_in(
            mutation,
            [(path, &class_catalogs(path, &prepared.declarations))],
        )?;
        self.usages
            .batch_save_items_in(mutation, [(path, &class_catalogs(path, &prepared.usages))])?;
        self.variables.batch_save_items_in(
            mutation,
            [(
                path,
                &variable_catalogs(path, &prepared.variable_declarations),
            )],
        )
    }
}

impl TransactionalRemover for Index {
    fn removed_files_in(&self, paths: &[String], mutation: &Mutation) -> Result<()> {
        self.remove(paths, Some(mutation))
    }
}

impl TransactionalClearer for Index {
    fn clear_in(&self, mutation: &Mutation) -> Result<()> {
        self.clear_all(Some(mutation))
    }
}

#[cfg(test)]
mod tests;
