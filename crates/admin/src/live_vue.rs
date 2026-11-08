//! Open-document overlays (port of Go `live_vue.go`).

use std::sync::Arc;

use crate::*;

#[derive(Clone, Debug, Default, PartialEq)]
pub(crate) struct LiveLegacyDocument {
    pub definition: ComponentDefinition,
    pub has_definition: bool,
    pub registrations: Vec<VueComponent>,
    pub mixins: Vec<AdminMixin>,
    pub modules: Vec<AdminModule>,
    pub services: Vec<AdminService>,
    pub stores: Vec<AdminStore>,
    pub store_factory: Option<AdminStoreFactory>,
    pub directives: Vec<AdminDirective>,
    pub filters: Vec<AdminFilter>,
    pub cms: Vec<AdminCMSRegistration>,
    pub privileges: Vec<AdminPrivilege>,
}

#[derive(Clone, Debug, Default, PartialEq)]
pub(crate) struct LiveLegacyDocumentSnapshot {
    pub path: String,
    pub document: Arc<LiveLegacyDocument>,
}

impl AdminComponentIndexer {
    /// Overlays an open document on the persisted effective component without
    /// mutating the persisted generation. `root` is the document's syntax
    /// tree; `None` falls back to the persisted component.
    pub fn get_component_for_document(
        &self,
        file_path: &str,
        root: Option<&SyntaxNode>,
        source: &str,
        line_index: Option<&LineIndex>,
    ) -> IndexResult<Option<VueComponent>> {
        let Some(root) = root else {
            return self.get_component_by_template_path(file_path);
        };
        let owned_index;
        let line_index = match line_index {
            Some(index) => index,
            None => {
                owned_index = LineIndex::new(source);
                &owned_index
            }
        };
        if gopath::ext(file_path).eq_ignore_ascii_case(".twig") {
            return self.component_for_live_twig_document(file_path, root, source, line_index);
        }
        if !is_vue_component_path(file_path) {
            return self.get_component_by_template_path(file_path);
        }
        let Some((definition, live_types)) =
            parse_live_vue_definition(file_path, root, source, line_index)
        else {
            return self.get_component_by_template_path(file_path);
        };
        self.component_for_live_vue_definition(file_path, &definition, &live_types)
            .map(Some)
    }

    pub(crate) fn component_for_live_vue_definition(
        &self,
        file_path: &str,
        raw_definition: &ComponentDefinition,
        live_types: &[AdminTypeFile],
    ) -> IndexResult<VueComponent> {
        let component = self.get_component_by_template_path(file_path)?;
        let mut definition = raw_definition.clone();
        self.enrich_script_setup_type_contracts(&mut definition, live_types)?;
        let mut result = VueComponent {
            name: derive_component_name_from_path(file_path),
            definition_path: file_path.to_owned(),
            template_path: file_path.to_owned(),
            ..Default::default()
        };
        if let Some(component) = component {
            result = component;
            if let Some(persisted) = self.get_component_definition(file_path)? {
                strip_component_definition(&mut result, &persisted, file_path);
            }
        }
        let mut live = VueComponent {
            name: result.name.clone(),
            definition_path: file_path.to_owned(),
            template_path: file_path.to_owned(),
            ..Default::default()
        };
        apply_definition(&mut live, &definition);
        let mut result = overlay_components(&result, &live);
        result.live_type_files = live_types.to_vec();
        Ok(result)
    }

    /// Whether an editor overlay is published for `file_path`.
    pub fn has_live_document(&self, file_path: &str) -> bool {
        if file_path.is_empty() {
            return false;
        }
        let file_path = gopath::clean(file_path);
        let live = read_lock(&self.live);
        live.vue_documents.contains_key(&file_path)
            || live.legacy_documents.contains_key(&file_path)
            || live.runtime_documents.contains_key(&file_path)
            || live.twig_templates.contains_key(&file_path)
            || live.type_files.contains_key(&file_path)
    }

    /// Publishes one immutable editor snapshot into the overlay. Only compact
    /// parsed contracts are retained.
    pub fn update_live_document(
        &self,
        file_path: &str,
        root: Option<&SyntaxNode>,
        source: &str,
        line_index: Option<&LineIndex>,
    ) {
        if file_path.is_empty() || !is_administration_source_path(file_path) {
            return;
        }
        let file_path = gopath::clean(file_path);
        let owned_index;
        let line_index = match line_index {
            Some(index) => index,
            None => {
                owned_index = LineIndex::new(source);
                &owned_index
            }
        };
        match gopath::ext(&file_path).to_lowercase().as_str() {
            ".ts" => {
                let type_file = parse_admin_type_file(&file_path, source, Some(line_index));
                let legacy =
                    root.map(|root| parse_live_legacy_document(&file_path, root, line_index));
                {
                    let mut live = write_lock(&self.live);
                    live.type_files.insert(file_path.clone(), type_file);
                    if let Some(legacy) = legacy {
                        live.legacy_documents
                            .insert(file_path.clone(), Arc::new(legacy));
                        live.legacy_generation += 1;
                    }
                }
                self.invalidate_template_component_cache();
            }
            ".js" => {
                let Some(root) = root else {
                    return;
                };
                let legacy = parse_live_legacy_document(&file_path, root, line_index);
                {
                    let mut live = write_lock(&self.live);
                    live.legacy_documents
                        .insert(file_path.clone(), Arc::new(legacy));
                    live.legacy_generation += 1;
                }
                self.invalidate_template_component_cache();
            }
            ".twig" => {
                let Some(root) = root else {
                    return;
                };
                let mut template = parse_template_tree(root, source, line_index);
                set_template_source_paths(&mut template, &file_path);
                write_lock(&self.live)
                    .twig_templates
                    .insert(file_path.clone(), template);
                self.invalidate_template_component_cache();
            }
            ".vue" => {
                let Some(root) = root else {
                    return;
                };
                let runtime = parse_live_runtime_document(&file_path, root, line_index);
                let parsed = parse_live_vue_definition(&file_path, root, source, line_index);
                {
                    let mut live = write_lock(&self.live);
                    live.runtime_documents
                        .insert(file_path.clone(), Arc::new(runtime));
                    match parsed {
                        Some((definition, mut live_types)) => {
                            if !live_types.is_empty() {
                                live.type_files
                                    .insert(file_path.clone(), live_types.swap_remove(0));
                            }
                            live.vue_documents.insert(file_path.clone(), definition);
                        }
                        None => {
                            live.vue_documents.remove(&file_path);
                        }
                    }
                }
                self.invalidate_template_component_cache();
            }
            _ => {}
        }
    }

    /// Discards an editor overlay on didClose.
    pub fn remove_live_document(&self, file_path: &str) {
        if file_path.is_empty() {
            return;
        }
        let file_path = gopath::clean(file_path);
        let removed = {
            let mut live = write_lock(&self.live);
            let mut removed = live.vue_documents.remove(&file_path).is_some();
            if live.legacy_documents.remove(&file_path).is_some() {
                live.legacy_generation += 1;
                removed = true;
            }
            removed |= live.runtime_documents.remove(&file_path).is_some();
            removed |= live.twig_templates.remove(&file_path).is_some();
            removed |= live.type_files.remove(&file_path).is_some();
            removed
        };
        // Closing a document that never had an overlay changes nothing.
        if removed {
            self.invalidate_template_component_cache();
        }
    }

    pub(crate) fn component_for_live_twig_document(
        &self,
        file_path: &str,
        root: &SyntaxNode,
        source: &str,
        line_index: &LineIndex,
    ) -> IndexResult<Option<VueComponent>> {
        let Some(component) = self.get_component_by_template_path(file_path)? else {
            return Ok(None);
        };
        let mut template = parse_template_tree(root, source, line_index);
        set_template_source_paths(&mut template, file_path);
        inherit_live_template_block_contracts(&mut template, &component, file_path);
        let mut result = component;
        strip_component_template_source(&mut result, file_path);
        let live = VueComponent {
            name: result.name.clone(),
            template_path: file_path.to_owned(),
            slots: template.slots,
            blocks: template.blocks,
            ..Default::default()
        };
        Ok(Some(overlay_components(&result, &live)))
    }

    pub(crate) fn apply_live_twig_template(&self, definition: &mut ComponentDefinition) {
        if definition.template_path.is_empty() {
            return;
        }
        let path = gopath::clean(&definition.template_path);
        let live = read_lock(&self.live);
        let Some(template) = live.twig_templates.get(&path) else {
            return;
        };
        definition.slots = template.slots.clone();
        definition.blocks = template.blocks.clone();
    }

    /// The live legacy definition for `file_path` and whether an open
    /// document shadows the persisted one.
    pub(crate) fn live_legacy_definition(
        &self,
        file_path: &str,
    ) -> IndexResult<(Option<ComponentDefinition>, bool)> {
        if file_path.is_empty() {
            return Ok((None, false));
        }
        let path = gopath::clean(file_path);
        let document = {
            let live = read_lock(&self.live);
            match live.legacy_documents.get(&path) {
                Some(document) => Some(document.clone()),
                None => {
                    let normalized = normalize_definition_path(&path);
                    let mut found = None;
                    for (candidate_path, candidate) in &live.legacy_documents {
                        if normalize_definition_path(candidate_path) == normalized {
                            found = Some(candidate.clone());
                            break;
                        }
                    }
                    found
                }
            }
        };
        let Some(document) = document else {
            return Ok((None, false));
        };
        if !document.has_definition {
            return Ok((None, true));
        }
        let mut definition = document.definition.clone();
        self.apply_live_twig_template(&mut definition);
        self.enrich_script_setup_type_contracts(&mut definition, &[])?;
        Ok((Some(definition), true))
    }

    pub(crate) fn is_live_legacy_document_path(&self, file_path: &str) -> bool {
        if file_path.is_empty() {
            return false;
        }
        let path = gopath::clean(file_path);
        let normalized = normalize_definition_path(&path);
        let live = read_lock(&self.live);
        live.legacy_documents.contains_key(&path)
            || live
                .legacy_documents
                .keys()
                .any(|candidate| normalize_definition_path(candidate) == normalized)
    }

    /// The template path of [`Self::live_legacy_definition`] without
    /// cloning or enriching the definition.
    pub(crate) fn live_legacy_template_path(&self, file_path: &str) -> (Option<String>, bool) {
        if file_path.is_empty() {
            return (None, false);
        }
        let live = read_lock(&self.live);
        if live.legacy_documents.is_empty() {
            return (None, false);
        }
        let path = gopath::clean(file_path);
        let document = match live.legacy_documents.get(&path) {
            Some(document) => Some(document),
            None => {
                let normalized = normalize_definition_path(&path);
                live.legacy_documents
                    .iter()
                    .find(|(candidate_path, _)| {
                        normalize_definition_path(candidate_path) == normalized
                    })
                    .map(|(_, candidate)| candidate)
            }
        };
        match document {
            None => (None, false),
            Some(document) if !document.has_definition => (None, true),
            Some(document) => (Some(document.definition.template_path.clone()), true),
        }
    }

    pub(crate) fn definition_with_live_template(
        &self,
        definition: &ComponentDefinition,
    ) -> IndexResult<ComponentDefinition> {
        let mut result = definition.clone();
        self.apply_live_twig_template(&mut result);
        self.enrich_script_setup_type_contracts(&mut result, &[])?;
        Ok(result)
    }

    /// Persisted registrations with open legacy documents applied as file
    /// replacements; returns `persisted` unchanged when nothing is open.
    pub(crate) fn registrations_with_live_documents(
        &self,
        persisted: Arc<[VueComponent]>,
        name: &str,
    ) -> Arc<[VueComponent]> {
        let (documents, generation) = {
            let live = read_lock(&self.live);
            if live.legacy_documents.is_empty() {
                return persisted;
            }
            let documents: BTreeMap<String, Arc<LiveLegacyDocument>> = live
                .legacy_documents
                .iter()
                .map(|(path, document)| (path.clone(), document.clone()))
                .collect();
            (documents, live.legacy_generation)
        };
        // The full catalog is requested by most admin features; merging it
        // clones every component, so reuse the merge until the persisted
        // view or an open legacy document changes.
        if name.is_empty() {
            let cache = self
                .merged_components
                .lock()
                .unwrap_or_else(std::sync::PoisonError::into_inner);
            if let Some(cached) = cache.as_ref()
                && cached.generation == generation
                && Arc::ptr_eq(&cached.persisted, &persisted)
            {
                return cached.merged.clone();
            }
            // A document whose registrations equal its persisted rows (the
            // usual case: a file opened without edits) replaces those rows
            // with identical values, so it leaves the catalog unchanged.
            // Skipping it avoids cloning the whole catalog on every open.
            drop(cache);
            let overriding = documents.iter().any(|(path, document)| {
                !self
                    .component_index
                    .get_values_by_path_view(path)
                    .is_ok_and(|rows| *rows == *document.registrations)
            });
            if !overriding {
                *self
                    .merged_components
                    .lock()
                    .unwrap_or_else(std::sync::PoisonError::into_inner) = Some(MergedComponents {
                    persisted: persisted.clone(),
                    generation,
                    merged: persisted.clone(),
                });
                return persisted;
            }
        }
        let mut result = Vec::with_capacity(persisted.len() + documents.len());
        for component in persisted.iter() {
            if documents.contains_key(&gopath::clean(&component.file_path)) {
                continue;
            }
            if name.is_empty() || component.name == name {
                result.push(component.clone());
            }
        }
        for document in documents.values() {
            for component in &document.registrations {
                if name.is_empty() || component.name == name {
                    result.push(component.clone());
                }
            }
        }
        let merged: Arc<[VueComponent]> = result.into();
        if name.is_empty() {
            *self
                .merged_components
                .lock()
                .unwrap_or_else(std::sync::PoisonError::into_inner) = Some(MergedComponents {
                persisted,
                generation,
                merged: merged.clone(),
            });
        }
        merged
    }

    /// Open legacy and runtime documents sorted by path.
    pub(crate) fn live_legacy_document_snapshots(&self) -> Vec<LiveLegacyDocumentSnapshot> {
        let live = read_lock(&self.live);
        let mut documents: BTreeMap<&String, &Arc<LiveLegacyDocument>> = BTreeMap::new();
        for (path, document) in &live.legacy_documents {
            documents.insert(path, document);
        }
        for (path, document) in &live.runtime_documents {
            documents.insert(path, document);
        }
        documents
            .into_iter()
            .map(|(path, document)| LiveLegacyDocumentSnapshot {
                path: path.clone(),
                document: document.clone(),
            })
            .collect()
    }

    pub(crate) fn live_vue_component(&self, file_path: &str) -> IndexResult<Option<VueComponent>> {
        if file_path.is_empty() {
            return Ok(None);
        }
        let file_path = gopath::clean(file_path);
        let (definition, type_file) = {
            let live = read_lock(&self.live);
            let Some(definition) = live.vue_documents.get(&file_path) else {
                return Ok(None);
            };
            (definition.clone(), live.type_files.get(&file_path).cloned())
        };
        let live_types: Vec<AdminTypeFile> = type_file.into_iter().collect();
        self.component_for_live_vue_definition(&file_path, &definition, &live_types)
            .map(Some)
    }

    /// [`Self::component_with_live_vue_document`] for a shared component:
    /// returns it unchanged (no copy) when no open Vue document overlays it.
    pub(crate) fn shared_component_with_live_vue_document(
        &self,
        component: Arc<VueComponent>,
    ) -> IndexResult<Arc<VueComponent>> {
        let overlaid = {
            let live = read_lock(&self.live);
            !live.vue_documents.is_empty()
                && [
                    &component.definition_path,
                    &component.template_path,
                    &component.file_path,
                ]
                .into_iter()
                .any(|candidate| {
                    !candidate.is_empty()
                        && live.vue_documents.contains_key(&gopath::clean(candidate))
                })
        };
        if !overlaid {
            return Ok(component);
        }
        self.component_with_live_vue_document(Arc::unwrap_or_clone(component))
            .map(Arc::new)
    }

    pub(crate) fn component_with_live_vue_document(
        &self,
        component: VueComponent,
    ) -> IndexResult<VueComponent> {
        for candidate in [
            &component.definition_path,
            &component.template_path,
            &component.file_path,
        ] {
            let Some(mut live) = self.live_vue_component(candidate)? else {
                continue;
            };
            if !component.name.is_empty() {
                live.name = component.name.clone();
            }
            live.kind = component.kind;
            live.target_component = component.target_component.clone();
            live.extends_component = component.extends_component.clone();
            live.import_path = component.import_path.clone();
            if !component.file_path.is_empty() {
                live.file_path = component.file_path.clone();
            }
            if !component.definition_path.is_empty() {
                live.definition_path = component.definition_path.clone();
            }
            if component.line != 0 {
                live.line = component.line;
            }
            return Ok(live);
        }
        Ok(component)
    }

    /// Open type files plus request-local files by cleaned path; empty when
    /// there are none (Go `nil`).
    pub(crate) fn live_type_file_overlays(
        &self,
        request_files: &[AdminTypeFile],
    ) -> HashMap<String, AdminTypeFile> {
        let mut result: HashMap<String, AdminTypeFile> = read_lock(&self.live).type_files.clone();
        for file in request_files {
            if !file.file_path.is_empty() {
                result.insert(gopath::clean(&file.file_path), file.clone());
            }
        }
        result
    }
}

/// Parses an open Vue SFC's definition plus its compact TypeScript context.
pub(crate) fn parse_live_vue_definition(
    file_path: &str,
    root: &SyntaxNode,
    source: &str,
    line_index: &LineIndex,
) -> Option<(ComponentDefinition, Vec<AdminTypeFile>)> {
    let sections = swlsp_parser_vue::sections(source);
    let definition = parse_vue_definition(file_path, root, source, line_index, &sections)?;
    let live_types = vec![parse_admin_type_file(
        file_path,
        &vue_script_type_source(source, &sections),
        Some(line_index),
    )];
    Some((definition, live_types))
}

pub(crate) fn parse_live_legacy_document(
    file_path: &str,
    root: &SyntaxNode,
    line_index: &LineIndex,
) -> LiveLegacyDocument {
    let mut result = parse_live_runtime_document(file_path, root, line_index);
    result.registrations =
        parse_component_registrations_with_line_index(root, file_path, line_index);
    let exports = jsquery::export_defaults(root);
    let Some(first) = exports.first() else {
        return result;
    };
    let Some(expression) = jsquery::export_default_expression(first) else {
        return result;
    };
    if component_definition_object(&expression).is_none() {
        return result;
    }
    let mut definition = parse_component_definition_with_line_index(root, line_index);
    set_definition_file_path(&mut definition, file_path);
    if !definition.template_path.is_empty() && !gopath::is_abs(&definition.template_path) {
        definition.template_path = resolve_template_path(file_path, &definition.template_path);
    }
    if !definition.template_path.is_empty()
        && let Some(template) = template_from_file(&definition.template_path)
    {
        definition.slots = template.slots;
        definition.blocks = template.blocks;
    }
    result.definition = definition.clone();
    result.has_definition = true;

    if matches!(
        jsquery::call_name(&expression).as_deref(),
        Some("Shopware.Component.wrapComponentConfig" | "Component.wrapComponentConfig")
    ) {
        let (line, _) = line_index.position(u32::from(first.range_trimmed_trivia().start()));
        result.registrations.push(VueComponent {
            name: derive_component_name_from_path(file_path),
            file_path: file_path.to_owned(),
            definition_path: file_path.to_owned(),
            line: line + 1,
            inline_definition: Some(Box::new(definition)),
            ..Default::default()
        });
    }
    result
}

pub(crate) fn parse_live_runtime_document(
    file_path: &str,
    root: &SyntaxNode,
    line_index: &LineIndex,
) -> LiveLegacyDocument {
    let (mixins, modules) = parse_mixins_and_modules(root, file_path, line_index);
    let (services, stores) = parse_admin_runtime_registries(root, file_path, line_index);
    LiveLegacyDocument {
        mixins,
        modules,
        services,
        stores,
        store_factory: parse_admin_store_factory(root, file_path, line_index),
        directives: parse_admin_directives(root, file_path, line_index),
        filters: parse_admin_filters(root, file_path, line_index),
        cms: parse_admin_cms_registrations(root, file_path, line_index),
        privileges: parse_admin_privileges(root, file_path, line_index),
        ..Default::default()
    }
}

pub(crate) fn inherit_live_template_block_contracts(
    template: &mut TemplateParseResult,
    effective: &VueComponent,
    file_path: &str,
) {
    let normalized = normalize_definition_path(file_path);
    for block in &mut template.blocks {
        let Some(current) = effective.component_block(&block.name) else {
            continue;
        };
        let inherited: Vec<TwigBlockScopeMember> = current
            .scope_members
            .iter()
            .filter(|member| {
                member.file_path.is_empty()
                    || normalize_definition_path(&member.file_path) != normalized
            })
            .cloned()
            .collect();
        block.scope_members = overlay_block_scope_members(&inherited, &block.scope_members);
        if block.deprecated.is_empty() {
            block.deprecated = current.deprecated.clone();
        }
    }
}

pub(crate) fn strip_component_template_source(component: &mut VueComponent, file_path: &str) {
    if file_path.is_empty() {
        return;
    }
    let normalized = normalize_definition_path(file_path);
    component
        .slots
        .retain(|slot| normalize_definition_path(&slot.file_path) != normalized);
    component
        .blocks
        .retain(|block| normalize_definition_path(&block.file_path) != normalized);
}

/// Applies editor snapshots as file replacements: an open source file
/// shadows every persisted value it previously owned.
pub(crate) fn overlay_live_legacy_values<T: Clone>(
    persisted: Vec<T>,
    documents: &[LiveLegacyDocumentSnapshot],
    file_path: impl Fn(&T) -> &str,
    values: impl Fn(&LiveLegacyDocument) -> &[T],
    matches: Option<&dyn Fn(&T) -> bool>,
) -> Vec<T> {
    let accepts = |value: &T| matches.is_none_or(|matches| matches(value));
    if documents.is_empty() {
        if matches.is_none() {
            return persisted;
        }
        return persisted
            .into_iter()
            .filter(|value| accepts(value))
            .collect();
    }
    let replaced: HashSet<String> = documents
        .iter()
        .map(|document| gopath::clean(&document.path))
        .collect();
    let mut result = Vec::with_capacity(persisted.len() + documents.len());
    for value in persisted {
        if replaced.contains(&gopath::clean(file_path(&value))) {
            continue;
        }
        if accepts(&value) {
            result.push(value);
        }
    }
    for document in documents {
        for value in values(&document.document) {
            if accepts(value) {
                result.push(value.clone());
            }
        }
    }
    result
}

pub(crate) fn is_vue_component_path(file_path: &str) -> bool {
    gopath::ext(file_path) == ".vue"
}

/// Removes everything the persisted definition of `file_path` contributed,
/// so the live definition can replace it.
pub(crate) fn strip_component_definition(
    component: &mut VueComponent,
    definition: &ComponentDefinition,
    file_path: &str,
) {
    let prop_names: HashSet<String> = definition
        .props
        .iter()
        .map(|prop| prop.name.clone())
        .collect();
    component.props = filter_component_props(&component.props, &prop_names);
    let event_names: HashSet<String> = definition
        .events
        .iter()
        .map(|event| canonical_event_name(&event.name))
        .collect();
    component.events = filter_component_events(&component.events, &event_names);
    component.emits = filter_component_names(&component.emits, &event_names);
    component.methods =
        filter_component_names(&component.methods, &string_set(&definition.methods));
    component.computed =
        filter_component_names(&component.computed, &string_set(&definition.computed));
    component.data = filter_component_names(&component.data, &string_set(&definition.data));
    component.injected =
        filter_component_names(&component.injected, &string_set(&definition.injected));
    let member_names: HashSet<String> = definition
        .members
        .iter()
        .map(|member| member.name.clone())
        .collect();
    component.members = filter_component_members(&component.members, file_path, &member_names);
    component.assignments = filter_component_assignments(&component.assignments, file_path);
    component.local_components = filter_local_components(&component.local_components, file_path);
    component.local_directives = filter_local_directives(&component.local_directives, file_path);
    component.slots =
        filter_component_slots(&component.slots, &slot_identity_set(&definition.slots));
    component.blocks =
        filter_component_blocks(&component.blocks, &string_set_blocks(&definition.blocks));
    if !definition.model_prop.is_empty() || !definition.model_event.is_empty() {
        component.model_prop.clear();
        component.model_event.clear();
    }
}

pub(crate) fn string_set(values: &[String]) -> HashSet<String> {
    values.iter().cloned().collect()
}

pub(crate) fn string_set_blocks(values: &[TwigBlock]) -> HashSet<String> {
    values.iter().map(|value| value.name.clone()).collect()
}

pub(crate) fn slot_identity_set(values: &[VueComponentSlot]) -> HashSet<String> {
    values.iter().map(VueComponentSlot::identity_key).collect()
}

pub(crate) fn filter_component_props(
    values: &[VueComponentProp],
    remove: &HashSet<String>,
) -> Vec<VueComponentProp> {
    values
        .iter()
        .filter(|value| !remove.contains(&value.name))
        .cloned()
        .collect()
}

pub(crate) fn filter_component_events(
    values: &[VueComponentEvent],
    remove: &HashSet<String>,
) -> Vec<VueComponentEvent> {
    values
        .iter()
        .filter(|value| !remove.contains(&canonical_event_name(&value.name)))
        .cloned()
        .collect()
}

pub(crate) fn filter_component_names(values: &[String], remove: &HashSet<String>) -> Vec<String> {
    values
        .iter()
        .filter(|value| !remove.contains(*value) && !remove.contains(&canonical_event_name(value)))
        .cloned()
        .collect()
}

pub(crate) fn filter_component_members(
    values: &[VueComponentMember],
    file_path: &str,
    remove: &HashSet<String>,
) -> Vec<VueComponentMember> {
    let normalized = normalize_definition_path(file_path);
    values
        .iter()
        .filter(|value| {
            !remove.contains(&value.name)
                && normalize_definition_path(&value.file_path) != normalized
        })
        .cloned()
        .collect()
}

pub(crate) fn filter_component_assignments(
    values: &[VueComponentAssignment],
    file_path: &str,
) -> Vec<VueComponentAssignment> {
    let normalized = normalize_definition_path(file_path);
    values
        .iter()
        .filter(|value| normalize_definition_path(&value.file_path) != normalized)
        .cloned()
        .collect()
}

pub(crate) fn filter_local_components(
    values: &[VueLocalComponent],
    file_path: &str,
) -> Vec<VueLocalComponent> {
    let normalized = normalize_definition_path(file_path);
    values
        .iter()
        .filter(|value| normalize_definition_path(&value.file_path) != normalized)
        .cloned()
        .collect()
}

pub(crate) fn filter_local_directives(
    values: &[VueLocalDirective],
    file_path: &str,
) -> Vec<VueLocalDirective> {
    let normalized = normalize_definition_path(file_path);
    values
        .iter()
        .filter(|value| normalize_definition_path(&value.file_path) != normalized)
        .cloned()
        .collect()
}

pub(crate) fn filter_component_slots(
    values: &[VueComponentSlot],
    remove: &HashSet<String>,
) -> Vec<VueComponentSlot> {
    values
        .iter()
        .filter(|value| !remove.contains(&value.identity_key()))
        .cloned()
        .collect()
}

pub(crate) fn filter_component_blocks(
    values: &[TwigBlock],
    remove: &HashSet<String>,
) -> Vec<TwigBlock> {
    values
        .iter()
        .filter(|value| !remove.contains(&value.name))
        .cloned()
        .collect()
}
