use std::collections::{BTreeMap, HashMap, HashSet};
use std::path::Path;
use std::sync::{Arc, PoisonError, RwLock};

use swlsp_indexer::{
    DataIndexer, Indexer, Mutation, ParsedFile, Result, Store, TransactionalClearer,
    TransactionalRemover, WORKSPACE_SYMBOL_PRIORITY_FRAMEWORK, WorkspaceSymbol,
    WorkspaceSymbolKind, WorkspaceSymbolRange,
};

use crate::model::{Command, Input, InputKind};
use crate::parser::{is_php_command_candidate, parse_php_commands_tree, parse_xml_commands};

type PathSet = Arc<RwLock<HashSet<String>>>;

pub struct Index {
    commands: DataIndexer<Command>,
    paths: PathSet,
}

impl Index {
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
            if !crate::equal_fold(&command.class, class)
                || !command.method.is_empty() && !crate::equal_fold(&command.method, method)
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
        let path = file.path();
        let commands = match file.extension() {
            ".php" if is_php_command_candidate(file.source()) => {
                parse_php_commands_tree(path, file.syntax_tree())
            }
            ".xml" if file.source().contains("console.command") => parse_xml_commands(file),
            ".php" | ".xml" if self.has_path(path) => Vec::new(),
            _ => return Ok(()),
        };

        // Go keys a map by command name; equal names keep the last command.
        let mut items: Vec<(&str, &Command)> = Vec::with_capacity(commands.len());
        let mut positions: HashMap<&str, usize> = HashMap::with_capacity(commands.len());
        for command in commands.iter().filter(|command| !command.name.is_empty()) {
            match positions.get(command.name.as_str()) {
                Some(&position) => items[position].1 = command,
                None => {
                    positions.insert(&command.name, items.len());
                    items.push((&command.name, command));
                }
            }
        }
        let present = !items.is_empty();
        self.commands
            .batch_save_items_in(file.mutation(), [(path, items.iter().copied())])?;
        add_command_workspace_symbols(file, &commands);
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

    fn as_transactional_remover(&self) -> Option<&dyn TransactionalRemover> {
        Some(self)
    }

    fn as_transactional_clearer(&self) -> Option<&dyn TransactionalClearer> {
        Some(self)
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

fn add_command_workspace_symbols(file: &ParsedFile, commands: &[Command]) {
    let symbols: Vec<WorkspaceSymbol> = commands
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
        .collect();
    file.add_workspace_symbols(symbols);
}
