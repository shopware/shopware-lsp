//! Effective component assembly (port of Go `indexer_effective.go`).

use std::sync::Arc;

use crate::*;

impl AdminComponentIndexer {
    pub fn get_all_component_names(&self) -> IndexResult<Vec<String>> {
        let components = self.get_all_components_view()?;
        let mut seen = HashSet::with_capacity(components.len());
        let mut result = Vec::with_capacity(components.len());
        for component in components.iter() {
            if component.name.is_empty() || !seen.insert(component.name.as_str()) {
                continue;
            }
            result.push(component.name.clone());
        }
        result.sort();
        Ok(result)
    }

    /// Effective components whose inline or imported configuration is owned
    /// by `file_path`.
    pub fn get_components_by_definition_path(
        &self,
        file_path: &str,
    ) -> IndexResult<Vec<VueComponent>> {
        let normalized = normalize_definition_path(file_path);
        let mut names = BTreeSet::new();
        if !normalized.is_empty() {
            self.definition_path_names(&normalized, file_path, &mut names)?;
            let mut result = Vec::with_capacity(names.len());
            for name in names {
                if let Some(component) = self.get_effective_component(&name)? {
                    result.push(component);
                }
            }
            return Ok(result);
        }
        let components = self.get_all_components_view()?;
        for component in components.iter() {
            let mut definition_path = component.definition_path.as_str();
            if definition_path.is_empty() && component.file_path == file_path {
                definition_path = &component.file_path;
            }
            if normalize_definition_path(definition_path) == normalized {
                names.insert(component.name.clone());
            }
        }
        let mut result = Vec::with_capacity(names.len());
        for name in names {
            if let Some(component) = self.get_effective_component(&name)? {
                result.push(component);
            }
        }
        Ok(result)
    }

    /// Names whose definition path normalizes to `normalized` (or, without a
    /// definition path, whose file is `file_path`). Equivalent to scanning
    /// [`Self::get_all_components_view`], but the persisted catalog is indexed
    /// once per repository view and open legacy documents are overlaid
    /// directly, so opening a document does not force a catalog rebuild.
    fn definition_path_names(
        &self,
        normalized: &str,
        file_path: &str,
        names: &mut BTreeSet<String>,
    ) -> IndexResult<()> {
        let persisted = self.component_index.get_all_values_view()?;
        let live: Vec<(String, Arc<LiveLegacyDocument>)> = read_lock(&self.live)
            .legacy_documents
            .iter()
            .map(|(path, document)| (path.clone(), document.clone()))
            .collect();
        let shadowed = |component_file: &str| {
            !live.is_empty()
                && live
                    .iter()
                    .any(|(path, _)| *path == gopath::clean(component_file))
        };
        {
            let mut cache = self
                .definition_paths
                .lock()
                .unwrap_or_else(std::sync::PoisonError::into_inner);
            if !cache
                .as_ref()
                .is_some_and(|index| Arc::ptr_eq(&index.catalog, &persisted))
            {
                let mut by_definition: HashMap<String, Vec<(String, String)>> = HashMap::new();
                let mut by_file: HashMap<String, Vec<(String, String)>> = HashMap::new();
                for component in persisted.iter() {
                    let entry = (component.name.clone(), component.file_path.clone());
                    if component.definition_path.is_empty() {
                        by_file
                            .entry(component.file_path.clone())
                            .or_default()
                            .push(entry);
                    } else {
                        by_definition
                            .entry(normalize_definition_path(&component.definition_path))
                            .or_default()
                            .push(entry);
                    }
                }
                *cache = Some(DefinitionPathIndex {
                    catalog: persisted.clone(),
                    by_definition,
                    by_file,
                });
            }
            let index = cache.as_ref().expect("definition path index");
            let found = index
                .by_definition
                .get(normalized)
                .into_iter()
                .chain(index.by_file.get(file_path))
                .flatten();
            for (name, component_file) in found {
                if !shadowed(component_file) {
                    names.insert(name.clone());
                }
            }
        }
        for (_, document) in &live {
            for component in &document.registrations {
                let matches = if component.definition_path.is_empty() {
                    component.file_path == file_path
                } else {
                    normalize_definition_path(&component.definition_path) == normalized
                };
                if matches {
                    names.insert(component.name.clone());
                }
            }
        }
        Ok(())
    }

    /// Registrations by name (several when extended or overridden).
    pub fn get_component(&self, name: &str) -> IndexResult<Vec<VueComponent>> {
        let components = self.component_index.get_values_view(name)?;
        Ok(self
            .registrations_with_live_documents(components, name)
            .to_vec())
    }

    /// The component definition stored for a definition path.
    pub fn get_component_definition(
        &self,
        definition_path: &str,
    ) -> IndexResult<Option<ComponentDefinition>> {
        let (definition, shadowed) = self.live_legacy_definition(definition_path)?;
        if definition.is_some() || shadowed {
            return Ok(definition);
        }
        let normalized_path = normalize_definition_path(definition_path);
        let definitions = self.definition_index.get_values_view(&normalized_path)?;
        let Some(first) = definitions.first() else {
            return Ok(None);
        };
        self.definition_with_live_template(first).map(Some)
    }

    /// The inline component definition by component name.
    pub fn get_component_definition_by_name(
        &self,
        name: &str,
    ) -> IndexResult<Option<ComponentDefinition>> {
        for component in self.get_component(name)? {
            if let Some(inline) = &component.inline_definition {
                return self.definition_with_live_template(inline).map(Some);
            }
        }
        let definitions = self.definition_index.get_values_view(name)?;
        let Some(first) = definitions
            .iter()
            .find(|definition| !self.is_live_legacy_document_path(&definition.file_path))
        else {
            return Ok(None);
        };
        self.definition_with_live_template(first).map(Some)
    }

    /// One concrete registration with its own definition attached, without
    /// parents, mixins or sibling overrides.
    pub fn get_component_registration_with_definition(
        &self,
        component: &VueComponent,
    ) -> IndexResult<VueComponent> {
        let mut component = component.clone();
        self.populate_component_definition(&mut component)?;
        Ok(component)
    }

    /// The effective component as a one-element list (empty when unknown).
    pub fn get_component_with_definition(&self, name: &str) -> IndexResult<Vec<VueComponent>> {
        Ok(self.get_effective_component(name)?.into_iter().collect())
    }

    /// The effective component: the extends chain, mixins and every
    /// registration and override overlaid with source locations retained.
    pub fn get_effective_component(&self, name: &str) -> IndexResult<Option<VueComponent>> {
        let mut resolving = HashSet::new();
        Ok(self
            .effective_component(name, &mut resolving)?
            .map(Arc::unwrap_or_clone))
    }

    pub(crate) fn effective_component(
        &self,
        name: &str,
        resolving: &mut HashSet<String>,
    ) -> IndexResult<Option<Arc<VueComponent>>> {
        if name.is_empty() || resolving.contains(name) {
            return Ok(None);
        }
        let epoch = {
            let cache = read_lock(&self.effective_cache);
            if let Some(cached) = cache.components.get(name) {
                return Ok(Some(cached.clone()));
            }
            cache.epoch
        };
        resolving.insert(name.to_owned());
        let result = self.assemble_effective_component(name, resolving);
        resolving.remove(name);
        let Some(result) = result? else {
            return Ok(None);
        };
        let result = Arc::new(result);
        let mut cache = write_lock(&self.effective_cache);
        if cache.epoch == epoch {
            cache.components.insert(name.to_owned(), result.clone());
        }
        Ok(Some(result))
    }

    fn assemble_effective_component(
        &self,
        name: &str,
        resolving: &mut HashSet<String>,
    ) -> IndexResult<Option<VueComponent>> {
        let mut components = self.get_component(name)?;
        if components.is_empty() {
            return Ok(None);
        }
        for component in &mut components {
            self.populate_component_definition(component)?;
        }
        components.sort_by(|left, right| {
            let left_override = left.kind == ComponentRegistrationKind::Override;
            let right_override = right.kind == ComponentRegistrationKind::Override;
            left_override
                .cmp(&right_override)
                .then_with(|| left.file_path.cmp(&right.file_path))
        });
        let mut result = VueComponent {
            name: name.to_owned(),
            ..Default::default()
        };
        for component in &components {
            let mut own = self.component_with_mixins(component)?;
            if !own.extends_component.is_empty() {
                let own_deprecated = own.deprecated.clone();
                let parent = self.effective_component(&own.extends_component.clone(), resolving)?;
                if let Some(parent) = parent {
                    own = overlay_components(&parent, &own);
                    // Extending a deprecated component does not deprecate the
                    // child registry name.
                    own.deprecated = own_deprecated;
                }
            }
            result = overlay_components(&result, &own);
        }
        self.enrich_effective_component_member_types(&mut result)?;
        Ok(Some(result))
    }

    pub(crate) fn populate_component_definition(
        &self,
        component: &mut VueComponent,
    ) -> IndexResult<()> {
        if let Some(inline) = &component.inline_definition {
            let definition = self.definition_with_live_template(inline)?;
            apply_definition(component, &definition);
            return Ok(());
        }
        let mut definition = None;
        if !component.definition_path.is_empty() {
            definition = self.get_component_definition(&component.definition_path)?;
        }
        if definition.is_none() {
            let definitions = self.definition_index.get_values_view(&component.name)?;
            let found = definitions
                .iter()
                .find(|definition| definition.file_path == component.file_path)
                .or_else(|| (definitions.len() == 1).then(|| &definitions[0]));
            if let Some(found) = found {
                definition = Some(self.definition_with_live_template(found)?);
            }
        }
        if let Some(definition) = definition {
            apply_definition(component, &definition);
        }
        Ok(())
    }

    /// The template path `populate_component_definition` would leave on
    /// `component`, without cloning the definition (template paths are not
    /// changed by live Twig templates or script-setup enrichment).
    pub(crate) fn registration_template_path<'a>(
        &self,
        component: &'a VueComponent,
    ) -> IndexResult<std::borrow::Cow<'a, str>> {
        use std::borrow::Cow;
        if let Some(inline) = &component.inline_definition {
            return Ok(Cow::Owned(inline.template_path.clone()));
        }
        if !component.definition_path.is_empty() {
            let (live, shadowed) = self.live_legacy_template_path(&component.definition_path);
            if let Some(path) = live {
                return Ok(Cow::Owned(path));
            }
            if !shadowed {
                let normalized_path = normalize_definition_path(&component.definition_path);
                let definitions = self.definition_index.get_values_view(&normalized_path)?;
                if let Some(first) = definitions.first() {
                    return Ok(Cow::Owned(first.template_path.clone()));
                }
            }
        }
        let definitions = self.definition_index.get_values_view(&component.name)?;
        let found = definitions
            .iter()
            .find(|definition| definition.file_path == component.file_path)
            .or_else(|| (definitions.len() == 1).then(|| &definitions[0]));
        Ok(match found {
            Some(found) => Cow::Owned(found.template_path.clone()),
            None => Cow::Borrowed(component.template_path.as_str()),
        })
    }

    pub(crate) fn component_with_mixins(
        &self,
        component: &VueComponent,
    ) -> IndexResult<VueComponent> {
        let mut result = VueComponent {
            name: component.name.clone(),
            ..Default::default()
        };
        let mut seen = HashSet::new();
        for mixin in &component.mixins {
            self.apply_mixin(&component.name, mixin, &mut seen, &mut result)?;
        }
        Ok(overlay_components(&result, component))
    }

    fn apply_mixin(
        &self,
        component_name: &str,
        name: &str,
        seen: &mut HashSet<String>,
        result: &mut VueComponent,
    ) -> IndexResult<()> {
        if name.is_empty() || !seen.insert(name.to_owned()) {
            return Ok(());
        }
        let mut mixins = self.get_mixin(name)?;
        if mixins.is_empty() {
            result.open_runtime_members = true;
        }
        mixins.sort_by(|left, right| left.file_path.cmp(&right.file_path));
        for mixin in &mixins {
            for parent in &mixin.definition.mixins {
                self.apply_mixin(component_name, parent, seen, result)?;
            }
            let mut mixin_component = VueComponent {
                name: component_name.to_owned(),
                ..Default::default()
            };
            apply_definition(&mut mixin_component, &mixin.definition);
            *result = overlay_components(result, &mixin_component);
        }
        Ok(())
    }

    /// Saves a component definition (primarily for testing).
    pub fn save_component_definition(
        &self,
        key: &str,
        definition: &ComponentDefinition,
    ) -> IndexResult<()> {
        self.invalidate_template_component_cache();
        self.definition_index
            .batch_save_items([(definition.file_path.as_str(), [(key, definition)])])
    }

    /// Saves a component (primarily for testing).
    pub fn save_component(&self, component: &VueComponent) -> IndexResult<()> {
        self.invalidate_template_component_cache();
        self.component_index.batch_save_items([(
            component.file_path.as_str(),
            [(component.name.as_str(), component)],
        )])
    }
}

pub(crate) fn apply_definition(component: &mut VueComponent, definition: &ComponentDefinition) {
    if !definition.deprecated.is_empty() {
        component.deprecated = definition.deprecated.clone();
    }
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
    component.open_runtime_members = definition.open_runtime_members;
    component.assignments = definition.assignments.clone();
    component.slots = definition.slots.clone();
    component.blocks = definition.blocks.clone();
    component.template_path = definition.template_path.clone();
}

/// Merges entries with the same name into one, preferring the most complete.
#[allow(dead_code)]
pub(crate) fn deduplicate_components(components: Vec<VueComponent>) -> Vec<VueComponent> {
    if components.len() <= 1 {
        return components;
    }
    let mut iter = components.into_iter();
    let mut best = iter.next().expect("at least two components");
    for component in iter {
        best = if component.props.len() > best.props.len() {
            merge_components(&best, &component)
        } else if component.props.len() < best.props.len() {
            merge_components(&component, &best)
        } else if !component.definition_path.is_empty() && best.definition_path.is_empty() {
            merge_components(&best, &component)
        } else {
            merge_components(&component, &best)
        };
    }
    vec![best]
}

/// Takes data from `preferred` and falls back to `fallback` for missing data.
#[allow(dead_code)]
pub(crate) fn merge_components(fallback: &VueComponent, preferred: &VueComponent) -> VueComponent {
    fn fill(target: &mut String, fallback: &str) {
        if target.is_empty() {
            *target = fallback.to_owned();
        }
    }
    fn fill_vec<T: Clone>(target: &mut Vec<T>, fallback: &[T]) {
        if target.is_empty() {
            *target = fallback.to_vec();
        }
    }
    let mut result = preferred.clone();
    result.open_runtime_members = preferred.open_runtime_members || fallback.open_runtime_members;
    fill(&mut result.deprecated, &fallback.deprecated);
    fill(&mut result.extends_component, &fallback.extends_component);
    fill(&mut result.import_path, &fallback.import_path);
    fill(&mut result.definition_path, &fallback.definition_path);
    fill_vec(&mut result.props, &fallback.props);
    fill(&mut result.model_prop, &fallback.model_prop);
    fill(&mut result.model_event, &fallback.model_event);
    fill_vec(&mut result.emits, &fallback.emits);
    fill_vec(&mut result.events, &fallback.events);
    fill_vec(&mut result.methods, &fallback.methods);
    fill_vec(&mut result.computed, &fallback.computed);
    fill_vec(&mut result.data, &fallback.data);
    fill_vec(&mut result.injected, &fallback.injected);
    fill_vec(&mut result.mixins, &fallback.mixins);
    fill_vec(&mut result.local_components, &fallback.local_components);
    fill_vec(&mut result.local_directives, &fallback.local_directives);
    fill_vec(&mut result.members, &fallback.members);
    fill_vec(&mut result.assignments, &fallback.assignments);
    fill_vec(&mut result.slots, &fallback.slots);
    fill_vec(&mut result.blocks, &fallback.blocks);
    fill(&mut result.template_path, &fallback.template_path);
    result
}

pub(crate) fn overlay_components(base: &VueComponent, overlay: &VueComponent) -> VueComponent {
    fn set(target: &mut String, value: &str) {
        if !value.is_empty() {
            *target = value.to_owned();
        }
    }
    let mut result = base.clone();
    result.open_runtime_members = result.open_runtime_members || overlay.open_runtime_members;
    set(&mut result.name, &overlay.name);
    set(&mut result.deprecated, &overlay.deprecated);
    if !overlay.kind.is_unset() {
        result.kind = overlay.kind;
    }
    set(&mut result.target_component, &overlay.target_component);
    set(&mut result.extends_component, &overlay.extends_component);
    set(&mut result.import_path, &overlay.import_path);
    set(&mut result.file_path, &overlay.file_path);
    set(&mut result.definition_path, &overlay.definition_path);
    if overlay.line != 0 {
        result.line = overlay.line;
    }
    set(&mut result.template_path, &overlay.template_path);
    set(&mut result.model_prop, &overlay.model_prop);
    set(&mut result.model_event, &overlay.model_event);
    // An empty overlay list leaves the base list unchanged; skip rebuilding it.
    macro_rules! overlay_field {
        ($field:ident, $overlay:path) => {
            if !overlay.$field.is_empty() {
                result.$field = $overlay(&result.$field, &overlay.$field);
            }
        };
    }
    overlay_field!(props, overlay_props);
    overlay_field!(emits, overlay_names);
    overlay_field!(events, overlay_events);
    overlay_field!(methods, overlay_names);
    overlay_field!(computed, overlay_names);
    overlay_field!(data, overlay_names);
    overlay_field!(injected, overlay_names);
    overlay_field!(mixins, overlay_names);
    overlay_field!(local_components, overlay_local_components);
    overlay_field!(local_directives, overlay_local_directives);
    overlay_field!(members, overlay_members);
    result
        .assignments
        .extend(overlay.assignments.iter().cloned());
    overlay_field!(slots, overlay_slots);
    overlay_field!(blocks, overlay_blocks);
    result
}

/// Replaces values with equal keys in place and appends new ones.
fn overlay_by_key<T: Clone>(
    base: &[T],
    overlay: &[T],
    key: impl Fn(&T) -> String,
    skip: impl Fn(&str) -> bool,
    mut merge: impl FnMut(&T, T) -> T,
) -> Vec<T> {
    let mut result = base.to_vec();
    if overlay.is_empty() {
        return result;
    }
    let mut positions: HashMap<String, usize> = HashMap::with_capacity(result.len());
    for (index, value) in result.iter().enumerate() {
        positions.insert(key(value), index);
    }
    for value in overlay {
        let value_key = key(value);
        if skip(&value_key) {
            continue;
        }
        if let Some(&index) = positions.get(&value_key) {
            result[index] = merge(&result[index], value.clone());
        } else {
            positions.insert(value_key, result.len());
            result.push(value.clone());
        }
    }
    result
}

pub(crate) fn overlay_props(
    base: &[VueComponentProp],
    overlay: &[VueComponentProp],
) -> Vec<VueComponentProp> {
    overlay_by_key(
        base,
        overlay,
        |prop| prop.name.clone(),
        |_| false,
        |_, prop| prop,
    )
}

pub(crate) fn overlay_names(base: &[String], overlay: &[String]) -> Vec<String> {
    let mut result = base.to_vec();
    if overlay.is_empty() {
        return result;
    }
    let mut seen: HashSet<String> = result.iter().cloned().collect();
    for name in overlay {
        if !name.is_empty() && seen.insert(name.clone()) {
            result.push(name.clone());
        }
    }
    result
}

pub(crate) fn overlay_local_components(
    base: &[VueLocalComponent],
    overlay: &[VueLocalComponent],
) -> Vec<VueLocalComponent> {
    overlay_by_key(
        base,
        overlay,
        |component| component.name.to_lowercase(),
        str::is_empty,
        |_, component| component,
    )
}

pub(crate) fn overlay_local_directives(
    base: &[VueLocalDirective],
    overlay: &[VueLocalDirective],
) -> Vec<VueLocalDirective> {
    overlay_by_key(
        base,
        overlay,
        |directive| directive.name.to_lowercase(),
        str::is_empty,
        |_, directive| directive,
    )
}

pub(crate) fn overlay_members(
    base: &[VueComponentMember],
    overlay: &[VueComponentMember],
) -> Vec<VueComponentMember> {
    overlay_by_key(
        base,
        overlay,
        |member| format!("{}\x00{}", member.kind.as_str(), member.name),
        |_| false,
        |current, mut member| {
            if member.deprecated.is_empty() {
                member.deprecated = current.deprecated.clone();
            }
            member
        },
    )
}

pub(crate) fn overlay_slots(
    base: &[VueComponentSlot],
    overlay: &[VueComponentSlot],
) -> Vec<VueComponentSlot> {
    overlay_by_key(
        base,
        overlay,
        VueComponentSlot::identity_key,
        |_| false,
        |current, mut slot| {
            if slot.file_path.is_empty() {
                slot.file_path = current.file_path.clone();
            }
            if slot.line == 0 {
                slot.line = current.line;
            }
            if slot.name_range == AdminSourceRange::default() && slot.file_path == current.file_path
            {
                slot.name_range = current.name_range.clone();
            }
            slot
        },
    )
}

pub(crate) fn overlay_events(
    base: &[VueComponentEvent],
    overlay: &[VueComponentEvent],
) -> Vec<VueComponentEvent> {
    overlay_by_key(
        base,
        overlay,
        |event| canonical_event_name(&event.name),
        str::is_empty,
        |current, mut event| {
            if event.documentation.is_empty() {
                event.documentation = current.documentation.clone();
            }
            if event.type_.is_empty() {
                event.type_ = current.type_.clone();
            }
            if event.file_path.is_empty() {
                event.file_path = current.file_path.clone();
            }
            if event.line == 0 {
                event.line = current.line;
            }
            if event.name_range == AdminSourceRange::default() {
                event.name_range = current.name_range.clone();
            }
            event
        },
    )
}

pub(crate) fn overlay_blocks(base: &[TwigBlock], overlay: &[TwigBlock]) -> Vec<TwigBlock> {
    overlay_by_key(
        base,
        overlay,
        |block| block.name.clone(),
        |_| false,
        |current, mut block| {
            if block.deprecated.is_empty() {
                block.deprecated = current.deprecated.clone();
            }
            block.scope_members =
                overlay_block_scope_members(&current.scope_members, &block.scope_members);
            block
        },
    )
}

pub(crate) fn overlay_block_scope_members(
    base: &[TwigBlockScopeMember],
    overlay: &[TwigBlockScopeMember],
) -> Vec<TwigBlockScopeMember> {
    overlay_by_key(
        base,
        overlay,
        |member| member.name.clone(),
        str::is_empty,
        |_, member| member,
    )
}
