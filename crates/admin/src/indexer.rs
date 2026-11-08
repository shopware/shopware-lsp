//! The Administration indexer and its repositories (port of Go
//! `indexer.go`).

use std::sync::RwLock;

use swlsp_indexer::Store;

use crate::*;

/// Indexes Administration components, registries, usages and TypeScript
/// types into namespaced repositories of the workspace store.
pub struct AdminComponentIndexer {
    pub(crate) component_index: DataIndexer<VueComponent>,
    pub(crate) definition_index: DataIndexer<ComponentDefinition>,
    pub(crate) mixin_index: DataIndexer<AdminMixin>,
    pub(crate) directive_index: DataIndexer<AdminDirective>,
    pub(crate) filter_index: DataIndexer<AdminFilter>,
    pub(crate) cms_index: DataIndexer<AdminCMSRegistration>,
    pub(crate) module_index: DataIndexer<AdminModule>,
    pub(crate) service_index: DataIndexer<AdminService>,
    pub(crate) store_index: DataIndexer<AdminStore>,
    pub(crate) store_factory_index: DataIndexer<AdminStoreFactory>,
    pub(crate) privilege_index: DataIndexer<AdminPrivilege>,
    pub(crate) usage_index: DataIndexer<AdminUsageSet>,
    pub(crate) type_index: DataIndexer<AdminTypeFile>,
    /// Template path → component name (Go `templateCache` +
    /// `templateCatalogBuilt`).
    pub(crate) template_cache: RwLock<TemplateComponentCache>,
    /// Effective components by name (Go `effectiveCache` + epoch).
    pub(crate) effective_cache: RwLock<EffectiveComponentCache>,
    /// Open-document overlays (Go `liveDocumentMu` + maps).
    pub(crate) live: RwLock<LiveDocuments>,
    /// The persisted catalog merged with open legacy documents, keyed by the
    /// persisted view and the legacy-document generation it was built from.
    pub(crate) merged_components: std::sync::Mutex<Option<MergedComponents>>,
    /// Component names by normalized definition path for one catalog view.
    pub(crate) definition_paths: std::sync::Mutex<Option<DefinitionPathIndex>>,
}

#[derive(Debug)]
pub(crate) struct DefinitionPathIndex {
    pub catalog: std::sync::Arc<[VueComponent]>,
    /// Normalized non-empty `definition_path` → (name, file path).
    pub by_definition: HashMap<String, Vec<(String, String)>>,
    /// Raw `file_path` of components without a definition path → (name,
    /// file path).
    pub by_file: HashMap<String, Vec<(String, String)>>,
}

#[derive(Debug)]
pub(crate) struct MergedComponents {
    pub persisted: std::sync::Arc<[VueComponent]>,
    pub generation: u64,
    pub merged: std::sync::Arc<[VueComponent]>,
}

#[derive(Debug, Default)]
pub(crate) struct TemplateComponentCache {
    pub components: HashMap<String, String>,
    pub catalog_built: bool,
    /// Normalized template paths proven to have no component in the
    /// current generation (Go recomputes these with a full scan per call).
    pub misses: HashSet<String>,
}

#[derive(Debug, Default)]
pub(crate) struct EffectiveComponentCache {
    pub components: HashMap<String, std::sync::Arc<VueComponent>>,
    pub epoch: u64,
}

#[derive(Debug, Default)]
pub(crate) struct LiveDocuments {
    pub vue_documents: HashMap<String, ComponentDefinition>,
    pub legacy_documents: HashMap<String, std::sync::Arc<LiveLegacyDocument>>,
    pub runtime_documents: HashMap<String, std::sync::Arc<LiveLegacyDocument>>,
    pub twig_templates: HashMap<String, TemplateParseResult>,
    pub type_files: HashMap<String, AdminTypeFile>,
    /// Bumped whenever `legacy_documents` changes.
    pub legacy_generation: u64,
}

impl AdminComponentIndexer {
    /// Opens the repositories inside `store`, or isolated databases below
    /// `config_dir` when no store is given (Go `NewAdminComponentIndexer`).
    pub fn new(config_dir: &str, store: Option<&Store>) -> IndexResult<Self> {
        fn open<T>(
            config_dir: &str,
            store: Option<&Store>,
            file: &str,
            namespace: &str,
        ) -> IndexResult<DataIndexer<T>>
        where
            T: Serialize + serde::de::DeserializeOwned + Send + Sync + 'static,
        {
            DataIndexer::repository(
                std::path::Path::new(config_dir).join(file),
                namespace,
                store,
            )
        }
        Ok(Self {
            component_index: open(config_dir, store, "admin_component.db", "admin.components")?,
            definition_index: open(
                config_dir,
                store,
                "admin_component_definition.db",
                "admin.definitions",
            )?,
            mixin_index: open(config_dir, store, "admin_mixin.db", "admin.mixins")?,
            directive_index: open(config_dir, store, "admin_directive.db", "admin.directives")?,
            filter_index: open(config_dir, store, "admin_filter.db", "admin.filters")?,
            cms_index: open(config_dir, store, "admin_cms.db", "admin.cms")?,
            module_index: open(config_dir, store, "admin_module.db", "admin.modules")?,
            service_index: open(config_dir, store, "admin_service.db", "admin.services")?,
            store_index: open(config_dir, store, "admin_store.db", "admin.stores")?,
            store_factory_index: open(
                config_dir,
                store,
                "admin_store_factory.db",
                "admin.store_factories",
            )?,
            privilege_index: open(config_dir, store, "admin_privilege.db", "admin.privileges")?,
            usage_index: open(config_dir, store, "admin_usage.db", "admin.usages")?,
            type_index: open(config_dir, store, "admin_type.db", "admin.types")?,
            template_cache: RwLock::default(),
            effective_cache: RwLock::default(),
            live: RwLock::default(),
            merged_components: std::sync::Mutex::default(),
            definition_paths: std::sync::Mutex::default(),
        })
    }
}

/// Runs every repository operation and returns the first error (Go
/// `errors.Join`).
fn join_errors(results: impl IntoIterator<Item = IndexResult<()>>) -> IndexResult<()> {
    let mut first = Ok(());
    for result in results {
        if first.is_ok() && result.is_err() {
            first = result;
        }
    }
    first
}

/// Reads and parses a template file; `None` when it cannot be read.
pub(crate) fn template_from_file(path: &str) -> Option<TemplateParseResult> {
    parse_template_from_file(path).ok()
}

/// One path with no items: a delete-only batch update.
fn empty_batch<T>(file_path: &str) -> [(&str, Vec<(String, T)>); 1] {
    [(file_path, Vec::new())]
}

impl AdminComponentIndexer {
    pub(crate) fn save_type_file(
        writes: &mut AdminWrites,
        file_path: &str,
        source: &str,
        line_index: &LineIndex,
    ) {
        let type_file = parse_admin_type_file(file_path, source, Some(line_index));
        let mut items = Vec::new();
        if !type_file.declarations.is_empty() || !type_file.imports.is_empty() {
            items.push((file_path.to_owned(), type_file));
        }
        let file_path = file_path.to_owned();
        writes.push(move |indexer, mutation| {
            indexer
                .type_index
                .batch_save_items_in(mutation, [(file_path, items)])
        });
    }

    pub(crate) fn save_usages(
        writes: &mut AdminWrites,
        file_path: &str,
        usages: Vec<AdminUsageSet>,
    ) {
        let mut batch: HashMap<String, AdminUsageSet> = HashMap::new();
        for usage in usages {
            batch.insert(
                admin_usage_key(usage.kind, &usage.owner, &usage.name),
                usage,
            );
        }
        let file_path = file_path.to_owned();
        writes.push(move |indexer, mutation| {
            indexer
                .usage_index
                .batch_save_items_in(mutation, [(file_path, batch)])
        });
    }

    fn index_file(&self, file: &ParsedFile) -> IndexResult<()> {
        self.prepare_writes(file).apply(self, file)
    }

    /// Parses `file` and records every repository write it implies, without
    /// touching the database. The scanner runs this outside the shared write
    /// transaction (in parallel); [`AdminWrites::apply`] replays the writes.
    pub(crate) fn prepare_writes(&self, file: &ParsedFile) -> AdminWrites {
        let mut writes = AdminWrites::default();
        let file_path = file.path();
        if is_meteor_declaration_path(file_path) {
            writes.invalidate_caches();
            Self::save_type_file(&mut writes, file_path, file.source(), file.line_index());
            let component = parse_meteor_declaration(file_path, file.source());
            let mut items = Vec::new();
            if let Some(component) = component {
                add_admin_component_workspace_symbols(
                    &mut writes,
                    std::slice::from_ref(&component),
                );
                items.push((component.name.clone(), component));
            }
            let path = file_path.to_owned();
            writes.push(move |indexer, mutation| {
                indexer
                    .component_index
                    .batch_save_items_in(mutation, [(path, items)])
            });
            Self::save_usages(&mut writes, file_path, Vec::new());
            return writes;
        }
        let ext = file.extension();
        if ext == ".twig" && is_administration_source_path(file_path) {
            writes.invalidate_caches();
            let Some(tree) = file.syntax_tree() else {
                Self::save_usages(&mut writes, file_path, Vec::new());
                return writes;
            };
            let usages = parse_admin_twig_usages(&tree.root(), file_path, file.line_index());
            Self::save_usages(&mut writes, file_path, usages);
            return writes;
        }
        if ext != ".js" && ext != ".ts" && ext != ".vue" {
            return writes;
        }
        // Generated Vite, Storybook and test bundles repeat registry-looking
        // strings without declaring project symbols.
        if !is_administration_source_path(file_path) {
            return writes;
        }
        writes.invalidate_caches();
        let Some(tree) = file.syntax_tree() else {
            return writes;
        };
        let root = tree.root();
        let line_index = file.line_index();
        let mut vue_sections = Vec::new();
        let mut vue_uses_typescript = false;
        if ext == ".vue" {
            vue_sections = swlsp_parser_vue::sections(file.source());
            vue_uses_typescript = vue_sections.iter().any(|section| {
                section.kind == swlsp_parser_vue::SectionKind::Script
                    && section.language.eq_ignore_ascii_case("ts")
            });
        }
        if ext == ".ts" || vue_uses_typescript {
            if ext == ".vue" {
                let type_source = vue_script_type_source(file.source(), &vue_sections);
                Self::save_type_file(&mut writes, file_path, &type_source, line_index);
            } else {
                Self::save_type_file(&mut writes, file_path, file.source(), line_index);
            }
        }
        let mut usages = parse_admin_javascript_usages(&root, file_path, line_index);
        if ext == ".vue" {
            usages.extend(parse_admin_twig_usages(&root, file_path, line_index));
        }
        Self::save_usages(&mut writes, file_path, usages);
        let path = file_path.to_owned();
        writes.push(move |indexer, mutation| {
            indexer
                .component_index
                .batch_save_items_in(mutation, empty_batch::<VueComponent>(&path))?;
            indexer
                .definition_index
                .batch_save_items_in(mutation, empty_batch::<ComponentDefinition>(&path))?;
            indexer
                .mixin_index
                .batch_save_items_in(mutation, empty_batch::<AdminMixin>(&path))?;
            indexer
                .module_index
                .batch_save_items_in(mutation, empty_batch::<AdminModule>(&path))
        });
        Self::index_registrations(&mut writes, file_path, &root, line_index);
        Self::index_mixins_and_modules(&mut writes, file_path, &root, line_index);
        Self::index_runtime_registries(&mut writes, file_path, &root, line_index);
        let handled_by_wrap =
            Self::index_wrapped_components(&mut writes, file_path, &root, line_index);
        if !handled_by_wrap {
            if ext == ".vue" {
                Self::index_vue_definition(
                    &mut writes,
                    file_path,
                    &root,
                    file.source(),
                    line_index,
                    &vue_sections,
                );
            } else {
                Self::index_definition(&mut writes, file_path, &root, line_index);
            }
        }
        writes
    }

    pub(crate) fn index_runtime_registries(
        writes: &mut AdminWrites,
        file_path: &str,
        root: &SyntaxNode,
        line_index: &LineIndex,
    ) {
        let (services, stores) = parse_admin_runtime_registries(root, file_path, line_index);
        let mut factories = Vec::new();
        if let Some(factory) = parse_admin_store_factory(root, file_path, line_index) {
            factories.push((normalize_definition_path(file_path), factory));
        }
        let directives = parse_admin_directives(root, file_path, line_index);
        let filters = parse_admin_filters(root, file_path, line_index);
        let cms_registrations = parse_admin_cms_registrations(root, file_path, line_index);
        let privileges = parse_admin_privileges(root, file_path, line_index);
        let services_batch = keyed(&services, |service| service.name.clone());
        let stores_batch = keyed(&stores, |store| store.name.clone());
        let directives_batch = keyed(&directives, |directive| directive.name.clone());
        let filters_batch = keyed(&filters, |filter| filter.name.clone());
        let cms_batch = keyed(&cms_registrations, |registration| {
            admin_cms_key(registration.kind, &registration.name)
        });
        let privileges_batch = keyed(&privileges, |privilege| privilege.name.clone());
        let path = file_path.to_owned();
        writes.push(move |indexer, mutation| {
            let path = path.as_str();
            indexer
                .service_index
                .batch_save_items_in(mutation, [(path, services_batch)])?;
            indexer
                .store_index
                .batch_save_items_in(mutation, [(path, stores_batch)])?;
            indexer
                .store_factory_index
                .batch_save_items_in(mutation, [(path, factories)])?;
            indexer
                .directive_index
                .batch_save_items_in(mutation, [(path, directives_batch)])?;
            indexer
                .filter_index
                .batch_save_items_in(mutation, [(path, filters_batch)])?;
            indexer
                .cms_index
                .batch_save_items_in(mutation, [(path, cms_batch)])?;
            indexer
                .privilege_index
                .batch_save_items_in(mutation, [(path, privileges_batch)])
        });
        add_admin_runtime_workspace_symbols(
            writes,
            &services,
            &stores,
            &directives,
            &filters,
            &cms_registrations,
            &privileges,
        );
    }

    pub(crate) fn index_mixins_and_modules(
        writes: &mut AdminWrites,
        file_path: &str,
        root: &SyntaxNode,
        line_index: &LineIndex,
    ) {
        let (mixins, modules) = parse_mixins_and_modules(root, file_path, line_index);
        let mixins_batch = keyed(&mixins, |mixin| mixin.name.clone());
        let modules_batch = keyed(&modules, |module| module.name.clone());
        let path = file_path.to_owned();
        writes.push(move |indexer, mutation| {
            indexer
                .mixin_index
                .batch_save_items_in(mutation, [(path.as_str(), mixins_batch)])?;
            indexer
                .module_index
                .batch_save_items_in(mutation, [(path.as_str(), modules_batch)])
        });
        add_admin_mixins_and_modules_workspace_symbols(writes, &mixins, &modules);
    }

    /// Indexes `Shopware.Component.register/extend/override` calls.
    pub(crate) fn index_registrations(
        writes: &mut AdminWrites,
        file_path: &str,
        root: &SyntaxNode,
        line_index: &LineIndex,
    ) {
        let components = parse_component_registrations_with_line_index(root, file_path, line_index);
        if components.is_empty() {
            return;
        }
        let mut batch: BTreeMap<String, HashMap<String, VueComponent>> = BTreeMap::new();
        let mut definitions: BTreeMap<String, HashMap<String, ComponentDefinition>> =
            BTreeMap::new();
        for component in &components {
            batch
                .entry(component.file_path.clone())
                .or_default()
                .insert(component.name.clone(), component.clone());
            if let Some(definition) = &component.inline_definition {
                definitions
                    .entry(component.file_path.clone())
                    .or_default()
                    .insert(component.name.clone(), (**definition).clone());
            }
        }
        writes.push(move |indexer, mutation| {
            indexer
                .component_index
                .batch_save_items_in(mutation, batch)?;
            if !definitions.is_empty() {
                indexer
                    .definition_index
                    .batch_save_items_in(mutation, definitions)?;
            }
            Ok(())
        });
        add_admin_component_workspace_symbols(writes, &components);
    }

    /// Indexes `export default Shopware.Component.wrapComponentConfig(...)`.
    /// Returns whether the file was such a wrapper.
    pub(crate) fn index_wrapped_components(
        writes: &mut AdminWrites,
        file_path: &str,
        root: &SyntaxNode,
        line_index: &LineIndex,
    ) -> bool {
        let mut found = None;
        for candidate in jsquery::export_defaults(root) {
            let Some(expression) = jsquery::export_default_expression(&candidate) else {
                continue;
            };
            if matches!(
                jsquery::call_name(&expression).as_deref(),
                Some("Shopware.Component.wrapComponentConfig" | "Component.wrapComponentConfig")
            ) {
                found = Some((candidate, expression));
                break;
            }
        }
        let Some((export_node, call_expression)) = found else {
            return false;
        };
        let component_name = derive_component_name_from_path(file_path);
        if component_name.is_empty() {
            return true;
        }
        let Some(config_object) = component_definition_object(&call_expression) else {
            return true;
        };
        let mut definition =
            parse_inline_definition(Some(root), &config_object, file_path, line_index);
        if let Some(definition) = definition.as_mut() {
            definition.deprecated = javascript_deprecation(&export_node);
            set_definition_file_path(definition, file_path);
            if let Some(template_path) = jsquery::import_path(root, "template")
                && !template_path.is_empty()
            {
                let template_abs_path = resolve_template_path(file_path, &template_path);
                definition.template_path = template_abs_path.clone();
                if let Some(result) = template_from_file(&template_abs_path) {
                    definition.slots = result.slots;
                    definition.blocks = result.blocks;
                }
            }
        }
        let (line, _) = line_index.position(u32::from(export_node.range_trimmed_trivia().start()));
        let mut component = VueComponent {
            name: component_name.clone(),
            file_path: file_path.to_owned(),
            line: line + 1,
            definition_path: file_path.to_owned(),
            ..Default::default()
        };
        if let Some(definition) = &definition {
            component.deprecated = definition.deprecated.clone();
            component.props = definition.props.clone();
            component.model_prop = definition.model_prop.clone();
            component.model_event = definition.model_event.clone();
            component.emits = definition.emits.clone();
            component.events = definition.events.clone();
            component.methods = definition.methods.clone();
            component.computed = definition.computed.clone();
            component.data = definition.data.clone();
            component.injected = definition.injected.clone();
            component.mixins = definition.mixins.clone();
            component.local_components = definition.local_components.clone();
            component.local_directives = definition.local_directives.clone();
            component.members = definition.members.clone();
            component.slots = definition.slots.clone();
            component.blocks = definition.blocks.clone();
            component.template_path = definition.template_path.clone();
            component.inline_definition = Some(Box::new(definition.clone()));
        }
        add_admin_component_workspace_symbols(writes, std::slice::from_ref(&component));
        let path = file_path.to_owned();
        let symbol_position = writes.ops.len() - 1;
        writes.insert(symbol_position, move |indexer, mutation| {
            indexer.component_index.batch_save_items_in(
                mutation,
                [(path.as_str(), [(component_name.as_str(), &component)])],
            )?;
            if let Some(definition) = &definition {
                indexer.definition_index.batch_save_items_in(
                    mutation,
                    [(path.as_str(), [(component_name.as_str(), definition)])],
                )?;
            }
            Ok(())
        });
        true
    }

    pub(crate) fn index_vue_definition(
        writes: &mut AdminWrites,
        file_path: &str,
        root: &SyntaxNode,
        source: &str,
        line_index: &LineIndex,
        sections: &[swlsp_parser_vue::Section],
    ) {
        let Some(definition) = parse_vue_definition(file_path, root, source, line_index, sections)
        else {
            return;
        };
        let path = file_path.to_owned();
        writes.push(move |indexer, mutation| {
            indexer.definition_index.batch_save_items_in(
                mutation,
                [(
                    path.as_str(),
                    [(normalize_definition_path(&path), definition)],
                )],
            )
        });
    }

    /// Indexes component definition files (`export default { ... }`).
    pub(crate) fn index_definition(
        writes: &mut AdminWrites,
        file_path: &str,
        root: &SyntaxNode,
        line_index: &LineIndex,
    ) {
        let exports = jsquery::export_defaults(root);
        let Some(first) = exports.first() else {
            return;
        };
        let Some(expression) = jsquery::export_default_expression(first) else {
            return;
        };
        if component_definition_object(&expression).is_none() {
            return;
        }
        let mut definition = parse_component_definition_with_line_index(root, line_index);
        set_definition_file_path(&mut definition, file_path);
        if !definition.template_path.is_empty() {
            let mut template_abs_path = definition.template_path.clone();
            if !gopath::is_abs(&template_abs_path) {
                template_abs_path = resolve_template_path(file_path, &definition.template_path);
                definition.template_path = template_abs_path.clone();
            }
            if let Some(result) = template_from_file(&template_abs_path) {
                definition.slots = result.slots;
                definition.blocks = result.blocks;
            }
        }
        let path = file_path.to_owned();
        writes.push(move |indexer, mutation| {
            indexer.definition_index.batch_save_items_in(
                mutation,
                [(
                    path.as_str(),
                    [(normalize_definition_path(&path), definition)],
                )],
            )
        });
    }

    pub(crate) fn removed_files_impl(
        &self,
        paths: &[String],
        mutation: Option<&Mutation>,
    ) -> IndexResult<()> {
        self.invalidate_template_component_cache();
        join_errors([
            self.component_index
                .batch_delete_by_file_paths_in(mutation, paths),
            self.definition_index
                .batch_delete_by_file_paths_in(mutation, paths),
            self.mixin_index
                .batch_delete_by_file_paths_in(mutation, paths),
            self.directive_index
                .batch_delete_by_file_paths_in(mutation, paths),
            self.filter_index
                .batch_delete_by_file_paths_in(mutation, paths),
            self.cms_index
                .batch_delete_by_file_paths_in(mutation, paths),
            self.module_index
                .batch_delete_by_file_paths_in(mutation, paths),
            self.service_index
                .batch_delete_by_file_paths_in(mutation, paths),
            self.store_index
                .batch_delete_by_file_paths_in(mutation, paths),
            self.store_factory_index
                .batch_delete_by_file_paths_in(mutation, paths),
            self.privilege_index
                .batch_delete_by_file_paths_in(mutation, paths),
            self.usage_index
                .batch_delete_by_file_paths_in(mutation, paths),
            self.type_index
                .batch_delete_by_file_paths_in(mutation, paths),
        ])
    }

    pub(crate) fn clear_impl(&self, mutation: Option<&Mutation>) -> IndexResult<()> {
        self.invalidate_template_component_cache();
        join_errors([
            self.component_index.clear_in(mutation),
            self.definition_index.clear_in(mutation),
            self.mixin_index.clear_in(mutation),
            self.directive_index.clear_in(mutation),
            self.filter_index.clear_in(mutation),
            self.cms_index.clear_in(mutation),
            self.module_index.clear_in(mutation),
            self.service_index.clear_in(mutation),
            self.store_index.clear_in(mutation),
            self.store_factory_index.clear_in(mutation),
            self.privilege_index.clear_in(mutation),
            self.usage_index.clear_in(mutation),
            self.type_index.clear_in(mutation),
        ])
    }

    fn close_all(&self) -> IndexResult<()> {
        join_errors([
            self.component_index.close(),
            self.definition_index.close(),
            self.mixin_index.close(),
            self.directive_index.close(),
            self.filter_index.close(),
            self.cms_index.close(),
            self.module_index.close(),
            self.service_index.close(),
            self.store_index.close(),
            self.store_factory_index.close(),
            self.privilege_index.close(),
            self.usage_index.close(),
            self.type_index.close(),
        ])
    }
}

/// Builds `key -> value` items where later values with the same key win,
/// matching Go map assignment.
fn keyed<T: Clone>(values: &[T], key: impl Fn(&T) -> String) -> HashMap<String, T> {
    let mut result = HashMap::with_capacity(values.len());
    for value in values {
        result.insert(key(value), value.clone());
    }
    result
}

/// Parses a Vue SFC's Options API and `<script setup>` definition plus its
/// template contracts. `None` when the file has neither a definition nor a
/// template (shared by indexing and the live-document overlay).
pub(crate) fn parse_vue_definition(
    file_path: &str,
    root: &SyntaxNode,
    source: &str,
    line_index: &LineIndex,
    sections: &[swlsp_parser_vue::Section],
) -> Option<ComponentDefinition> {
    let mut definition = parse_component_definition_with_line_index(root, line_index);
    let mut has_definition = !jsquery::export_defaults(root).is_empty();
    let mut has_template = false;
    for section in sections {
        match section.kind {
            swlsp_parser_vue::SectionKind::Template => has_template = true,
            swlsp_parser_vue::SectionKind::Script => {
                if !section.setup {
                    continue;
                }
                if let Some(setup) = parse_script_setup_definition(
                    root,
                    source,
                    file_path,
                    line_index,
                    section.body_range,
                ) {
                    definition = merge_component_definitions(definition, &setup);
                    has_definition = true;
                }
            }
            _ => {}
        }
    }
    if !has_definition && !has_template {
        return None;
    }
    if has_template {
        definition.has_template = true;
        definition.template_path = file_path.to_owned();
        let mut template = parse_template_tree(root, source, line_index);
        set_template_source_paths(&mut template, file_path);
        definition.slots = template.slots;
        definition.blocks = template.blocks;
    }
    set_definition_file_path(&mut definition, file_path);
    Some(definition)
}

impl swlsp_indexer::Indexer for AdminComponentIndexer {
    fn id(&self) -> &str {
        "admin.component.indexer"
    }

    fn index(&self, file: &ParsedFile) -> IndexResult<()> {
        self.index_file(file)
    }

    fn removed_files(&self, paths: &[String]) -> IndexResult<()> {
        self.removed_files_impl(paths, None)
    }

    fn close(&self) -> IndexResult<()> {
        self.close_all()
    }

    fn clear(&self) -> IndexResult<()> {
        self.clear_impl(None)
    }

    fn as_preparing(&self) -> Option<&dyn swlsp_indexer::PreparingIndexer> {
        Some(self)
    }

    fn as_supplemental_path(&self) -> Option<&dyn swlsp_indexer::SupplementalPathIndexer> {
        Some(self)
    }

    fn as_supplemental_syntax(&self) -> Option<&dyn swlsp_indexer::SupplementalSyntaxIndexer> {
        Some(self)
    }

    fn as_transactional_remover(&self) -> Option<&dyn swlsp_indexer::TransactionalRemover> {
        Some(self)
    }

    fn as_transactional_clearer(&self) -> Option<&dyn swlsp_indexer::TransactionalClearer> {
        Some(self)
    }
}

impl swlsp_indexer::PreparingIndexer for AdminComponentIndexer {
    fn prepare(&self, file: &ParsedFile) -> IndexResult<Option<swlsp_indexer::Prepared>> {
        Ok(Some(Box::new(std::sync::Mutex::new(Some(
            self.prepare_writes(file),
        )))))
    }

    fn index_prepared(
        &self,
        file: &ParsedFile,
        prepared: Option<&swlsp_indexer::Prepared>,
    ) -> IndexResult<()> {
        let writes = prepared
            .and_then(|prepared| prepared.downcast_ref::<std::sync::Mutex<Option<AdminWrites>>>())
            .and_then(|slot| {
                slot.lock()
                    .unwrap_or_else(std::sync::PoisonError::into_inner)
                    .take()
            });
        match writes {
            Some(writes) => writes.apply(self, file),
            None => self.index_file(file),
        }
    }
}

type AdminWrite =
    Box<dyn FnOnce(&AdminComponentIndexer, Option<&Mutation>) -> IndexResult<()> + Send + Sync>;

/// Repository writes and workspace symbols recorded while parsing one file,
/// replayed in order by [`AdminWrites::apply`].
#[derive(Default)]
pub(crate) struct AdminWrites {
    ops: Vec<AdminWrite>,
    symbols: Vec<(usize, Vec<swlsp_indexer::WorkspaceSymbol>)>,
    invalidate: Vec<usize>,
}

impl AdminWrites {
    fn push(
        &mut self,
        write: impl FnOnce(&AdminComponentIndexer, Option<&Mutation>) -> IndexResult<()>
        + Send
        + Sync
        + 'static,
    ) {
        self.ops.push(Box::new(write));
    }

    /// Inserts a write before the operation at `position`.
    fn insert(
        &mut self,
        position: usize,
        write: impl FnOnce(&AdminComponentIndexer, Option<&Mutation>) -> IndexResult<()>
        + Send
        + Sync
        + 'static,
    ) {
        self.ops.insert(position, Box::new(write));
        for (at, _) in &mut self.symbols {
            if *at >= position {
                *at += 1;
            }
        }
        for at in &mut self.invalidate {
            if *at >= position {
                *at += 1;
            }
        }
    }

    /// Records workspace symbols published at this point of the replay.
    pub(crate) fn add_workspace_symbols(&mut self, symbols: Vec<swlsp_indexer::WorkspaceSymbol>) {
        self.symbols.push((self.ops.len(), symbols));
        self.ops.push(Box::new(|_, _| Ok(())));
    }

    fn invalidate_caches(&mut self) {
        self.invalidate.push(self.ops.len());
        self.ops.push(Box::new(|_, _| Ok(())));
    }

    /// Replays the recorded writes against `file`'s mutation.
    fn apply(self, indexer: &AdminComponentIndexer, file: &ParsedFile) -> IndexResult<()> {
        let mutation = file.mutation();
        let mut symbols = self.symbols.into_iter().peekable();
        let mut invalidate = self.invalidate.into_iter().peekable();
        for (position, write) in self.ops.into_iter().enumerate() {
            if invalidate.next_if_eq(&position).is_some() {
                indexer.invalidate_template_component_cache();
            }
            if let Some((_, symbols)) = symbols.next_if(|(at, _)| *at == position) {
                file.add_workspace_symbols(symbols);
            }
            write(indexer, mutation)?;
        }
        Ok(())
    }
}

impl swlsp_indexer::SupplementalPathIndexer for AdminComponentIndexer {
    fn should_enter_directory(&self, directory: &str) -> bool {
        let Some(relative) = meteor_package_relative_path(directory) else {
            return false;
        };
        let relative = gopath::to_slash(&relative);
        relative.is_empty()
            || relative == "@shopware-ag"
            || relative == METEOR_PACKAGE_PATH
            || relative == format!("{METEOR_PACKAGE_PATH}/dist")
            || relative == format!("{METEOR_PACKAGE_PATH}/dist/esm")
    }

    fn should_index_path(&self, file_path: &str) -> bool {
        is_meteor_declaration_path(file_path)
    }
}

impl swlsp_indexer::SupplementalSyntaxIndexer for AdminComponentIndexer {
    fn should_preparse_path(&self, _path: &str) -> bool {
        false
    }
}

impl swlsp_indexer::TransactionalRemover for AdminComponentIndexer {
    fn removed_files_in(&self, paths: &[String], mutation: &Mutation) -> IndexResult<()> {
        self.removed_files_impl(paths, Some(mutation))
    }
}

impl swlsp_indexer::TransactionalClearer for AdminComponentIndexer {
    fn clear_in(&self, mutation: &Mutation) -> IndexResult<()> {
        self.clear_impl(Some(mutation))
    }
}

/// Keeps only `<script>` bodies of a Vue SFC at their original offsets; every
/// other byte except line breaks becomes a space.
pub(crate) fn vue_script_type_source(
    source: &str,
    sections: &[swlsp_parser_vue::Section],
) -> String {
    let bytes = source.as_bytes();
    let mut masked: Vec<u8> = bytes
        .iter()
        .map(|&byte| {
            if byte == b'\n' || byte == b'\r' {
                byte
            } else {
                b' '
            }
        })
        .collect();
    for section in sections {
        let start = usize::from(section.body_range.start());
        let end = usize::from(section.body_range.end());
        if section.kind != swlsp_parser_vue::SectionKind::Script || end > bytes.len() {
            continue;
        }
        masked[start..end].copy_from_slice(&bytes[start..end]);
    }
    match String::from_utf8(masked) {
        Ok(text) => text,
        Err(error) => String::from_utf8_lossy(error.as_bytes()).into_owned(),
    }
}

pub(crate) fn is_administration_source_path(file_path: &str) -> bool {
    let mut normalized = gopath::to_slash(&gopath::clean(file_path));
    if !normalized.starts_with('/') {
        normalized.insert(0, '/');
    }
    normalized.contains("/Resources/app/administration/src/")
}

pub(crate) fn meteor_package_relative_path(file_path: &str) -> Option<String> {
    let normalized = gopath::to_slash(&gopath::clean(file_path));
    const MARKER: &str = "/Resources/app/administration/node_modules/";
    match normalized.find(MARKER) {
        Some(position) => Some(
            normalized[position + MARKER.len()..]
                .trim_start_matches('/')
                .to_owned(),
        ),
        None => normalized
            .ends_with(MARKER.trim_end_matches('/'))
            .then(String::new),
    }
}

pub(crate) fn is_meteor_declaration_path(file_path: &str) -> bool {
    let Some(relative) = meteor_package_relative_path(file_path) else {
        return false;
    };
    let normalized = gopath::to_slash(&relative);
    if !normalized.starts_with(&format!("{METEOR_PACKAGE_PATH}/dist/esm/")) {
        return false;
    }
    let base = gopath::base(&normalized);
    base.starts_with("Mt") && base.ends_with(".d.ts")
}

/// `/path/mt-card/index.ts` -> `mt-card`; `/path/sw-button.js` -> `sw-button`.
pub(crate) fn derive_component_name_from_path(file_path: &str) -> String {
    let base = gopath::base(file_path);
    if base == "index.js" || base == "index.ts" || base == "index.vue" {
        return gopath::base(&gopath::dir(file_path));
    }
    let ext = gopath::ext(&base);
    base.strip_suffix(ext).unwrap_or(&base).to_owned()
}

pub(crate) fn merge_component_definitions(
    mut base: ComponentDefinition,
    overlay: &ComponentDefinition,
) -> ComponentDefinition {
    base.props = overlay_script_setup_props(&base.props, &overlay.props);
    base.members = overlay_script_setup_members(&base.members, &overlay.members);
    append_unique_values(&mut base.methods, &overlay.methods);
    append_unique_values(&mut base.computed, &overlay.computed);
    append_unique_values(&mut base.data, &overlay.data);
    append_unique_values(&mut base.injected, &overlay.injected);
    append_unique_values(&mut base.mixins, &overlay.mixins);
    append_unique_values(&mut base.emits, &overlay.emits);
    for event in &overlay.events {
        append_component_event(&mut base.events, event.clone());
    }
    append_unique_values(
        &mut base.script_setup_prop_types,
        &overlay.script_setup_prop_types,
    );
    append_unique_values(
        &mut base.script_setup_event_types,
        &overlay.script_setup_event_types,
    );
    append_unique_values(
        &mut base.script_setup_slot_types,
        &overlay.script_setup_slot_types,
    );
    base.script_setup_prop_defaults = overlay_script_setup_prop_defaults(
        &base.script_setup_prop_defaults,
        &overlay.script_setup_prop_defaults,
    );
    base.script_setup_prop_bindings = overlay_script_setup_prop_bindings(
        &base.script_setup_prop_bindings,
        &overlay.script_setup_prop_bindings,
    );
    base.local_components =
        overlay_local_components(&base.local_components, &overlay.local_components);
    base.local_directives =
        overlay_local_directives(&base.local_directives, &overlay.local_directives);
    base.assignments.extend(overlay.assignments.iter().cloned());
    base.open_runtime_members = base.open_runtime_members || overlay.open_runtime_members;
    if !overlay.model_prop.is_empty() || !overlay.model_event.is_empty() {
        base.model_prop = overlay.model_prop.clone();
        base.model_event = overlay.model_event.clone();
    }
    if !overlay.deprecated.is_empty() {
        base.deprecated = overlay.deprecated.clone();
    }
    base
}

/// Go `appendUniqueValues`: appends non-empty values not yet present.
pub(crate) fn append_unique_values(values: &mut Vec<String>, additions: &[String]) {
    for value in additions {
        if value.is_empty() || values.contains(value) {
            continue;
        }
        values.push(value.clone());
    }
}

/// A normalized key for a definition file: no extension, no `/index`.
pub(crate) fn normalize_definition_path(file_path: &str) -> String {
    let ext = gopath::ext(file_path);
    let normalized = file_path.strip_suffix(ext).unwrap_or(file_path);
    normalized
        .strip_suffix("/index")
        .unwrap_or(normalized)
        .to_owned()
}
