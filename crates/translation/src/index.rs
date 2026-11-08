//! The translation catalogue index (port of Go `indexer.go` and
//! `workspace_symbols.go`).

use std::collections::{BTreeMap, HashMap, HashSet};
use std::path::Path;

use swlsp_indexer::{
    DataIndexer, Indexer, Mutation, ParsedFile, Result, Store, TransactionalClearer,
    TransactionalRemover, WORKSPACE_SYMBOL_PRIORITY_FRAMEWORK,
    WORKSPACE_SYMBOL_PRIORITY_FRAMEWORK_MEMBER, WorkspaceSymbol, WorkspaceSymbolKind,
    WorkspaceSymbolPosition, WorkspaceSymbolRange,
};
use swlsp_php_types::{equal_fold, go_to_lower};

use crate::parser::{
    catalogue_metadata, parse_compiled_php_catalogue, parse_php_resource, parse_xml_resource,
    parse_yaml_resource,
};
use crate::{Message, normalize_domain, sort_messages, split_storage_key, storage_key};

/// Persists translation messages of Symfony catalogue resources.
pub struct Index {
    pub(crate) messages: DataIndexer<Message>,
}

impl Index {
    pub fn new(config_dir: impl AsRef<Path>, store: Option<&Store>) -> Result<Self> {
        let messages = DataIndexer::repository(
            config_dir.as_ref().join("translations.db"),
            "symfony.translations",
            store,
        )?;
        Ok(Self { messages })
    }

    /// Messages of `key` in `domain`, ordered by file and position.
    pub fn messages_for(&self, domain: &str, key: &str) -> Result<Vec<Message>> {
        if domain.is_empty() || key.is_empty() {
            return Ok(Vec::new());
        }
        let mut messages = self.messages.get_values(&storage_key(domain, key))?;
        sort_messages(&mut messages);
        Ok(messages)
    }

    pub fn has_message(&self, domain: &str, key: &str) -> Result<bool> {
        Ok(!self.messages_for(domain, key)?.is_empty())
    }

    /// Distinct domains (first spelling wins), sorted case-insensitively.
    pub fn domains(&self) -> Result<Vec<String>> {
        let values = self.messages.get_all_values_view()?;
        let mut unique: HashMap<String, &str> = HashMap::new();
        for message in values.iter() {
            if message.domain.is_empty() {
                continue;
            }
            unique
                .entry(go_to_lower(&message.domain))
                .or_insert(&message.domain);
        }
        let mut domains: Vec<(String, String)> = unique
            .into_iter()
            .map(|(lower, domain)| (lower, domain.to_owned()))
            .collect();
        domains.sort_by(|left, right| left.0.cmp(&right.0));
        Ok(domains.into_iter().map(|(_, domain)| domain).collect())
    }

    /// Sorted message keys of `domain`.
    pub fn keys(&self, domain: &str) -> Result<Vec<String>> {
        if domain.is_empty() {
            return Ok(Vec::new());
        }
        let keys = self.messages.get_all_keys_view()?;
        let domain = go_to_lower(normalize_domain(domain));
        let mut result: Vec<String> = keys
            .iter()
            .filter_map(|value| split_storage_key(value))
            .filter(|(stored, _)| *stored == domain)
            .map(|(_, key)| key.to_owned())
            .collect();
        result.sort();
        Ok(result)
    }

    pub fn domain_messages(&self, domain: &str) -> Result<Vec<Message>> {
        if domain.is_empty() {
            return Ok(Vec::new());
        }
        let values = self.messages.get_all_values_view()?;
        let domain = normalize_domain(domain);
        let mut result: Vec<Message> = values
            .iter()
            .filter(|message| equal_fold(&message.domain, domain))
            .cloned()
            .collect();
        sort_messages(&mut result);
        Ok(result)
    }

    /// Every message, ordered by key, domain, locale, file and line.
    pub fn messages(&self) -> Result<Vec<Message>> {
        let mut values = self.messages.get_all_values()?;
        values.sort_by(|left, right| {
            left.key
                .cmp(&right.key)
                .then_with(|| left.domain.cmp(&right.domain))
                .then_with(|| left.locale.cmp(&right.locale))
                .then_with(|| left.file.cmp(&right.file))
                .then(left.line.cmp(&right.line))
        });
        Ok(values)
    }

    fn remove(&self, paths: &[String], mutation: Option<&Mutation>) -> Result<()> {
        if paths.is_empty() {
            return Ok(());
        }
        self.messages.batch_delete_by_file_paths_in(mutation, paths)
    }
}

impl Indexer for Index {
    fn id(&self) -> &str {
        "translation.indexer"
    }

    fn index(&self, file: &ParsedFile) -> Result<()> {
        let Some(metadata) = catalogue_metadata(file.path()) else {
            return Ok(());
        };
        let messages = match file.extension() {
            _ if metadata.compiled => parse_compiled_php_catalogue(file, &metadata),
            ".php" => parse_php_resource(file, &metadata),
            ".yaml" | ".yml" => parse_yaml_resource(file, &metadata),
            ".xml" | ".xlf" | ".xliff" => parse_xml_resource(file, &metadata),
            _ => Vec::new(),
        };
        // A repeated storage key keeps the last message (Go map semantics).
        let mut items: BTreeMap<String, &Message> = BTreeMap::new();
        for message in &messages {
            if message.domain.is_empty() || message.key.is_empty() {
                continue;
            }
            items.insert(storage_key(&message.domain, &message.key), message);
        }
        self.messages
            .batch_save_items_in(file.mutation(), [(file.path(), items)])?;
        add_message_workspace_symbols(file, &messages);
        Ok(())
    }

    fn removed_files(&self, paths: &[String]) -> Result<()> {
        self.remove(paths, None)
    }

    fn close(&self) -> Result<()> {
        self.messages.close()
    }

    fn clear(&self) -> Result<()> {
        self.messages.clear()
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
        self.remove(paths, Some(mutation))
    }
}

impl TransactionalClearer for Index {
    fn clear_in(&self, mutation: &Mutation) -> Result<()> {
        self.messages.clear_in(Some(mutation))
    }
}

fn add_message_workspace_symbols(file: &ParsedFile, messages: &[Message]) {
    let mut result = Vec::with_capacity(messages.len() * 2);
    let mut domains: HashSet<(&str, &str)> = HashSet::new();
    for message in messages {
        if message.key.is_empty() || message.file.is_empty() {
            continue;
        }
        let range = WorkspaceSymbolRange {
            start: WorkspaceSymbolPosition {
                line: message.line as i32,
                character: message.character as i32,
            },
            end: WorkspaceSymbolPosition {
                line: message.end_line as i32,
                character: message.end_character as i32,
            },
        };
        if !message.domain.is_empty()
            && domains.insert((message.domain.as_str(), message.locale.as_str()))
        {
            result.push(WorkspaceSymbol {
                name: message.domain.clone(),
                container_name: format!("Translation domain · {}", message.locale),
                path: message.file.clone(),
                domain: "translation.domain".to_owned(),
                kind: WorkspaceSymbolKind::NAMESPACE,
                priority: WORKSPACE_SYMBOL_PRIORITY_FRAMEWORK,
                range,
                ..WorkspaceSymbol::default()
            });
        }
        result.push(WorkspaceSymbol {
            name: message.key.clone(),
            container_name: format!("Translation · {} · {}", message.domain, message.locale),
            path: message.file.clone(),
            domain: "translation.message".to_owned(),
            kind: WorkspaceSymbolKind::STRING,
            priority: WORKSPACE_SYMBOL_PRIORITY_FRAMEWORK_MEMBER,
            range,
            ..WorkspaceSymbol::default()
        });
    }
    file.add_workspace_symbols(result);
}
