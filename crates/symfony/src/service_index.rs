//! The Symfony service index: services, parameters, and
//! prototypes from XML, YAML, and PHP configuration files, merged with the
//! compiled container.

use std::collections::{HashMap, HashSet};
use std::path::Path;
use std::sync::atomic::{AtomicU64, Ordering};
use std::sync::{Arc, Mutex, PoisonError, RwLock};

use swlsp_indexer::{
    DataIndexer, Indexer, Mutation, ParsedFile, Prepared, PreparingIndexer, Result, Store,
    TransactionalClearer, TransactionalRemover,
};
use swlsp_php::PhpIndex;
use swlsp_php_semantic::{Flags, Symbol, SymbolKind};

use crate::container_catalog::ContainerCatalog;
use crate::model::{
    ContainerTwigComponent, ContainerTwigGlobal, Location, Parameter, Service, ServicePrototype,
};
use crate::path_match::path_matches_config_pattern;
use crate::util::Lowercase;
use crate::workspace_symbols::add_service_workspace_symbols;

const PATH_SERVICES: u8 = 1;
const PATH_PARAMETERS: u8 = 1 << 1;
const PATH_PROTOTYPES: u8 = 1 << 2;

#[derive(Clone, Copy, Default)]
struct PathState {
    touched: u8,
    present: u8,
}

#[derive(Default)]
struct PrototypeCache {
    services: Option<Arc<Vec<Service>>>,
    php_revision: u64,
    own_revision: u64,
}

#[derive(Default)]
struct ServicesByTypeCache {
    entries: Option<HashMap<String, Vec<Service>>>,
    php_revision: u64,
    own_revision: u64,
    container_revision: u64,
}

type PathKinds = Arc<RwLock<HashMap<String, u8>>>;

/// Records grouped per source path, keyed within each path.
type PathWrites<T> = Vec<(String, Vec<(String, T)>)>;

/// Service configuration parsed ahead of the write transaction.
struct PreparedServices {
    services: Vec<Service>,
    parameters: Vec<Parameter>,
    service_write: PathWrites<Service>,
    parameter_write: PathWrites<Parameter>,
    prototype_write: PathWrites<ServicePrototype>,
}

/// Maintains an index of service IDs from XML, YAML, and PHP configurator
/// files.
pub struct ServiceIndex {
    project_root: String,
    pub(crate) services: DataIndexer<Service>,
    pub(crate) parameters: DataIndexer<Parameter>,
    pub(crate) prototypes: DataIndexer<ServicePrototype>,
    container: Arc<ContainerCatalog>,
    php_index: RwLock<Option<PhpIndex>>,
    prototype_revision: AtomicU64,
    prototype_cache: Mutex<PrototypeCache>,
    services_by_type: Mutex<ServicesByTypeCache>,
    indexed_paths: PathKinds,
}

impl std::fmt::Debug for ServiceIndex {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        f.debug_struct("ServiceIndex")
            .field("project_root", &self.project_root)
            .finish()
    }
}

impl ServiceIndex {
    /// Advanced after every change to the indexed services, parameters,
    /// prototypes or the compiled container catalog; inspections declare it
    /// as an input (`ProblemAnalyzer::input_revision`).
    pub fn revision(&self) -> u64 {
        self.services.revision()
            + self.parameters.revision()
            + self.prototypes.revision()
            + self.container.revision()
            + self.prototype_revision.load(Ordering::SeqCst)
    }

    /// Opens the service repositories in `store`, or in owned databases
    /// under `config_dir` when no workspace store is given.
    pub fn new(
        project_root: &str,
        config_dir: impl AsRef<Path>,
        store: Option<&Store>,
    ) -> Result<Self> {
        let config_dir = config_dir.as_ref();
        let services = DataIndexer::repository(
            config_dir.join("symfony.service"),
            "symfony.services",
            store,
        )?;
        let parameters = DataIndexer::repository(
            config_dir.join("symfony.parameter"),
            "symfony.parameters",
            store,
        )?;
        let prototypes = DataIndexer::repository(
            config_dir.join("symfony.prototype"),
            "symfony.service_prototypes",
            store,
        )?;
        let mut indexed_paths: HashMap<String, u8> = HashMap::new();
        for (repository_paths, kind) in [
            (services.get_all_file_paths(), PATH_SERVICES),
            (parameters.get_all_file_paths(), PATH_PARAMETERS),
            (prototypes.get_all_file_paths(), PATH_PROTOTYPES),
        ] {
            let paths = match repository_paths {
                Ok(paths) => paths,
                Err(error) => {
                    let _ = services.close();
                    let _ = parameters.close();
                    let _ = prototypes.close();
                    return Err(error);
                }
            };
            for path in paths {
                *indexed_paths.entry(path).or_default() |= kind;
            }
        }
        Ok(Self {
            project_root: project_root.to_owned(),
            services,
            parameters,
            prototypes,
            container: Arc::new(ContainerCatalog::new(project_root)),
            php_index: RwLock::new(None),
            prototype_revision: AtomicU64::new(0),
            prototype_cache: Mutex::new(PrototypeCache::default()),
            services_by_type: Mutex::new(ServicesByTypeCache::default()),
            indexed_paths: Arc::new(RwLock::new(indexed_paths)),
        })
    }

    pub fn project_root(&self) -> &str {
        &self.project_root
    }

    /// The compiled container catalog fed by the compiled container index.
    pub fn container_catalog(&self) -> &Arc<ContainerCatalog> {
        &self.container
    }

    /// Enables inheritance-aware resolution for `instanceof` configurator
    /// rules and prototype expansion.
    pub fn set_php_index(&self, php_index: Option<PhpIndex>) {
        *self
            .php_index
            .write()
            .unwrap_or_else(PoisonError::into_inner) = php_index;
        self.prototype_revision.fetch_add(1, Ordering::SeqCst);
    }

    pub fn php_index(&self) -> Option<PhpIndex> {
        self.php_index
            .read()
            .unwrap_or_else(PoisonError::into_inner)
            .clone()
    }

    pub fn get_compiled_twig_components(&self) -> Vec<ContainerTwigComponent> {
        self.container.get_twig_components()
    }

    /// Compiled Twig components plus the container revision.
    pub fn get_compiled_twig_components_state(&self) -> (Vec<ContainerTwigComponent>, u64) {
        self.container.get_twig_components_state()
    }

    /// Reparses the currently discovered Symfony container from disk. Normal
    /// editor use is updated by the file scanner.
    pub fn reload_compiled_container(&self) -> std::io::Result<()> {
        self.container.find_and_load_container()
    }

    /// Globals registered on the compiled Twig service.
    pub fn get_twig_globals(&self) -> Vec<ContainerTwigGlobal> {
        self.container.get_twig_globals()
    }

    /// Whether a compiled container is loaded. Without one, services and
    /// parameters registered at compile time are unknown.
    pub fn container_exists(&self) -> bool {
        self.container.container_exists()
    }

    fn has_indexed_path(&self, path: &str) -> bool {
        self.indexed_paths
            .read()
            .unwrap_or_else(PoisonError::into_inner)
            .get(path)
            .is_some_and(|kinds| *kinds != 0)
    }

    fn bump(&self) {
        self.prototype_revision.fetch_add(1, Ordering::SeqCst);
    }

    fn publish_paths(
        &self,
        mutation: Option<&Mutation>,
        update: impl FnOnce(&mut HashMap<String, u8>) + Send + 'static,
    ) -> Result<()> {
        let paths = self.indexed_paths.clone();
        let publish = move || update(&mut paths.write().unwrap_or_else(PoisonError::into_inner));
        match mutation {
            Some(mutation) => mutation.after_commit(publish),
            None => {
                publish();
                Ok(())
            }
        }
    }

    /// Parses the file's services, parameters, and prototypes without
    /// writing anything; `None` for files the service index ignores.
    fn extract(&self, file: &ParsedFile) -> Option<PreparedServices> {
        let path = file.path();
        if path.contains("var/cache") {
            return None;
        }
        let (services, parameters, prototypes) = match file.extension() {
            ".xml" => crate::xml_services::parse_xml_service_config_tree(
                path,
                file.syntax_tree(),
                file.line_index(),
            ),
            ".yaml" | ".yml" => crate::yaml_services::parse_yaml_service_config_tree(
                path,
                file.syntax_tree(),
                file.line_index(),
            ),
            ".php" => {
                if !is_php_service_config_candidate(file.source()) {
                    if !self.has_indexed_path(path) {
                        return None;
                    }
                    (Vec::new(), Vec::new(), Vec::new())
                } else {
                    let tree = file.syntax_tree()?;
                    let config = crate::php_service_config(path, &tree.root(), file.line_index());
                    (config.services, config.parameters, config.prototypes)
                }
            }
            _ => return None,
        };

        // One entry per id; a repeated id keeps the last value.
        let mut service_write: PathWrites<Service> = vec![(path.to_owned(), Vec::new())];
        for service in &services {
            let bucket = bucket(&mut service_write, &service.path);
            upsert(bucket, service.id.clone(), service.clone());
        }
        let mut parameter_write: PathWrites<Parameter> = vec![(path.to_owned(), Vec::new())];
        for parameter in &parameters {
            let bucket = bucket(&mut parameter_write, &parameter.path);
            upsert(bucket, parameter.name.clone(), parameter.clone());
        }
        let mut prototype_write: PathWrites<ServicePrototype> = vec![(path.to_owned(), Vec::new())];
        for prototype in prototypes {
            let key = format!(
                "{}:{}:{}",
                prototype.line, prototype.namespace, prototype.resource
            );
            upsert(&mut prototype_write[0].1, key, prototype);
        }
        Some(PreparedServices {
            services,
            parameters,
            service_write,
            parameter_write,
            prototype_write,
        })
    }

    /// Writes prepared service configuration for `file` and publishes its
    /// indexed path kinds after commit.
    fn persist(&self, file: &ParsedFile, prepared: Option<&PreparedServices>) -> Result<()> {
        let Some(PreparedServices {
            services,
            parameters,
            service_write,
            parameter_write,
            prototype_write,
        }) = prepared
        else {
            return Ok(());
        };

        // Every indexer sees every file; one without service data, now or
        // before, changes nothing and must not invalidate derived caches.
        let changed = self.has_indexed_path(file.path())
            || service_write.iter().any(|(_, items)| !items.is_empty())
            || parameter_write.iter().any(|(_, items)| !items.is_empty())
            || prototype_write.iter().any(|(_, items)| !items.is_empty());
        let mutation = file.mutation();
        self.parameters.batch_save_items_in(
            mutation,
            parameter_write.iter().map(|(path, items)| {
                (
                    path.as_str(),
                    items.iter().map(|(key, value)| (key.as_str(), value)),
                )
            }),
        )?;
        self.prototypes.batch_save_items_in(
            mutation,
            prototype_write.iter().map(|(path, items)| {
                (
                    path.as_str(),
                    items.iter().map(|(key, value)| (key.as_str(), value)),
                )
            }),
        )?;
        self.services.batch_save_items_in(
            mutation,
            service_write.iter().map(|(path, items)| {
                (
                    path.as_str(),
                    items.iter().map(|(key, value)| (key.as_str(), value)),
                )
            }),
        )?;

        let mut states: HashMap<String, PathState> = HashMap::new();
        let mut add = |path: &str, kind: u8, present: bool| {
            let state = states.entry(path.to_owned()).or_default();
            state.touched |= kind;
            if present {
                state.present |= kind;
            }
        };
        for (path, items) in service_write {
            add(path, PATH_SERVICES, !items.is_empty());
        }
        for (path, items) in parameter_write {
            add(path, PATH_PARAMETERS, !items.is_empty());
        }
        for (path, items) in prototype_write {
            add(path, PATH_PROTOTYPES, !items.is_empty());
        }
        self.publish_paths(mutation, move |paths| {
            for (path, state) in states {
                let mut kinds = paths.get(&path).copied().unwrap_or(0);
                kinds &= !state.touched;
                kinds |= state.present;
                if kinds == 0 {
                    paths.remove(&path);
                } else {
                    paths.insert(path, kinds);
                }
            }
        })?;
        add_service_workspace_symbols(file, services, parameters);
        if changed {
            self.bump();
        }
        Ok(())
    }

    fn remove(&self, paths: &[String], mutation: Option<&Mutation>) -> Result<()> {
        let changed = paths.iter().any(|path| self.has_indexed_path(path));
        let results = [
            self.services.batch_delete_by_file_paths_in(mutation, paths),
            self.parameters
                .batch_delete_by_file_paths_in(mutation, paths),
            self.prototypes
                .batch_delete_by_file_paths_in(mutation, paths),
        ];
        for result in results {
            result?;
        }
        let paths = paths.to_vec();
        self.publish_paths(mutation, move |indexed| {
            for path in &paths {
                indexed.remove(path);
            }
        })?;
        if changed {
            self.bump();
        }
        Ok(())
    }

    fn clear_all(&self, mutation: Option<&Mutation>) -> Result<()> {
        let results = [
            self.services.clear_in(mutation),
            self.parameters.clear_in(mutation),
            self.prototypes.clear_in(mutation),
        ];
        for result in results {
            result?;
        }
        self.publish_paths(mutation, |indexed| indexed.clear())?;
        self.bump();
        Ok(())
    }

    /// All indexed service IDs, sorted.
    pub fn get_all_services(&self) -> Result<Vec<String>> {
        let mut ids = self.services.get_all_keys()?;
        let mut seen: HashSet<String> = ids.iter().cloned().collect();
        for service in self.expanded_prototype_services()?.iter() {
            if seen.insert(service.id.clone()) {
                ids.push(service.id.clone());
            }
        }
        if self.container_exists() {
            for id in self.container.get_all_services() {
                if seen.insert(id.clone()) {
                    ids.push(id);
                }
            }
        }
        ids.sort();
        Ok(ids)
    }

    /// One definition per service ID. Source definitions take precedence over
    /// prototype expansion and the compiled container.
    pub fn get_all_service_definitions(&self) -> Result<Vec<Service>> {
        let explicit = self.services.get_all_values_view()?;
        let prototypes = self.expanded_prototype_services()?;
        let mut result: Vec<Service> = Vec::with_capacity(explicit.len() + prototypes.len());
        let mut seen: HashSet<String> = HashSet::with_capacity(explicit.len() + prototypes.len());
        let mut append = |definitions: &mut dyn Iterator<Item = Service>| {
            for service in definitions {
                if service.id.is_empty() || !seen.insert(service.id.clone()) {
                    continue;
                }
                result.push(service);
            }
        };
        append(&mut explicit.iter().cloned());
        append(&mut prototypes.iter().cloned());
        if self.container_exists() {
            append(&mut self.container.get_all_service_definitions().into_iter());
        }
        result.sort_by(|left, right| left.id.cmp(&right.id));
        Ok(result)
    }

    /// A specific service by its ID.
    pub fn get_service_by_id(&self, id: &str) -> Result<Option<Service>> {
        let services = self.services.get_values_view(id)?;
        if let Some(service) = services.first() {
            return Ok(Some(self.with_resolved_instanceof_tags(service.clone())));
        }
        if let Some(service) = self
            .expanded_prototype_services()?
            .iter()
            .find(|service| service.id == id)
        {
            return Ok(Some(self.with_resolved_instanceof_tags(service.clone())));
        }
        if self.container_exists() {
            return Ok(self.container.get_service_by_id(id));
        }
        Ok(None)
    }

    /// All tag names, sorted.
    pub fn get_all_tags(&self) -> Result<Vec<String>> {
        let mut tags: HashSet<String> = HashSet::new();
        for service in self.services.get_all_values_view()?.iter() {
            tags.extend(service.tags.keys().cloned());
            tags.extend(service.instanceof_tags.keys().cloned());
        }
        for prototype in self.prototypes.get_all_values_view()?.iter() {
            tags.extend(prototype.tags.keys().cloned());
            tags.extend(prototype.instanceof_tags.keys().cloned());
        }
        let mut tags: Vec<String> = tags.into_iter().collect();
        tags.sort();
        Ok(tags)
    }

    /// All service IDs with the given tag, sorted.
    pub fn get_services_by_tag(&self, tag_name: &str) -> Result<Vec<String>> {
        let values = self.services.get_all_values_view()?;
        let php = self.php_index();
        let mut services: Vec<String> = Vec::new();
        let mut explicit_definitions: HashSet<&str> = HashSet::with_capacity(values.len());
        let matches_conditional = |service: &Service| -> bool {
            match (service.instanceof_tags.get(tag_name), php.as_ref()) {
                (Some(target), Some(php)) => php.is_subtype_of(&service.class, target),
                _ => false,
            }
        };
        for value in values.iter() {
            explicit_definitions.insert(&value.id);
            if value.tags.contains_key(tag_name) || matches_conditional(value) {
                services.push(value.id.clone());
            }
        }
        let prototypes = self.expanded_prototype_services()?;
        for service in prototypes.iter() {
            if explicit_definitions.contains(service.id.as_str()) {
                continue;
            }
            if service.tags.contains_key(tag_name) || matches_conditional(service) {
                services.push(service.id.clone());
            }
        }
        services.sort();
        Ok(services)
    }

    /// Concrete services whose configured class can be assigned to
    /// `target_name`, including explicit, prototype-expanded, and compiled
    /// definitions. Memoized per target until the PHP workspace, the service
    /// index, or the compiled container change.
    pub fn get_services_by_type(&self, target_name: &str) -> Result<Vec<Service>> {
        let target_name = target_name.trim();
        let target_name = target_name.strip_prefix('\\').unwrap_or(target_name);
        if target_name.is_empty() {
            return Ok(Vec::new());
        }
        let php = self.php_index();
        let php_revision = php.as_ref().map_or(0, PhpIndex::revision);
        let own_revision = self.prototype_revision.load(Ordering::SeqCst);
        let container_revision = self.container.revision();
        let key = target_name.lowercase();
        {
            let mut cache = self
                .services_by_type
                .lock()
                .unwrap_or_else(PoisonError::into_inner);
            if cache.entries.is_some()
                && (cache.php_revision != php_revision
                    || cache.own_revision != own_revision
                    || cache.container_revision != container_revision)
            {
                cache.entries = None;
            }
            if cache.entries.is_none() {
                cache.entries = Some(HashMap::new());
                cache.php_revision = php_revision;
                cache.own_revision = own_revision;
                cache.container_revision = container_revision;
            }
            if let Some(cached) = cache.entries.as_ref().and_then(|entries| entries.get(&key)) {
                return Ok(cached.clone());
            }
        }
        let result = self.find_services_by_type(target_name, php.as_ref())?;
        let mut cache = self
            .services_by_type
            .lock()
            .unwrap_or_else(PoisonError::into_inner);
        if cache.php_revision == php_revision
            && cache.own_revision == own_revision
            && cache.container_revision == container_revision
            && let Some(entries) = cache.entries.as_mut()
        {
            entries.insert(key, result.clone());
        }
        Ok(result)
    }

    fn find_services_by_type(
        &self,
        target_name: &str,
        php: Option<&PhpIndex>,
    ) -> Result<Vec<Service>> {
        let explicit = self.services.get_all_values_view()?;
        let prototypes = self.expanded_prototype_services()?;
        let compiled = if self.container_exists() {
            self.container.get_all_service_definitions()
        } else {
            Vec::new()
        };
        let mut seen: HashSet<String> = HashSet::new();
        let mut result: Vec<Service> = Vec::new();
        for service in explicit
            .iter()
            .chain(prototypes.iter())
            .chain(compiled.iter())
        {
            let mut class_name = service.class.strip_prefix('\\').unwrap_or(&service.class);
            if class_name.is_empty() && service.id.contains('\\') {
                class_name = service.id.strip_prefix('\\').unwrap_or(&service.id);
            }
            if class_name.is_empty() {
                continue;
            }
            let matches = swlsp_textutil::eq_ignore_case(class_name, target_name)
                || php.is_some_and(|php| php.is_subtype_of(class_name, target_name));
            if !matches || !seen.insert(service.id.lowercase()) {
                continue;
            }
            result.push(service.clone());
        }
        result.sort_by_cached_key(|service| service.id.lowercase());
        Ok(result)
    }

    fn with_resolved_instanceof_tags(&self, mut service: Service) -> Service {
        if service.instanceof_tags.is_empty() || service.class.is_empty() {
            return service;
        }
        let Some(php) = self.php_index() else {
            return service;
        };
        for (tag, target) in &service.instanceof_tags {
            if php.is_subtype_of(&service.class, target) {
                service.tags.insert(tag.clone(), String::new());
            }
        }
        service
    }

    /// Services generated from prototypes for every matching concrete class,
    /// sorted by ID. Memoized per PHP and service revision.
    pub(crate) fn expanded_prototype_services(&self) -> Result<Arc<Vec<Service>>> {
        let Some(php) = self.php_index() else {
            return Ok(Arc::new(Vec::new()));
        };
        let php_revision = php.revision();
        let own_revision = self.prototype_revision.load(Ordering::SeqCst);
        let mut cache = self
            .prototype_cache
            .lock()
            .unwrap_or_else(PoisonError::into_inner);
        if let Some(services) = cache.services.as_ref()
            && cache.php_revision == php_revision
            && cache.own_revision == own_revision
        {
            return Ok(services.clone());
        }
        let mut prototypes = self.prototypes.get_all_values()?;
        prototypes
            .sort_by(|left, right| left.path.cmp(&right.path).then(left.line.cmp(&right.line)));
        // The class catalog's declarations (`PhpIndex::class_symbols_view`),
        // read through packed views: matching needs only the name, kind,
        // flags and path, so no declaration is materialized. Class order
        // does not matter because a service depends only on the class name.
        let snapshot = php.semantic_snapshot();
        let classes: Vec<_> = if prototypes.is_empty() {
            Vec::new()
        } else {
            snapshot
                .global_class_views()
                .into_iter()
                .filter(|view| {
                    let flags = view.flags();
                    !(flags.has(Flags::INTERNAL)
                        || flags.has(Flags::SYNTHETIC) && !flags.has(Flags::CLASS_ALIAS))
                })
                .collect()
        };
        let mut expanded: HashMap<String, Service> = HashMap::new();
        for prototype in &prototypes {
            for class in &classes {
                if !prototype_matches(
                    prototype,
                    class.fully_qualified(),
                    class.kind(),
                    class.flags(),
                    class.path(),
                ) {
                    continue;
                }
                let name = class.fully_qualified().to_owned();
                expanded.insert(
                    name.clone(),
                    Service {
                        id: name.clone(),
                        class: name,
                        autowire: prototype.autowire,
                        autowire_set: prototype.autowire_set,
                        tags: prototype.tags.clone(),
                        instanceof_tags: prototype.instanceof_tags.clone(),
                        path: prototype.path.clone(),
                        line: prototype.line,
                        range: prototype.range,
                        ..Service::default()
                    },
                );
            }
        }
        let mut services: Vec<Service> = expanded.into_values().collect();
        services.sort_by(|left, right| left.id.cmp(&right.id));
        let services = Arc::new(services);
        cache.services = Some(services.clone());
        cache.php_revision = php_revision;
        cache.own_revision = own_revision;
        Ok(services)
    }

    /// All parameters sorted case-insensitively; compiled parameters fill in
    /// names the source does not define.
    pub fn get_all_parameters(&self) -> Result<Vec<Parameter>> {
        let mut values = self.parameters.get_all_values()?;
        let mut seen: HashSet<String> = values
            .iter()
            .map(|parameter| parameter.name.lowercase())
            .collect();
        if self.container_exists() {
            for parameter in self.container.get_all_parameters() {
                if seen.insert(parameter.name.lowercase()) {
                    values.push(parameter);
                }
            }
        }
        values.sort_by_cached_key(|parameter| parameter.name.lowercase());
        Ok(values)
    }

    /// A parameter by name, falling back to its lowercase form and the
    /// compiled container.
    pub fn get_parameter_by_name(&self, name: &str) -> Result<Option<Parameter>> {
        let mut values = self.parameters.get_values_view(name)?;
        let lower = name.lowercase();
        if values.is_empty() && lower != name {
            values = self.parameters.get_values_view(&lower)?;
        }
        if let Some(value) = values.first() {
            return Ok(Some(value.clone()));
        }
        if self.container_exists() {
            if let Some(parameter) = self.container.get_parameter_by_name(name) {
                return Ok(Some(parameter));
            }
            return Ok(self.container.get_parameter_by_name(&lower));
        }
        Ok(None)
    }

    /// Definition locations of services configured with `class_name`.
    pub fn get_services_usage_by_class_name(&self, class_name: &str) -> Result<Vec<Location>> {
        let class_name = class_name.strip_prefix('\\').unwrap_or(class_name);
        let mut locations: Vec<Location> = Vec::new();
        let mut seen: HashSet<Location> = HashSet::new();
        let mut append = |service: &Service| {
            if !swlsp_textutil::eq_ignore_case(
                service.class.strip_prefix('\\').unwrap_or(&service.class),
                class_name,
            ) {
                return;
            }
            let location = Location {
                path: service.path.clone(),
                line: service.line,
            };
            if seen.insert(location.clone()) {
                locations.push(location);
            }
        };
        for service in self.services.get_all_values_view()?.iter() {
            append(service);
        }
        for service in self.expanded_prototype_services()?.iter() {
            append(service);
        }
        if self.container_exists() {
            for service in self.container.get_all_service_definitions() {
                append(&service);
            }
        }
        locations.sort();
        Ok(locations)
    }

    /// Source definitions for services whose effective autowire setting is
    /// enabled. Parent definitions are followed because child definitions
    /// inherit unset options.
    pub fn get_autowired_services_usage_by_class_name(
        &self,
        class_name: &str,
    ) -> Result<Vec<Location>> {
        if class_name.is_empty() {
            return Ok(Vec::new());
        }
        // The definitions of `get_all_service_definitions`, borrowed: the
        // first definition of an ID wins, and only the result locations are
        // copied.
        let explicit = self.services.get_all_values_view()?;
        let prototypes = self.expanded_prototype_services()?;
        let container = if self.container_exists() {
            self.container.get_all_service_definitions()
        } else {
            Vec::new()
        };
        let mut seen_ids: HashSet<&str> =
            HashSet::with_capacity(explicit.len() + prototypes.len() + container.len());
        let definitions: Vec<&Service> = explicit
            .iter()
            .chain(prototypes.iter())
            .chain(container.iter())
            .filter(|service| !service.id.is_empty() && seen_ids.insert(&service.id))
            .collect();
        let class_name = class_name.strip_prefix('\\').unwrap_or(class_name);
        // Parent lookups are only needed when a matching definition leaves
        // autowiring unset.
        let mut by_id: Option<HashMap<String, Vec<&Service>>> = None;
        let mut locations: Vec<Location> = Vec::new();
        let mut seen: HashSet<Location> = HashSet::new();
        for service in &definitions {
            if !swlsp_textutil::eq_ignore_case(
                service.class.strip_prefix('\\').unwrap_or(&service.class),
                class_name,
            ) {
                continue;
            }
            let autowire = if service.autowire_set {
                service.autowire
            } else {
                let by_id = by_id.get_or_insert_with(|| service_definitions_by_id(&definitions));
                effective_service_autowire(service, by_id, &mut HashSet::new())
            };
            if !autowire {
                continue;
            }
            let location = Location {
                path: service.path.clone(),
                line: service.line,
            };
            if location.path.is_empty() || !seen.insert(location.clone()) {
                continue;
            }
            locations.push(location);
        }
        locations.sort();
        Ok(locations)
    }
}

/// Definitions keyed by their lowercase ID without a leading backslash.
fn service_definitions_by_id<'a>(definitions: &[&'a Service]) -> HashMap<String, Vec<&'a Service>> {
    let mut by_id: HashMap<String, Vec<&Service>> = HashMap::new();
    for service in definitions {
        let key = service
            .id
            .strip_prefix('\\')
            .unwrap_or(&service.id)
            .lowercase();
        if !key.is_empty() {
            by_id.entry(key).or_default().push(service);
        }
    }
    by_id
}

fn bucket<'a, T>(write: &'a mut PathWrites<T>, path: &str) -> &'a mut Vec<(String, T)> {
    let index = match write.iter().position(|(existing, _)| existing == path) {
        Some(index) => index,
        None => {
            write.push((path.to_owned(), Vec::new()));
            write.len() - 1
        }
    };
    &mut write[index].1
}

fn upsert<T>(items: &mut Vec<(String, T)>, key: String, value: T) {
    match items.iter_mut().find(|(existing, _)| *existing == key) {
        Some(entry) => entry.1 = value,
        None => items.push((key, value)),
    }
}

/// Cheap byte check selecting PHP files that may configure services.
pub(crate) fn is_php_service_config_candidate(content: &str) -> bool {
    let fluent = content.contains("ContainerConfigurator")
        && (content.contains("->services") || content.contains("->parameters"));
    let array = (content.contains("'services'")
        || content.contains("\"services\"")
        || content.contains("'parameters'")
        || content.contains("\"parameters\""))
        && (content.contains("return") || content.contains("::config"));
    fluent || array
}

pub(crate) fn prototype_matches_class(prototype: &ServicePrototype, class: &Symbol) -> bool {
    prototype_matches(
        prototype,
        class.fully_qualified.as_str(),
        class.kind,
        class.flags,
        class.path.as_str(),
    )
}

fn prototype_matches(
    prototype: &ServicePrototype,
    fully_qualified: &str,
    kind: SymbolKind,
    flags: Flags,
    path: &str,
) -> bool {
    if fully_qualified.is_empty() || kind != SymbolKind::Class || flags.has(Flags::ABSTRACT) {
        return false;
    }
    let mut namespace = prototype
        .namespace
        .strip_prefix('\\')
        .unwrap_or(&prototype.namespace)
        .to_owned();
    if !namespace.is_empty() {
        if !namespace.ends_with('\\') {
            namespace.push('\\');
        }
        if !fully_qualified.starts_with(&namespace) {
            return false;
        }
    }
    if !path_matches_config_pattern(path, &prototype.resource) {
        return false;
    }
    !prototype
        .excludes
        .iter()
        .any(|exclude| path_matches_config_pattern(path, exclude))
}

fn effective_service_autowire(
    service: &Service,
    definitions: &HashMap<String, Vec<&Service>>,
    visiting: &mut HashSet<String>,
) -> bool {
    if service.autowire_set {
        return service.autowire;
    }
    let parent = service
        .parent
        .strip_prefix('\\')
        .unwrap_or(&service.parent)
        .lowercase();
    if parent.is_empty() || visiting.contains(&parent) {
        return false;
    }
    visiting.insert(parent.clone());
    let result = definitions.get(&parent).is_some_and(|candidates| {
        candidates
            .iter()
            .any(|definition| effective_service_autowire(definition, definitions, visiting))
    });
    visiting.remove(&parent);
    result
}

impl Indexer for ServiceIndex {
    fn id(&self) -> &str {
        "symfony.service"
    }

    fn index(&self, file: &ParsedFile) -> Result<()> {
        self.persist(file, self.extract(file).as_ref())
    }

    fn removed_files(&self, paths: &[String]) -> Result<()> {
        self.remove(paths, None)
    }

    fn close(&self) -> Result<()> {
        let results = [
            self.services.close(),
            self.parameters.close(),
            self.prototypes.close(),
        ];
        for result in results {
            result?;
        }
        Ok(())
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

impl PreparingIndexer for ServiceIndex {
    fn prepare(&self, file: &ParsedFile) -> Result<Option<Prepared>> {
        Ok(Some(Box::new(self.extract(file))))
    }

    fn index_prepared(&self, file: &ParsedFile, prepared: Option<&Prepared>) -> Result<()> {
        match prepared.and_then(|value| value.downcast_ref::<Option<PreparedServices>>()) {
            Some(prepared) => self.persist(file, prepared.as_ref()),
            None => self.index(file),
        }
    }
}

impl TransactionalRemover for ServiceIndex {
    fn removed_files_in(&self, paths: &[String], mutation: &Mutation) -> Result<()> {
        self.remove(paths, Some(mutation))
    }
}

impl TransactionalClearer for ServiceIndex {
    fn clear_in(&self, mutation: &Mutation) -> Result<()> {
        self.clear_all(Some(mutation))
    }
}

impl ServiceIndex {
    /// The scanner-fed compiled container index for this service index.
    pub fn compiled_container_index(
        &self,
        cache: impl AsRef<Path>,
        store: Option<&Store>,
    ) -> Result<crate::CompiledCatalogIndex> {
        crate::CompiledCatalogIndex::new_container(
            &self.project_root,
            cache,
            self.container.clone(),
            store,
        )
    }
}

impl swlsp_indexer::Revisioned for ServiceIndex {
    fn revision(&self) -> u64 {
        ServiceIndex::revision(self)
    }
}
