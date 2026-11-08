//! Event record persistence and queries (port of Go `event/index.go`).

use std::collections::{HashMap, HashSet};
use std::path::Path;
use std::sync::{Arc, Mutex, PoisonError, RwLock};

use swlsp_indexer::{
    DataIndexer, Indexer, Mutation, ParsedFile, Result, Store, TransactionalClearer,
    TransactionalRemover,
};
use swlsp_parser_php::query as phpquery;
use swlsp_php::{NameResolver, PhpIndex};
use swlsp_php_types::{Kind as TypeKind, Type, equal_fold, go_to_lower};
use swlsp_syntax::{TextRange, TextSize};

use crate::model::{Event, Occurrence, OccurrenceKind, Record};
use crate::parser::{
    is_php_event_candidate, parse_class_constants, parse_php, parse_xml, parse_yaml,
    resolved_class_name,
};

type PathSet = Arc<RwLock<HashSet<String>>>;

#[derive(Default)]
struct ConstantCache {
    revision: u64,
    values: HashMap<String, String>,
}

/// Indexes Symfony event listeners (subscribers, `#[AsEventListener]`,
/// `kernel.event_listener` service tags) and EventDispatcher dispatches.
///
/// The in-memory set of paths with persisted records lets a file that stops
/// being a candidate still clear its old rows.
pub struct Index {
    records: DataIndexer<Record>,
    php_index: RwLock<Option<PhpIndex>>,
    paths: PathSet,
    constants: Mutex<ConstantCache>,
}

impl Index {
    pub fn new(config_dir: impl AsRef<Path>, store: Option<&Store>) -> Result<Self> {
        let records = DataIndexer::repository(
            config_dir.as_ref().join("events.db"),
            "symfony.events.records",
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
            constants: Mutex::new(ConstantCache::default()),
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

    /// Every event, sorted by name case-insensitively. Constant expressions
    /// and listener parameter types are resolved through the PHP index.
    pub fn events(&self) -> Result<Vec<Event>> {
        let records = self.records.get_all_values_view()?;
        let php_index = self.php_index();
        let mut grouped: HashMap<String, Event> = HashMap::new();
        for record in records.iter() {
            let mut event_type = record.event_type.clone();
            if event_type.is_empty() {
                event_type = record_event_type(php_index.as_ref(), record);
            }
            let mut name = record.name.clone();
            if name.is_empty() {
                name = self.constant_value(php_index.as_ref(), &record.expression);
            }
            if name.is_empty() && record.expression.is_empty() {
                name = event_type.clone();
            }
            let name = normalize_name(&name).to_owned();
            if name.is_empty() {
                continue;
            }
            let current = grouped.entry(go_to_lower(&name)).or_insert_with(|| Event {
                name: name.clone(),
                ..Event::default()
            });
            if event_type.is_empty() && looks_like_class_name(&name) {
                event_type = name.clone();
            }
            if current.event_type.is_empty() {
                current.event_type = event_type.clone();
            }
            for occurrence in &record.occurrences {
                let mut occurrence = occurrence.clone();
                if occurrence.event_type.is_empty() {
                    occurrence.event_type = event_type.clone();
                }
                if current.event_type.is_empty() {
                    current.event_type = occurrence.event_type.clone();
                }
                current.occurrences.push(occurrence);
            }
        }
        let mut result: Vec<(String, Event)> = grouped
            .into_iter()
            .map(|(key, mut event)| {
                event.occurrences = unique_occurrences(std::mem::take(&mut event.occurrences));
                (key, event)
            })
            .collect();
        // Keys are the lowercased names, so this is Go's order.
        result.sort_by(|left, right| left.0.cmp(&right.0));
        Ok(result.into_iter().map(|(_, event)| event).collect())
    }

    pub fn event(&self, name: &str) -> Result<Option<Event>> {
        let name = normalize_name(name);
        if name.is_empty() {
            return Ok(None);
        }
        Ok(self
            .events()?
            .into_iter()
            .find(|event| equal_fold(&event.name, name)))
    }

    /// The listener declared at `offset` in `path`.
    pub fn listener_at(&self, path: &str, offset: TextSize) -> Result<Option<Occurrence>> {
        let records = self.records.get_all_values_view()?;
        for record in records.iter() {
            for occurrence in &record.occurrences {
                if occurrence.kind != OccurrenceKind::Listener || occurrence.file != path {
                    continue;
                }
                if contains_offset(occurrence.range, offset)
                    || contains_offset(occurrence.method_range, offset)
                {
                    return Ok(Some(occurrence.clone()));
                }
            }
        }
        Ok(None)
    }

    /// Events with a listener on `class` (and `method`, when not empty).
    pub fn events_for_listener(&self, class: &str, method: &str) -> Result<Vec<Event>> {
        let class = normalize_name(class);
        if class.is_empty() {
            return Ok(Vec::new());
        }
        Ok(self
            .events()?
            .into_iter()
            .filter(|event| {
                event.occurrences.iter().any(|listener| {
                    listener.kind == OccurrenceKind::Listener
                        && equal_fold(&listener.class, class)
                        && (method.is_empty() || equal_fold(&listener.method, method))
                })
            })
            .collect())
    }

    /// The string value of a `Class::CONSTANT` expression, read from the
    /// class's source file and cached per PHP index revision.
    fn constant_value(&self, php_index: Option<&PhpIndex>, expression: &str) -> String {
        let expression = normalize_name(expression);
        let Some(php_index) = php_index else {
            return String::new();
        };
        let Some(separator) = expression.rfind("::") else {
            return String::new();
        };
        if separator < 1 || separator + 2 >= expression.len() {
            return String::new();
        }
        let revision = php_index.revision();
        let key = go_to_lower(expression);
        let mut cache = self
            .constants
            .lock()
            .unwrap_or_else(PoisonError::into_inner);
        if cache.revision != revision {
            cache.revision = revision;
            cache.values.clear();
        }
        if let Some(value) = cache.values.get(&key) {
            return value.clone();
        }
        let class_name = &expression[..separator];
        let Some(symbol) = php_index
            .find_class(class_name)
            .filter(|symbol| !symbol.path.is_empty() && !symbol.path.starts_with("phpstub://"))
        else {
            cache.values.insert(key, String::new());
            return String::new();
        };
        let path = symbol.path.to_string();
        let Ok(source) = std::fs::read(&path) else {
            cache.values.insert(key, String::new());
            return String::new();
        };
        let file = ParsedFile::new(path.clone(), source);
        let Some(tree) = file.syntax_tree() else {
            cache.values.insert(key, String::new());
            return String::new();
        };
        let root = tree.root();
        let resolver = NameResolver::new(&root);
        for class in phpquery::classes(&root) {
            let resolved = resolved_class_name(Some(&class), &resolver);
            if !equal_fold(&resolved, class_name) {
                continue;
            }
            for constant in parse_class_constants(&class, &resolved, &path) {
                cache
                    .values
                    .insert(go_to_lower(&constant.expression), constant.value);
            }
            break;
        }
        cache.values.get(&key).cloned().unwrap_or_default()
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
        "symfony.event"
    }

    fn index(&self, file: &ParsedFile) -> Result<()> {
        let path = file.path();
        let records = match file.extension() {
            ".php" => {
                if !is_php_event_candidate(file.source()) && !self.has_path(path) {
                    return Ok(());
                }
                parse_php(file)
            }
            ".yaml" | ".yml" | ".xml" => {
                if !file.source().contains("kernel.event_listener") {
                    if !self.has_path(path) {
                        return Ok(());
                    }
                    Vec::new()
                } else if file.extension() == ".xml" {
                    parse_xml(file)
                } else {
                    parse_yaml(file)
                }
            }
            _ => return Ok(()),
        };
        // Go keys a map by record key; equal keys keep the last value.
        let mut items: Vec<(String, Record)> = Vec::with_capacity(records.len());
        let mut positions: HashMap<String, usize> = HashMap::with_capacity(records.len());
        for record in records {
            let key = record_key(&record);
            if key.is_empty() {
                continue;
            }
            match positions.get(&key) {
                Some(&position) => items[position].1 = record,
                None => {
                    positions.insert(key.clone(), items.len());
                    items.push((key, record));
                }
            }
        }
        let present = !items.is_empty();
        self.records.batch_save_items_in(
            file.mutation(),
            [(
                path,
                items.iter().map(|(key, record)| (key.as_str(), record)),
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

pub(crate) fn record_key(record: &Record) -> String {
    if !record.name.is_empty() {
        return format!("name:{}", go_to_lower(normalize_name(&record.name)));
    }
    if !record.expression.is_empty() {
        return format!("expression:{}", go_to_lower(&record.expression));
    }
    if let Some(occurrence) = record.occurrences.first() {
        let target = if occurrence.class.is_empty() {
            &occurrence.service
        } else {
            &occurrence.class
        };
        if !target.is_empty() || !occurrence.method.is_empty() {
            return format!(
                "inferred:{}",
                go_to_lower(&format!("{target}::{}", occurrence.method))
            );
        }
    }
    String::new()
}

pub(crate) fn normalize_name(name: &str) -> &str {
    let name = name.trim();
    name.strip_prefix('\\').unwrap_or(name)
}

pub(crate) fn looks_like_class_name(name: &str) -> bool {
    name.contains('\\') && !name.contains([' ', '.', ':', '-'])
}

fn contains_offset(range: TextRange, offset: TextSize) -> bool {
    !range.is_empty() && offset >= range.start() && offset <= range.end()
}

/// Drops occurrences with equal file, range, class and method (keeping the
/// first) and sorts by file and start offset.
pub(crate) fn unique_occurrences(occurrences: Vec<Occurrence>) -> Vec<Occurrence> {
    let mut seen = HashSet::with_capacity(occurrences.len());
    let mut result: Vec<Occurrence> = occurrences
        .into_iter()
        .filter(|occurrence| {
            seen.insert(format!(
                "{}:{}:{}:{}:{}",
                occurrence.file,
                u32::from(occurrence.range.start()),
                u32::from(occurrence.range.end()),
                occurrence.class,
                occurrence.method
            ))
        })
        .collect();
    result.sort_by(|left, right| {
        left.file
            .cmp(&right.file)
            .then(left.range.start().cmp(&right.range.start()))
    });
    result
}

fn record_event_type(php_index: Option<&PhpIndex>, record: &Record) -> String {
    let Some(php_index) = php_index else {
        return String::new();
    };
    for occurrence in &record.occurrences {
        if !occurrence.event_type.is_empty() {
            return normalize_name(&occurrence.event_type).to_owned();
        }
        if occurrence.class.is_empty() || occurrence.method.is_empty() {
            continue;
        }
        for method in php_index.find_methods(&occurrence.class, &occurrence.method) {
            let Some(parameter) = method.parameters.first() else {
                continue;
            };
            let event_type = named_event_type(&parameter.ty);
            if !event_type.is_empty() {
                return event_type;
            }
        }
    }
    String::new()
}

fn named_event_type(value: &Type) -> String {
    if value.is_unknown() {
        return String::new();
    }
    if value.kind() == TypeKind::Object && !value.name().is_empty() {
        return normalize_name(value.name()).to_owned();
    }
    value
        .arguments()
        .iter()
        .map(named_event_type)
        .find(|name| !name.is_empty())
        .unwrap_or_default()
}
