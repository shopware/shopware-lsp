use std::collections::{BTreeMap, HashMap, HashSet};
use std::path::Path;
use std::sync::{Arc, PoisonError, RwLock};

use swlsp_indexer::{
    DataIndexer, Indexer, Mutation, ParsedFile, Prepared, PreparingIndexer, Result, Store,
    TransactionalClearer, TransactionalRemover, WORKSPACE_SYMBOL_PRIORITY_FRAMEWORK,
    WorkspaceSymbol, WorkspaceSymbolKind, WorkspaceSymbolRange,
};

use crate::model::{Command, Input, InputKind};
use crate::parser::{is_php_command_candidate, parse_php_commands_tree, parse_xml_commands};

type PathSet = Arc<RwLock<HashSet<String>>>;

/// Commands extracted from one file; `None` leaves the file alone.
struct PreparedCommands(Option<ExtractedCommands>);

struct ExtractedCommands {
    /// Every parsed command, in source order.
    commands: Vec<Command>,
    /// Indexes into `commands` that are stored, one per name (last wins),
    /// in first-occurrence order.
    stored: Vec<usize>,
    symbols: Vec<WorkspaceSymbol>,
}

pub struct Index {
    commands: DataIndexer<Command>,
    paths: PathSet,
}

impl Index {
    /// Advanced after every committed change to this index; inspections
    /// declare it as an input (`ProblemAnalyzer::input_revision`).
    pub fn revision(&self) -> u64 {
        self.commands.revision()
    }

    pub fn new(config_dir: impl AsRef<Path>, store: Option<&Store>) -> Result<Self> {
        let commands = DataIndexer::repository(
            config_dir.as_ref().join("console.db"),
            "symfony.console.commands",
            store,
        )?;
        let paths = match commands.get_all_file_paths() {
            Ok(paths) => paths,
            Err(error) => {
                let _ = commands.close();
                return Err(error);
            }
        };
        Ok(Self {
            commands,
            paths: Arc::new(RwLock::new(paths.into_iter().collect())),
        })
    }

    /// Every declaration of a command name (aliases included).
    pub fn get_command(&self, name: &str) -> Result<Vec<Command>> {
        if name.is_empty() {
            return Ok(Vec::new());
        }
        self.commands.get_values(name)
    }

    /// Every command, sorted by name and file.
    pub fn get_commands(&self) -> Result<Vec<Command>> {
        let mut commands = self.commands.get_all_values()?;
        commands.sort_by(|left, right| {
            left.name
                .cmp(&right.name)
                .then_with(|| left.file.cmp(&right.file))
        });
        Ok(commands)
    }

    /// The arguments or options of commands handled by `class` (and
    /// `method`, for method-level commands), unique by name and sorted.
    pub fn inputs_for_target(
        &self,
        class: &str,
        method: &str,
        kind: InputKind,
    ) -> Result<Vec<Input>> {
        if class.is_empty() {
            return Ok(Vec::new());
        }
        let class = class.strip_prefix('\\').unwrap_or(class);
        let commands = self.commands.get_all_values_view()?;
        let mut unique: BTreeMap<String, Input> = BTreeMap::new();
        for command in commands.iter() {
            if !swlsp_textutil::eq_ignore_case(&command.class, class)
                || !command.method.is_empty()
                    && !swlsp_textutil::eq_ignore_case(&command.method, method)
            {
                continue;
            }
            let inputs = match kind {
                InputKind::Argument => &command.arguments,
                InputKind::Option => &command.options,
            };
            for input in inputs {
                unique.insert(input.name.clone(), input.clone());
            }
        }
        Ok(unique.into_values().collect())
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

    /// Parses one file's commands without writing anything. `None` means
    /// the file is neither a candidate nor previously indexed.
    fn extract(&self, file: &ParsedFile) -> PreparedCommands {
        let path = file.path();
        let commands = match file.extension() {
            ".php" if is_php_command_candidate(file.source()) => {
                parse_php_commands_tree(path, file.syntax_tree())
            }
            ".xml" if file.source().contains("console.command") => parse_xml_commands(file),
            ".php" | ".xml" if self.has_path(path) => Vec::new(),
            _ => return PreparedCommands(None),
        };

        // One command per name: a later declaration replaces an earlier one.
        let mut stored: Vec<usize> = Vec::with_capacity(commands.len());
        let mut positions: HashMap<&str, usize> = HashMap::with_capacity(commands.len());
        for (index, command) in commands.iter().enumerate() {
            if command.name.is_empty() {
                continue;
            }
            match positions.get(command.name.as_str()) {
                Some(&position) => stored[position] = index,
                None => {
                    positions.insert(&command.name, stored.len());
                    stored.push(index);
                }
            }
        }
        let symbols = command_workspace_symbols(file, &commands);
        PreparedCommands(Some(ExtractedCommands {
            commands,
            stored,
            symbols,
        }))
    }

    /// Stores extracted commands and their workspace symbols and publishes
    /// the path membership.
    fn persist(&self, file: &ParsedFile, prepared: &PreparedCommands) -> Result<()> {
        let Some(extracted) = &prepared.0 else {
            return Ok(());
        };
        let path = file.path();
        let items = extracted
            .stored
            .iter()
            .filter_map(|&index| extracted.commands.get(index))
            .map(|command| (command.name.as_str(), command));
        let present = !extracted.stored.is_empty();
        self.commands
            .batch_save_items_in(file.mutation(), [(path, items)])?;
        file.add_workspace_symbols(extracted.symbols.iter().cloned());
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
        "symfony.console"
    }

    fn index(&self, file: &ParsedFile) -> Result<()> {
        let prepared = self.extract(file);
        self.persist(file, &prepared)
    }

    fn removed_files(&self, paths: &[String]) -> Result<()> {
        self.commands.batch_delete_by_file_paths(paths)?;
        self.remove_paths(paths, None)
    }

    fn close(&self) -> Result<()> {
        self.commands.close()
    }

    fn clear(&self) -> Result<()> {
        self.commands.clear()?;
        self.publish(None, HashSet::clear)
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
        Ok(Some(Box::new(self.extract(file))))
    }

    fn index_prepared(&self, file: &ParsedFile, prepared: Option<&Prepared>) -> Result<()> {
        match prepared.and_then(|value| value.downcast_ref::<PreparedCommands>()) {
            Some(prepared) => self.persist(file, prepared),
            None => self.index(file),
        }
    }
}

impl TransactionalRemover for Index {
    fn removed_files_in(&self, paths: &[String], mutation: &Mutation) -> Result<()> {
        self.commands
            .batch_delete_by_file_paths_in(Some(mutation), paths)?;
        self.remove_paths(paths, Some(mutation))
    }
}

impl TransactionalClearer for Index {
    fn clear_in(&self, mutation: &Mutation) -> Result<()> {
        self.commands.clear_in(Some(mutation))?;
        self.publish(Some(mutation), HashSet::clear)
    }
}

fn command_workspace_symbols(file: &ParsedFile, commands: &[Command]) -> Vec<WorkspaceSymbol> {
    commands
        .iter()
        .filter(|command| !command.name.is_empty() && !command.file.is_empty())
        .map(|command| {
            let mut container = "Symfony command".to_owned();
            if !command.description.is_empty() {
                container.push_str(" \u{b7} ");
                container.push_str(&command.description);
            }
            WorkspaceSymbol {
                name: command.name.clone(),
                container_name: container,
                aliases: vec![command.class.clone()],
                path: command.file.clone(),
                domain: "symfony.command".to_owned(),
                kind: WorkspaceSymbolKind::FUNCTION,
                priority: WORKSPACE_SYMBOL_PRIORITY_FRAMEWORK,
                range: WorkspaceSymbolRange::from_text(file, command.range),
            }
        })
        .collect()
}

impl swlsp_indexer::Revisioned for Index {
    fn revision(&self) -> u64 {
        Index::revision(self)
    }
}
