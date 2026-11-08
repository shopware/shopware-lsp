use std::collections::{HashMap, HashSet};
use std::path::Path;
use std::sync::{Arc, PoisonError, RwLock};

use swlsp_indexer::{
    DataIndexer, Indexer, Mutation, ParsedFile, Result, Store, TransactionalClearer,
    TransactionalRemover,
};
use swlsp_php::PhpIndex;
use swlsp_php_types::{Kind as TypeKind, Type, equal_fold, go_to_lower};

use crate::model::{Message, Occurrence, OccurrenceKind};
use crate::parser::{parse_php, parse_xml, parse_yaml};

type PathSet = Arc<RwLock<HashSet<String>>>;

/// Indexes Messenger handlers and dispatches.
///
/// The in-memory set of paths with persisted occurrences lets a file that
/// stops being a candidate still clear its old rows.
pub struct Index {
    records: DataIndexer<Occurrence>,
    php_index: RwLock<Option<PhpIndex>>,
    paths: PathSet,
}

impl Index {
    pub fn new(config_dir: impl AsRef<Path>, store: Option<&Store>) -> Result<Self> {
        let records = DataIndexer::repository(
            config_dir.as_ref().join("messenger.db"),
            "symfony.messenger.occurrences",
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
            php_index: RwLock::new(None),
            paths: Arc::new(RwLock::new(paths.into_iter().collect())),
        })
    }

    pub fn set_php_index(&self, index: PhpIndex) {
        *self
            .php_index
            .write()
            .unwrap_or_else(PoisonError::into_inner) = Some(index);
    }

    fn php_index(&self) -> Option<PhpIndex> {
        self.php_index
            .read()
            .unwrap_or_else(PoisonError::into_inner)
            .clone()
    }

    /// Every message with at least one occurrence, sorted by name
    /// (case-insensitively). Handlers without an explicit message infer it
    /// from the handler method's first parameter type.
    pub fn messages(&self) -> Result<Vec<Message>> {
        let occurrences = self.records.get_all_values_view()?;
        let php_index = self.php_index();
        let mut grouped: HashMap<String, Message> = HashMap::new();
        for occurrence in occurrences.iter() {
            let names =
                if occurrence.message.is_empty() && occurrence.kind == OccurrenceKind::Handler {
                    inferred_handler_messages(php_index.as_ref(), occurrence)
                } else {
                    vec![occurrence.message.clone()]
                };
            for name in names {
                let name = normalize_name(&name);
                if name.is_empty() {
                    continue;
                }
                let message = grouped.entry(go_to_lower(name)).or_insert_with(|| Message {
                    name: name.to_owned(),
                    occurrences: Vec::new(),
                });
                let mut current = occurrence.clone();
                current.message = name.to_owned();
                message.occurrences.push(current);
            }
        }
        let mut result: Vec<(String, Message)> = grouped
            .into_iter()
            .map(|(key, mut message)| {
                message.occurrences = unique_occurrences(std::mem::take(&mut message.occurrences));
                (key, message)
            })
            .collect();
        // Keys are the lowercased names, so this is Go's order.
        result.sort_by(|left, right| left.0.cmp(&right.0));
        Ok(result.into_iter().map(|(_, message)| message).collect())
    }

    pub fn message(&self, name: &str) -> Result<Option<Message>> {
        let name = normalize_name(name);
        Ok(self
            .messages()?
            .into_iter()
            .find(|message| equal_fold(&message.name, name)))
    }

    pub fn message_names(&self) -> Result<Vec<String>> {
        Ok(self
            .messages()?
            .into_iter()
            .map(|message| message.name)
            .collect())
    }

    pub fn messages_for_handler(
        &self,
        class_name: &str,
        method_name: &str,
    ) -> Result<Vec<Message>> {
        Ok(self
            .messages()?
            .into_iter()
            .filter(|message| {
                message.occurrences.iter().any(|handler| {
                    handler.kind == OccurrenceKind::Handler
                        && equal_fold(&handler.class, class_name)
                        && equal_fold(&handler.method, method_name)
                })
            })
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
        "symfony.messenger"
    }

    fn index(&self, file: &ParsedFile) -> Result<()> {
        let path = file.path();
        if !messenger_candidate(file) && !self.has_path(path) {
            return Ok(());
        }
        let occurrences = match file.extension() {
            ".php" => parse_php(file, self.php_index().as_ref()),
            ".yaml" | ".yml" => parse_yaml(file),
            ".xml" => parse_xml(file),
            _ => return Ok(()),
        };
        // Go keys a map by occurrence; equal keys keep the last value.
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

    fn as_transactional_remover(&self) -> Option<&dyn TransactionalRemover> {
        Some(self)
    }

    fn as_transactional_clearer(&self) -> Option<&dyn TransactionalClearer> {
        Some(self)
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

fn inferred_handler_messages(php_index: Option<&PhpIndex>, occurrence: &Occurrence) -> Vec<String> {
    let Some(php_index) = php_index else {
        return Vec::new();
    };
    if occurrence.class.is_empty() {
        return Vec::new();
    }
    let method = if occurrence.method.is_empty() {
        "__invoke"
    } else {
        occurrence.method.as_str()
    };
    let mut result = Vec::new();
    for symbol in php_index.find_methods(&occurrence.class, method) {
        let Some(parameter) = symbol.parameters.first() else {
            continue;
        };
        result.extend(object_type_names(&parameter.ty));
        if result.is_empty() {
            result.extend(object_type_names(&parameter.native_type));
        }
    }
    unique_names(result)
}

pub(crate) fn object_type_names(value: &Type) -> Vec<String> {
    match value.kind() {
        TypeKind::Union | TypeKind::Intersection => value
            .arguments()
            .iter()
            .flat_map(object_type_names)
            .collect(),
        TypeKind::Object => {
            let name = normalize_name(value.name());
            if name.is_empty() {
                Vec::new()
            } else {
                vec![name.to_owned()]
            }
        }
        _ => Vec::new(),
    }
}

pub(crate) fn occurrence_key(occurrence: &Occurrence) -> String {
    format!(
        "{}:{}:{}:{}:{}:{}:{}",
        occurrence.file,
        occurrence.kind.ordinal(),
        u32::from(occurrence.range.start()),
        u32::from(occurrence.range.end()),
        go_to_lower(&occurrence.message),
        go_to_lower(&occurrence.class),
        go_to_lower(&occurrence.method),
    )
}

/// Drops occurrences with equal keys (keeping the first) and sorts by file
/// and start offset.
pub(crate) fn unique_occurrences(values: Vec<Occurrence>) -> Vec<Occurrence> {
    let mut seen = HashSet::with_capacity(values.len());
    let mut result: Vec<Occurrence> = values
        .into_iter()
        .filter(|value| seen.insert(occurrence_key(value)))
        .collect();
    result.sort_by(|left, right| {
        left.file
            .cmp(&right.file)
            .then(left.range.start().cmp(&right.range.start()))
    });
    result
}

/// Normalized, case-insensitively unique names sorted case-insensitively.
pub(crate) fn unique_names(values: Vec<String>) -> Vec<String> {
    let mut seen = HashSet::with_capacity(values.len());
    let mut result: Vec<String> = Vec::new();
    for value in values {
        let value = normalize_name(&value);
        if value.is_empty() || !seen.insert(go_to_lower(value)) {
            continue;
        }
        result.push(value.to_owned());
    }
    result.sort_by_cached_key(|value| go_to_lower(value));
    result
}

pub(crate) fn normalize_name(value: &str) -> &str {
    let value = value.trim();
    value.strip_prefix('\\').unwrap_or(value)
}

fn messenger_candidate(file: &ParsedFile) -> bool {
    let content = file.source();
    match file.extension() {
        ".php" => {
            content.contains("AsMessageHandler")
                || content.contains("MessageSubscriberInterface")
                || content.contains("getHandledMessages")
                || content.contains("messenger.message_handler")
                || content.contains("->dispatch")
        }
        ".yaml" | ".yml" | ".xml" => content.contains("messenger.message_handler"),
        _ => false,
    }
}
