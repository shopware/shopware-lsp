//! The persistent component index and its resolved catalog.
//!
//! Extraction happens in [`PreparingIndexer::prepare`], ahead of
//! the workspace write transaction; `index_prepared` only persists.

use std::collections::{HashMap, HashSet};
use std::path::Path;
use std::sync::{Arc, Mutex, RwLock};

use swlsp_indexer::{
    DataIndexer, Indexer, Mutation, ParsedFile, Prepared, PreparingIndexer, Result, Store,
    TransactionalClearer, TransactionalRemover, WORKSPACE_SYMBOL_PRIORITY_FRAMEWORK,
    WorkspaceSymbol, WorkspaceSymbolKind, WorkspaceSymbolRange,
};
use swlsp_php::PhpIndex;
use swlsp_php_semantic::{Flags, Symbol, Visibility};
use swlsp_symfony::ServiceIndex;
use swlsp_syntax::{SyntaxNode, TextRange};
use swlsp_textutil::{eq_ignore_case, lowercase};
use swlsp_twig::{TwigIndexer, template_names};

use crate::config::{
    configuration_in_php, configuration_in_yaml, unique_namespaces, unique_strings,
};
use crate::model::*;
use crate::php::{
    EXPOSE_IN_TEMPLATE, LIVE_ACTION, LIVE_LISTENER, computed_method_name, declarations_in_php,
    exposed_method_name, live_actions_in_php, live_event_references_in_php_impl,
    live_listeners_in_php_impl, props_in_php,
};
use crate::twig::{
    live_action_argument_references_in_twig, live_action_references_in_twig_impl,
    live_event_references_in_twig_impl, props_in_twig_impl, usages_in_twig_impl,
};

const MODERN_SERVICE_TAG: &str = "ux.twig_component";
const LEGACY_SERVICE_TAG: &str = "twig.component";
const RECORD_KEY: &str = "components";
const DEFAULT_NAMESPACE: &str = "App\\Twig\\Components";

#[derive(Default)]
struct Dependencies {
    php: Option<PhpIndex>,
    services: Option<Arc<ServiceIndex>>,
    twig: Option<Arc<TwigIndexer>>,
}

#[derive(Default)]
struct Catalog {
    valid: bool,
    epoch: u64,
    compiled_revision: u64,
    components: Vec<Component>,
    props: HashMap<String, Vec<Prop>>,
    /// `live_listeners` keyed by the PHP index identity and revision.
    live_listeners: Option<(usize, u64, Arc<Vec<LiveListener>>)>,
}

impl Catalog {
    fn invalidate(&mut self) {
        self.valid = false;
        self.epoch += 1;
        self.compiled_revision = 0;
        self.components.clear();
        self.props.clear();
        self.live_listeners = None;
    }
}

/// Symfony UX Twig and Live components of one workspace.
pub struct Index {
    records: DataIndexer<Record>,
    paths: Arc<RwLock<HashSet<String>>>,
    dependencies: RwLock<Dependencies>,
    catalog: Arc<Mutex<Catalog>>,
}

impl std::fmt::Debug for Index {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        f.debug_struct("twigcomponent::Index")
            .finish_non_exhaustive()
    }
}

/// The extraction result of one file.
struct PreparedRecord {
    candidate: bool,
    record: Record,
}

fn read<T>(lock: &RwLock<T>) -> std::sync::RwLockReadGuard<'_, T> {
    lock.read()
        .unwrap_or_else(std::sync::PoisonError::into_inner)
}

fn write<T>(lock: &RwLock<T>) -> std::sync::RwLockWriteGuard<'_, T> {
    lock.write()
        .unwrap_or_else(std::sync::PoisonError::into_inner)
}

fn lock<T>(lock: &Mutex<T>) -> std::sync::MutexGuard<'_, T> {
    lock.lock()
        .unwrap_or_else(std::sync::PoisonError::into_inner)
}

fn after_commit(
    mutation: Option<&Mutation>,
    publish: impl FnOnce() + Send + 'static,
) -> Result<()> {
    match mutation {
        Some(mutation) => mutation.after_commit(publish),
        None => {
            publish();
            Ok(())
        }
    }
}

fn contains_any(content: &str, needles: &[&str]) -> bool {
    needles.iter().any(|needle| content.contains(needle))
}

fn sort_by_name_fold<T>(values: &mut [T], name: impl Fn(&T) -> &str) {
    values.sort_by_cached_key(|value| lowercase(name(value)));
}

impl Index {
    pub fn new(config_dir: impl AsRef<Path>, store: Option<&Store>) -> Result<Self> {
        let records = DataIndexer::repository(
            config_dir.as_ref().join("twig_components.db"),
            "symfony.twig_components.records",
            store,
        )?;
        let paths = match records.get_all_file_paths() {
            Ok(paths) => paths,
            Err(error) => {
                let _ = records.close();
                return Err(error);
            }
        };
        Ok(Index {
            records,
            paths: Arc::new(RwLock::new(paths.into_iter().collect())),
            dependencies: RwLock::default(),
            catalog: Arc::default(),
        })
    }

    pub fn set_dependencies(
        &self,
        php: Option<PhpIndex>,
        services: Option<Arc<ServiceIndex>>,
        twig: Option<Arc<TwigIndexer>>,
    ) {
        *write(&self.dependencies) = Dependencies {
            php,
            services,
            twig,
        };
        self.invalidate_catalog();
    }

    fn php(&self) -> Option<PhpIndex> {
        read(&self.dependencies).php.clone()
    }

    fn services(&self) -> Option<Arc<ServiceIndex>> {
        read(&self.dependencies).services.clone()
    }

    fn twig(&self) -> Option<Arc<TwigIndexer>> {
        read(&self.dependencies).twig.clone()
    }

    // --- indexing ---

    fn prepare_file(file: &ParsedFile) -> Option<PreparedRecord> {
        let path = file.path();
        let content = file.source();
        let mut record = Record {
            file: path.to_owned(),
            ..Record::default()
        };
        let root = || file.syntax_tree().map(|tree| tree.root());
        let candidate = match file.extension() {
            ".php" => {
                let candidate = contains_any(
                    content,
                    &[
                        "AsTwigComponent",
                        "AsLiveComponent",
                        "LiveAction",
                        "LiveArg",
                        "LiveListener",
                        "->emit",
                        "twig_component",
                    ],
                );
                if candidate && let Some(root) = root() {
                    extract_php(path, &root, &mut record);
                }
                candidate
            }
            ".yaml" | ".yml" => {
                let candidate = content.contains("twig_component");
                if candidate && let Some(root) = root() {
                    (record.namespaces, record.anonymous_directories) =
                        configuration_in_yaml(path, &root);
                }
                candidate
            }
            ".twig" => {
                let candidate = contains_any(
                    content,
                    &[
                        "component",
                        "<twig:",
                        "live_action",
                        "data-live-action-param",
                        "data-live-event-param",
                        " props ",
                        "{% props",
                        "{%- props",
                        "@prop",
                    ],
                );
                if candidate && let Some(root) = root() {
                    extract_twig(path, &root, &mut record);
                }
                candidate
            }
            _ => return None,
        };
        Some(PreparedRecord { candidate, record })
    }

    fn persist(&self, file: &ParsedFile, prepared: Option<&PreparedRecord>) -> Result<()> {
        let Some(prepared) = prepared else {
            return Ok(());
        };
        let path = file.path();
        let mutation = file.mutation();
        if !prepared.candidate && !self.has_indexed_path(path) {
            return self.invalidate_after(mutation);
        }
        let present = !prepared.record.is_empty();
        let items: Vec<(&str, &Record)> = if present {
            vec![(RECORD_KEY, &prepared.record)]
        } else {
            Vec::new()
        };
        self.records
            .batch_save_items_in(mutation, [(path, items)])?;
        let paths = self.paths.clone();
        let owned = path.to_owned();
        after_commit(mutation, move || {
            let mut paths = write(&paths);
            if present {
                paths.insert(owned);
            } else {
                paths.remove(&owned);
            }
        })?;
        file.add_workspace_symbols(component_workspace_symbols(file, &prepared.record));
        self.invalidate_after(mutation)
    }

    fn has_indexed_path(&self, path: &str) -> bool {
        read(&self.paths).contains(path)
    }

    fn invalidate_after(&self, mutation: Option<&Mutation>) -> Result<()> {
        let catalog = self.catalog.clone();
        after_commit(mutation, move || lock(&catalog).invalidate())
    }

    fn invalidate_catalog(&self) {
        lock(&self.catalog).invalidate();
    }

    fn remove(&self, paths: &[String], mutation: Option<&Mutation>) -> Result<()> {
        self.records
            .batch_delete_by_file_paths_in(mutation, paths)?;
        let indexed = self.paths.clone();
        let catalog = self.catalog.clone();
        let removed = paths.to_vec();
        after_commit(mutation, move || {
            let mut indexed = write(&indexed);
            for path in &removed {
                indexed.remove(path);
            }
            drop(indexed);
            lock(&catalog).invalidate();
        })
    }

    fn clear_all(&self, mutation: Option<&Mutation>) -> Result<()> {
        self.records.clear_in(mutation)?;
        let indexed = self.paths.clone();
        let catalog = self.catalog.clone();
        after_commit(mutation, move || {
            write(&indexed).clear();
            lock(&catalog).invalidate();
        })
    }

    // --- queries ---

    /// Every resolved component, sorted by name.
    pub fn components(&self) -> Result<Vec<Component>> {
        let (compiled, compiled_revision) = self.compiled_components();
        let epoch = {
            let mut catalog = lock(&self.catalog);
            if catalog.valid && catalog.compiled_revision == compiled_revision {
                return Ok(catalog.components.clone());
            }
            if catalog.valid {
                // The compiled container changed since the catalog was built.
                catalog.invalidate();
            }
            catalog.epoch
        };
        let result = self.build_components(compiled)?;
        let mut catalog = lock(&self.catalog);
        if catalog.epoch == epoch && !catalog.valid {
            catalog.components = result.clone();
            catalog.valid = true;
            catalog.compiled_revision = compiled_revision;
        }
        if catalog.valid && catalog.compiled_revision == compiled_revision {
            return Ok(catalog.components.clone());
        }
        Ok(result)
    }

    fn build_components(&self, compiled: Vec<Declaration>) -> Result<Vec<Component>> {
        let records = self.records.get_all_values_view()?;
        let namespaces = configured_namespaces(&records);
        let mut result: Vec<Component> = records
            .iter()
            .flat_map(|record| &record.declarations)
            .filter_map(|declaration| resolve_declaration(declaration, &namespaces))
            .collect();
        for declaration in self.service_declarations()? {
            result.extend(resolve_declaration(&declaration, &namespaces));
        }
        for declaration in &compiled {
            if let Some(component) = resolve_declaration(declaration, &namespaces) {
                merge_compiled_component(&mut result, component);
            }
        }
        result.extend(self.anonymous_components(&anonymous_directories(&records))?);
        let mut result = unique_components(result);
        sort_components(&mut result);
        Ok(result)
    }

    pub fn find(&self, name: &str) -> Result<Vec<Component>> {
        Ok(self
            .components()?
            .into_iter()
            .filter(|component| component.name == name)
            .collect())
    }

    /// Resolves one declaration against the configured namespaces.
    pub fn resolve_declaration(&self, declaration: &Declaration) -> Result<Option<Component>> {
        let records = self.records.get_all_values_view()?;
        Ok(resolve_declaration(
            declaration,
            &configured_namespaces(&records),
        ))
    }

    pub fn names(&self) -> Result<Vec<String>> {
        let mut seen = HashSet::new();
        let mut result: Vec<String> = self
            .components()?
            .into_iter()
            .map(|component| component.name)
            .filter(|name| !name.is_empty() && seen.insert(name.clone()))
            .collect();
        sort_by_name_fold(&mut result, String::as_str);
        Ok(result)
    }

    pub fn usages(&self, name: &str) -> Result<Vec<Usage>> {
        if name.is_empty() {
            return Ok(Vec::new());
        }
        let records = self.records.get_all_values_view()?;
        let mut result: Vec<Usage> = records
            .iter()
            .flat_map(|record| &record.usages)
            .filter(|usage| usage.name == name)
            .cloned()
            .collect();
        sort_usages(&mut result);
        Ok(result)
    }

    /// Indexed template files implementing `component`.
    pub fn template_files(&self, component: &Component) -> Result<Vec<String>> {
        if component.template.is_empty() {
            return Ok(Vec::new());
        }
        let Some(twig) = self.twig() else {
            return Ok(Vec::new());
        };
        let mut result: Vec<String> = twig
            .get_twig_files_by_rel_path(&component.template)?
            .into_iter()
            .map(|file| file.path)
            .filter(|path| !path.is_empty())
            .collect();
        result.sort();
        result.dedup();
        Ok(result)
    }

    pub fn blocks(&self, name: &str) -> Result<Vec<Block>> {
        let components = self.find(name)?;
        let Some(twig) = self.twig() else {
            return Ok(Vec::new());
        };
        let mut seen = HashSet::new();
        let mut result = Vec::new();
        for component in &components {
            if component.template.is_empty() {
                continue;
            }
            for file in twig.get_twig_files_by_rel_path(&component.template)? {
                for block in file.blocks.values() {
                    if seen.insert(format!("{}\0{}", block.name, file.path)) {
                        result.push(Block {
                            name: block.name.clone(),
                            file: file.path.clone(),
                            line: block.line,
                        });
                    }
                }
            }
        }
        result.sort_by(|left, right| {
            left.name
                .cmp(&right.name)
                .then_with(|| left.file.cmp(&right.file))
        });
        Ok(result)
    }

    /// Template and PHP props of the named component.
    pub fn props(&self, name: &str) -> Result<Vec<Prop>> {
        let epoch = {
            let catalog = lock(&self.catalog);
            if let Some(cached) = catalog.props.get(name) {
                return Ok(cached.clone());
            }
            catalog.epoch
        };
        let components = self.find(name)?;
        let records = self.records.get_all_values_view()?;
        let mut template_paths = HashSet::new();
        let mut component_classes = HashSet::new();
        for component in &components {
            if !component.class.is_empty() {
                component_classes.insert(lowercase(&normalize_class(&component.class)));
            }
            template_paths.extend(self.template_files(component)?);
        }
        let mut indexed = Vec::new();
        for record in records.iter() {
            if template_paths.contains(&record.file) {
                indexed.extend(
                    record
                        .props
                        .iter()
                        .filter(|prop| prop.class.is_empty())
                        .cloned(),
                );
            }
            indexed.extend(
                record
                    .props
                    .iter()
                    .filter(|prop| {
                        !prop.class.is_empty()
                            && component_classes.contains(&lowercase(&normalize_class(&prop.class)))
                    })
                    .cloned(),
            );
        }
        let mut result = self.php_props(&components);
        for semantic in &mut result {
            let matched = indexed.iter().find(|prop| {
                !prop.class.is_empty()
                    && !prop.member.is_empty()
                    && eq_ignore_case(
                        &normalize_class(&prop.class),
                        &normalize_class(&semantic.class),
                    )
                    && eq_ignore_case(&prop.member, &semantic.member)
            });
            if let Some(prop) = matched {
                semantic.name = prop.name.clone();
                semantic.live |= prop.live;
                semantic.writable |= prop.writable;
            }
        }
        result.extend(indexed);
        let mut result = merge_props(result);
        sort_by_name_fold(&mut result, |prop| &prop.name);
        let mut catalog = lock(&self.catalog);
        if catalog.epoch == epoch {
            catalog.props.insert(name.to_owned(), result.clone());
        }
        Ok(result)
    }

    /// Zero-argument public component getters exposed through Twig's cached
    /// `computed.*` proxy.
    pub fn computed(&self, name: &str) -> Result<Vec<Prop>> {
        let components = self.find(name)?;
        let Some(php) = self.php() else {
            return Ok(Vec::new());
        };
        let mut seen_classes = HashSet::new();
        let mut result = Vec::new();
        for component in &components {
            let class = normalize_class(&component.class);
            if class.is_empty() || !seen_classes.insert(lowercase(&class)) {
                continue;
            }
            for method in php.methods(&class) {
                let Some(computed) = computed_method_name(&method.name) else {
                    continue;
                };
                if method.visibility != Visibility::Public
                    || method.flags.has(Flags::STATIC)
                    || !method.parameters.is_empty()
                {
                    continue;
                }
                result.push(Prop {
                    name: computed,
                    ty: method.return_type.to_string(),
                    file: method.path.to_string(),
                    class: class.clone(),
                    range: method.selection_range,
                    ..Prop::default()
                });
            }
        }
        let mut result = merge_props(result);
        sort_by_name_fold(&mut result, |prop| &prop.name);
        Ok(result)
    }

    pub fn computed_for_template(&self, path: &str) -> Result<Vec<Prop>> {
        let mut result = Vec::new();
        for component in self.components_for_template(path)? {
            result.extend(self.computed(&component.name)?);
        }
        let mut result = merge_props(result);
        sort_by_name_fold(&mut result, |prop| &prop.name);
        Ok(result)
    }

    /// Every public, non-static `#[LiveAction]` method visible on the named
    /// Live Component, including inherited and trait methods.
    pub fn live_actions(&self, name: &str) -> Result<Vec<LiveAction>> {
        let components = self.find(name)?;
        let Some(php) = self.php() else {
            return Ok(Vec::new());
        };
        let snapshot = php.semantic_snapshot();
        let records = self.records.get_all_values_view()?;
        let indexed: HashMap<String, &LiveAction> = records
            .iter()
            .flat_map(|record| &record.live_actions)
            .map(|action| (declaration_key(&action.file, action.range), action))
            .collect();
        let mut seen = HashSet::new();
        let mut result = Vec::new();
        for component in &components {
            if !component.live || component.class.is_empty() {
                continue;
            }
            let class = normalize_class(&component.class);
            for method in php.methods(&class) {
                if method.visibility != Visibility::Public
                    || method.flags.has(Flags::STATIC)
                    || !has_attribute(&method, LIVE_ACTION)
                {
                    continue;
                }
                let key = declaration_key(&method.path, method.selection_range);
                if !seen.insert(key.clone()) {
                    continue;
                }
                let mut parameters = semantic_parameters(&method, &snapshot);
                if let Some(parsed) = indexed.get(&key) {
                    apply_parsed_parameters(&mut parameters, &parsed.parameters);
                }
                result.push(LiveAction {
                    name: method.name.to_string(),
                    class: class.clone(),
                    method: method.name.to_string(),
                    file: method.path.to_string(),
                    range: method.selection_range,
                    parameters,
                });
            }
        }
        result.sort_by_cached_key(|action| {
            (
                lowercase(&action.name),
                action.file.clone(),
                action.range.start(),
            )
        });
        Ok(result)
    }

    pub fn live_actions_for_template(&self, path: &str) -> Result<Vec<LiveAction>> {
        let mut seen = HashSet::new();
        let mut result = Vec::new();
        for component in self.components_for_template(path)? {
            for action in self.live_actions(&component.name)? {
                if seen.insert(declaration_key(&action.file, action.range)) {
                    result.push(action);
                }
            }
        }
        result.sort_by_cached_key(|action| (lowercase(&action.name), action.file.clone()));
        Ok(result)
    }

    pub fn live_action_references(
        &self,
        component_name: &str,
        action_name: &str,
    ) -> Result<Vec<LiveActionReference>> {
        let components = self.find(component_name)?;
        let mut template_paths = HashSet::new();
        for component in &components {
            if component.source == SourceKind::AnonymousTemplate && !component.file.is_empty() {
                template_paths.insert(component.file.clone());
            }
            template_paths.extend(self.template_files(component)?);
        }
        let records = self.records.get_all_values_view()?;
        let mut result: Vec<LiveActionReference> = records
            .iter()
            .filter(|record| template_paths.contains(&record.file))
            .flat_map(|record| &record.live_action_references)
            .filter(|reference| eq_ignore_case(&reference.name, action_name))
            .cloned()
            .collect();
        result.sort_by(|left, right| {
            left.file
                .cmp(&right.file)
                .then(left.range.start().cmp(&right.range.start()))
        });
        Ok(result)
    }

    /// Public, non-static `#[LiveListener]` methods visible on indexed Live
    /// Components. Repeatable attributes produce one entry per event.
    pub fn live_listeners(&self) -> Result<Vec<LiveListener>> {
        Ok(self.live_listeners_view()?.as_ref().clone())
    }

    /// [`Self::live_listeners`] as a shared view, memoized per catalog
    /// generation and PHP index revision.
    pub fn live_listeners_view(&self) -> Result<Arc<Vec<LiveListener>>> {
        let Some(php) = self.php() else {
            return Ok(Arc::new(Vec::new()));
        };
        let (_, compiled_revision) = self.compiled_components();
        let identity = php.identity();
        let revision = php.revision();
        let epoch = {
            let catalog = lock(&self.catalog);
            if catalog.valid
                && catalog.compiled_revision == compiled_revision
                && let Some((cached_identity, cached_revision, listeners)) = &catalog.live_listeners
                && *cached_identity == identity
                && *cached_revision == revision
            {
                return Ok(listeners.clone());
            }
            catalog.epoch
        };
        let result = Arc::new(self.build_live_listeners(&php)?);
        let mut catalog = lock(&self.catalog);
        if catalog.valid
            && catalog.epoch == epoch
            && catalog.compiled_revision == compiled_revision
            && php.revision() == revision
        {
            catalog.live_listeners = Some((identity, revision, result.clone()));
        }
        Ok(result)
    }

    fn build_live_listeners(&self, php: &PhpIndex) -> Result<Vec<LiveListener>> {
        let components = self.components()?;
        let snapshot = php.semantic_snapshot();
        let records = self.records.get_all_values_view()?;
        let mut indexed: HashMap<String, Vec<&LiveListener>> = HashMap::new();
        for listener in records.iter().flat_map(|record| &record.live_listeners) {
            indexed
                .entry(declaration_key(&listener.file, listener.method_range))
                .or_default()
                .push(listener);
        }
        let mut seen = HashSet::new();
        let mut result = Vec::new();
        for component in &components {
            if !component.live || component.class.is_empty() {
                continue;
            }
            let class = normalize_class(&component.class);
            for method in php.methods(&class) {
                if method.visibility != Visibility::Public
                    || method.flags.has(Flags::STATIC)
                    || !has_attribute(&method, LIVE_LISTENER)
                {
                    continue;
                }
                let Some(parsed) =
                    indexed.get(&declaration_key(&method.path, method.selection_range))
                else {
                    continue;
                };
                let parameters = semantic_parameters(&method, &snapshot);
                for declaration in parsed {
                    let mut current = parameters.clone();
                    apply_parsed_parameters(&mut current, &declaration.parameters);
                    let key = format!(
                        "{}\0{}\0{}",
                        lowercase(&declaration.name),
                        lowercase(&method.path),
                        range_key(declaration.range)
                    );
                    if !seen.insert(key) {
                        continue;
                    }
                    result.push(LiveListener {
                        name: declaration.name.clone(),
                        class: declaration.class.clone(),
                        method: method.name.to_string(),
                        file: method.path.to_string(),
                        range: declaration.range,
                        method_range: method.selection_range,
                        parameters: current,
                    });
                }
            }
        }
        result.sort_by_cached_key(|listener| {
            (
                lowercase(&listener.name),
                listener.file.clone(),
                listener.range.start(),
            )
        });
        Ok(result)
    }

    pub fn live_event_references(&self, event_name: &str) -> Result<Vec<LiveEventReference>> {
        if event_name.is_empty() {
            return Ok(Vec::new());
        }
        let records = self.records.get_all_values_view()?;
        let mut result: Vec<LiveEventReference> = records
            .iter()
            .flat_map(|record| &record.live_event_references)
            .filter(|reference| eq_ignore_case(&reference.name, event_name))
            .cloned()
            .collect();
        result.sort_by(|left, right| {
            left.file
                .cmp(&right.file)
                .then(left.range.start().cmp(&right.range.start()))
        });
        Ok(result)
    }

    pub fn live_event_names(&self) -> Result<Vec<String>> {
        let mut seen = HashSet::new();
        let mut result: Vec<String> = self
            .live_listeners()?
            .into_iter()
            .map(|listener| listener.name)
            .filter(|name| !name.is_empty() && seen.insert(lowercase(name)))
            .collect();
        sort_by_name_fold(&mut result, String::as_str);
        Ok(result)
    }

    /// Components implemented by the template at `path`.
    pub fn components_for_template(&self, path: &str) -> Result<Vec<Component>> {
        let components = self.components()?;
        let names: HashSet<String> = template_names(path).into_iter().collect();
        Ok(components
            .into_iter()
            .filter(|component| {
                (component.file == path && component.source == SourceKind::AnonymousTemplate)
                    || names.contains(&component.template)
            })
            .collect())
    }

    /// Components implemented by `path` plus their props, including the
    /// props declared in the current (possibly unsaved) template.
    pub fn context_for_template(
        &self,
        path: &str,
        current_root: Option<&SyntaxNode>,
    ) -> Result<(Vec<Component>, Vec<Prop>)> {
        let components = self.components_for_template(path)?;
        if components.is_empty() {
            return Ok((components, Vec::new()));
        }
        let mut props = Vec::new();
        for component in &components {
            props.extend(
                self.props(&component.name)?
                    .into_iter()
                    .filter(|prop| prop.file != path),
            );
        }
        if let Some(root) = current_root {
            props.extend(props_in_twig_impl(path, root));
        }
        let mut props = merge_props(props);
        sort_by_name_fold(&mut props, |prop| &prop.name);
        Ok((components, props))
    }

    fn compiled_components(&self) -> (Vec<Declaration>, u64) {
        let Some(services) = self.services() else {
            return (Vec::new(), 0);
        };
        let (components, revision) = services.get_compiled_twig_components_state();
        let result = components
            .into_iter()
            .filter(|component| !component.name.is_empty())
            .map(|component| Declaration {
                name: component.name,
                class: component.class,
                template: component.template,
                template_from_method: component.template_from_method,
                file: component.path,
                name_range: component.name_range,
                class_range: component.class_range,
                template_range: component.template_range,
                source: SourceKind::CompiledContainer,
                expose_public_props: true,
                live: false,
            })
            .collect();
        (result, revision)
    }

    fn service_declarations(&self) -> Result<Vec<Declaration>> {
        let Some(services) = self.services() else {
            return Ok(Vec::new());
        };
        let mut result = Vec::new();
        let mut seen = HashSet::new();
        for tag in [MODERN_SERVICE_TAG, LEGACY_SERVICE_TAG] {
            for id in services.get_services_by_tag(tag)? {
                let Some(service) = services.get_service_by_id(&id)? else {
                    continue;
                };
                let mut class = normalize_class(&service.class);
                if class.is_empty() {
                    class = normalize_class(&service.id);
                }
                if class.is_empty()
                    || !seen.insert(format!("{}\0{}", lowercase(&class), service.path))
                {
                    continue;
                }
                result.push(Declaration {
                    class,
                    file: service.path,
                    source: SourceKind::Service,
                    expose_public_props: true,
                    ..Declaration::default()
                });
            }
        }
        Ok(result)
    }

    fn anonymous_components(&self, directories: &[String]) -> Result<Vec<Component>> {
        let Some(twig) = self.twig() else {
            return Ok(Vec::new());
        };
        let mut result = Vec::new();
        for template in twig.get_all_template_files()? {
            let Some(name) = anonymous_component_name(&template, directories) else {
                continue;
            };
            for file in twig.get_twig_files_by_rel_path(&template)? {
                result.push(Component {
                    name: name.clone(),
                    template: template.clone(),
                    file: file.path,
                    source: SourceKind::AnonymousTemplate,
                    expose_public_props: true,
                    ..Component::default()
                });
            }
        }
        Ok(result)
    }

    fn php_props(&self, components: &[Component]) -> Vec<Prop> {
        let Some(php) = self.php() else {
            return Vec::new();
        };
        let mut seen_classes = HashSet::new();
        let mut result = Vec::new();
        for component in components {
            let class = normalize_class(&component.class);
            if class.is_empty() || !seen_classes.insert(lowercase(&class)) {
                continue;
            }
            for property in php.properties(&class) {
                let exposed = (component.expose_public_props
                    && property.visibility == Visibility::Public)
                    || has_attribute(&property, EXPOSE_IN_TEMPLATE);
                if !exposed {
                    continue;
                }
                let name = property
                    .name
                    .strip_prefix('$')
                    .unwrap_or(&property.name)
                    .to_owned();
                result.push(Prop {
                    member: name.clone(),
                    name,
                    ty: property.ty.to_string(),
                    file: property.path.to_string(),
                    class: class.clone(),
                    range: property.selection_range,
                    ..Prop::default()
                });
            }
            for method in php.methods(&class) {
                if method.visibility != Visibility::Public
                    || !has_attribute(&method, EXPOSE_IN_TEMPLATE)
                {
                    continue;
                }
                result.push(Prop {
                    name: exposed_method_name(&method.name),
                    ty: method.return_type.to_string(),
                    file: method.path.to_string(),
                    class: class.clone(),
                    member: method.name.to_string(),
                    range: method.selection_range,
                    ..Prop::default()
                });
            }
        }
        result
    }
}

fn extract_php(path: &str, root: &SyntaxNode, record: &mut Record) {
    record.declarations = declarations_in_php(path, root);
    record.props = props_in_php(path, root);
    record.live_actions = live_actions_in_php(path, root);
    record.live_listeners = live_listeners_in_php_impl(path, root);
    (record.live_event_references, record.live_event_arguments) =
        live_event_references_in_php_impl(path, root);
    (record.namespaces, record.anonymous_directories) = configuration_in_php(path, root);
}

fn extract_twig(path: &str, root: &SyntaxNode, record: &mut Record) {
    record.usages = usages_in_twig_impl(path, root);
    record.props = props_in_twig_impl(path, root);
    record.live_action_references = live_action_references_in_twig_impl(path, root);
    record.live_action_arguments = live_action_argument_references_in_twig(path, root);
    (record.live_event_references, record.live_event_arguments) =
        live_event_references_in_twig_impl(path, root);
}

pub(crate) fn component_workspace_symbols(
    file: &ParsedFile,
    record: &Record,
) -> Vec<WorkspaceSymbol> {
    record
        .declarations
        .iter()
        .map(|declaration| {
            let mut container = if declaration.live {
                "Live component".to_owned()
            } else {
                "Twig component".to_owned()
            };
            if !declaration.class.is_empty() {
                container.push_str(" · ");
                container.push_str(&declaration.class);
            }
            WorkspaceSymbol {
                name: declaration.name.clone(),
                container_name: container,
                aliases: vec![declaration.class.clone(), declaration.template.clone()],
                path: declaration.file.clone(),
                domain: "twig.component".to_owned(),
                kind: WorkspaceSymbolKind::CLASS,
                priority: WORKSPACE_SYMBOL_PRIORITY_FRAMEWORK,
                range: WorkspaceSymbolRange::from_text(file, declaration.name_range),
            }
        })
        .collect()
}

fn declaration_key(path: &str, range: TextRange) -> String {
    format!("{}\0{}", lowercase(path), range_key(range))
}

fn has_attribute(symbol: &Symbol, name: &str) -> bool {
    symbol
        .attributes()
        .iter()
        .any(|attribute| eq_ignore_case(&normalize_class(&attribute.name), name))
}

fn semantic_parameters(
    method: &Symbol,
    snapshot: &swlsp_php_semantic::Snapshot,
) -> Vec<LiveActionParameter> {
    method
        .parameters
        .iter()
        .map(|parameter| {
            let name = parameter
                .name
                .strip_prefix('$')
                .unwrap_or(&parameter.name)
                .to_owned();
            LiveActionParameter {
                php_name: name.clone(),
                name,
                ty: parameter.ty.to_string(),
                optional: parameter.optional || parameter.flags.has(Flags::VARIADIC),
                live_arg: false,
                range: snapshot
                    .symbol(parameter.id.as_str())
                    .map_or(parameter.range, |symbol| symbol.selection_range),
            }
        })
        .collect()
}

/// Copies `#[LiveArg]` aliases from the parsed declaration onto the semantic
/// parameters.
fn apply_parsed_parameters(parameters: &mut [LiveActionParameter], parsed: &[LiveActionParameter]) {
    for parameter in parameters {
        if let Some(found) = parsed
            .iter()
            .find(|candidate| eq_ignore_case(&candidate.php_name, &parameter.php_name))
        {
            parameter.name = found.name.clone();
            parameter.live_arg = found.live_arg;
        }
    }
}

fn configured_namespaces(records: &[Record]) -> Vec<Namespace> {
    let mut result: Vec<Namespace> = records
        .iter()
        .flat_map(|record| record.namespaces.iter().cloned())
        .collect();
    result.sort_by(|left, right| {
        left.file
            .cmp(&right.file)
            .then(left.range.start().cmp(&right.range.start()))
    });
    let has_default = result.iter().any(|namespace| {
        eq_ignore_case(&normalize_class(&namespace.class_prefix), DEFAULT_NAMESPACE)
    });
    if !has_default {
        result.push(Namespace {
            class_prefix: DEFAULT_NAMESPACE.to_owned(),
            template_directory: "components/".to_owned(),
            ..Namespace::default()
        });
    }
    unique_namespaces(result)
}

fn anonymous_directories(records: &[Record]) -> Vec<String> {
    let result = unique_strings(
        records
            .iter()
            .flat_map(|record| record.anonymous_directories.iter().cloned())
            .collect(),
    );
    if result.is_empty() {
        return vec!["components/".to_owned()];
    }
    result
}

/// Whether `class` is `prefix` or lies in namespace `prefix`, ignoring case.
fn class_in_namespace(class: &str, prefix: &str) -> bool {
    eq_ignore_case(class, prefix)
        || lowercase(class).starts_with(&format!("{}\\", lowercase(prefix)))
}

pub(crate) fn resolve_declaration(
    declaration: &Declaration,
    namespaces: &[Namespace],
) -> Option<Component> {
    let class = normalize_class(&declaration.class);
    let mut name = normalize_component_name(&declaration.name);
    let mut template = normalize_template(&declaration.template);
    let matched = namespaces
        .iter()
        .find(|namespace| class_in_namespace(&class, &normalize_class(&namespace.class_prefix)));
    if name.is_empty()
        && let Some(matched) = matched
    {
        let rest = class
            .get(normalize_class(&matched.class_prefix).len()..)
            .unwrap_or_default();
        let relative = rest.strip_prefix('\\').unwrap_or(rest);
        name = relative.replace('\\', ":");
        if !matched.name_prefix.is_empty() && !name.is_empty() {
            name = format!("{}:{name}", matched.name_prefix);
        }
    }
    if name.is_empty() {
        return None;
    }
    if template.is_empty() && declaration.template_from_method.is_empty() {
        let mut directory = "components/";
        let mut template_name = name.as_str();
        if let Some(matched) = matched {
            directory = &matched.template_directory;
            if !matched.name_prefix.is_empty() {
                let prefix = format!("{}:", matched.name_prefix);
                template_name = template_name.strip_prefix(&prefix).unwrap_or(template_name);
            }
        }
        template = format!(
            "{}{}.html.twig",
            normalize_directory(directory),
            template_name.replace(':', "/")
        );
    }
    let name_range = if declaration.name_range.is_empty() {
        declaration.class_range
    } else {
        declaration.name_range
    };
    Some(Component {
        name,
        class,
        template,
        template_from_method: declaration.template_from_method.clone(),
        file: declaration.file.clone(),
        name_range,
        class_range: declaration.class_range,
        template_range: declaration.template_range,
        source: declaration.source,
        expose_public_props: declaration.expose_public_props,
        live: declaration.live,
    })
}

fn merge_compiled_component(components: &mut Vec<Component>, compiled: Component) {
    let Some(merged) = components
        .iter_mut()
        .find(|component| eq_ignore_case(&component.name, &compiled.name))
    else {
        components.push(compiled);
        return;
    };
    if !compiled.class.is_empty() {
        merged.class = compiled.class;
        merged.class_range = compiled.class_range;
    }
    if !compiled.template.is_empty() {
        merged.template = compiled.template;
        merged.template_range = compiled.template_range;
    }
    if !compiled.template_from_method.is_empty() {
        merged.template = String::new();
        merged.template_from_method = compiled.template_from_method;
    }
    merged.file = compiled.file;
    merged.source = SourceKind::CompiledContainer;
    merged.name_range = compiled.name_range;
    merged.expose_public_props = compiled.expose_public_props;
}

/// The anonymous component name of `template` below one of `directories`.
pub(crate) fn anonymous_component_name(template: &str, directories: &[String]) -> Option<String> {
    let replaced = template.replace('\\', "/");
    let template = replaced.strip_prefix('/').unwrap_or(&replaced);
    let mut namespace = "";
    let mut relative = template;
    if template.starts_with('@') {
        let slash = template.find('/').filter(|&slash| slash > 1)?;
        namespace = &template[1..slash];
        relative = &template[slash + 1..];
    }
    for directory in directories {
        let normalized = normalize_directory(directory);
        let mut directory = normalized.as_str();
        let mut directory_namespace = "";
        if directory.starts_with('@') {
            let Some(slash) = directory.find('/').filter(|&slash| slash > 1) else {
                continue;
            };
            directory_namespace = &directory[1..slash];
            directory = &directory[slash + 1..];
            if namespace != directory_namespace {
                continue;
            }
        }
        let Some(name) = relative.strip_prefix(directory) else {
            continue;
        };
        let name = name.strip_suffix(".html.twig").unwrap_or(name);
        let name = name.strip_suffix(".twig").unwrap_or(name);
        let name = name.trim_matches('/');
        if name.is_empty() || name == "index" {
            return None;
        }
        let name = name.replace('/', ":");
        let name = name.strip_suffix(":index").unwrap_or(&name);
        if name.is_empty() {
            return None;
        }
        if !namespace.is_empty() && directory_namespace.is_empty() {
            return Some(format!("{namespace}:{name}"));
        }
        return Some(name.to_owned());
    }
    None
}

/// Merges props by case-insensitive name, keeping the first and filling its
/// empty fields from later ones.
pub(crate) fn merge_props(props: Vec<Prop>) -> Vec<Prop> {
    let mut by_name: HashMap<String, usize> = HashMap::with_capacity(props.len());
    let mut result: Vec<Prop> = Vec::with_capacity(props.len());
    for prop in props {
        let key = lowercase(&prop.name);
        if key.is_empty() {
            continue;
        }
        let Some(&index) = by_name.get(&key) else {
            by_name.insert(key, result.len());
            result.push(prop);
            continue;
        };
        let existing = &mut result[index];
        if existing.ty.is_empty() {
            existing.ty = prop.ty;
        }
        if existing.default_value.is_empty() {
            existing.default_value = prop.default_value;
        }
        if existing.description.is_empty() {
            existing.description = prop.description;
        }
        if existing.class.is_empty() {
            existing.class = prop.class;
        }
        if existing.member.is_empty() {
            existing.member = prop.member;
        }
        existing.live |= prop.live;
        existing.writable |= prop.writable;
    }
    result
}

fn unique_components(components: Vec<Component>) -> Vec<Component> {
    let mut seen = HashSet::with_capacity(components.len());
    components
        .into_iter()
        .filter(|component| !component.name.is_empty() && seen.insert(component_key(component)))
        .collect()
}

impl Indexer for Index {
    fn id(&self) -> &str {
        "symfony.twig_components"
    }

    fn index(&self, file: &ParsedFile) -> Result<()> {
        let prepared = Self::prepare_file(file);
        self.persist(file, prepared.as_ref())
    }

    fn removed_files(&self, paths: &[String]) -> Result<()> {
        self.remove(paths, None)
    }

    fn close(&self) -> Result<()> {
        self.records.close()
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
        Ok(Self::prepare_file(file).map(|prepared| Box::new(prepared) as Prepared))
    }

    fn index_prepared(&self, file: &ParsedFile, prepared: Option<&Prepared>) -> Result<()> {
        self.persist(
            file,
            prepared.and_then(|value| value.downcast_ref::<PreparedRecord>()),
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

/// Advanced by every committed change to the component records and by the
/// PHP, service and Twig indexes components resolve through.
impl swlsp_indexer::Revisioned for Index {
    fn revision(&self) -> u64 {
        let dependencies = read(&self.dependencies);
        self.records.revision()
            + dependencies.php.revision()
            + dependencies.services.revision()
            + dependencies.twig.revision()
    }
}
