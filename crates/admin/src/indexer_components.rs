//! Component, mixin and module catalog queries (port of Go
//! `indexer_components.go`).

use std::sync::Arc;

use crate::*;

fn sort_registrations(components: &mut [VueComponent], by_line: bool) {
    components.sort_by(|left, right| {
        let left_override = left.kind == ComponentRegistrationKind::Override;
        let right_override = right.kind == ComponentRegistrationKind::Override;
        left_override
            .cmp(&right_override)
            .then_with(|| left.file_path.cmp(&right.file_path))
            .then_with(|| {
                if by_line {
                    left.line.cmp(&right.line)
                } else {
                    std::cmp::Ordering::Equal
                }
            })
    });
}

impl AdminComponentIndexer {
    pub fn get_all_mixins(&self) -> IndexResult<Vec<AdminMixin>> {
        let values = self.mixin_index.get_all_values()?;
        Ok(overlay_live_legacy_values(
            values,
            &self.live_legacy_document_snapshots(),
            |value: &AdminMixin| value.file_path.as_str(),
            |document| document.mixins.as_slice(),
            None,
        ))
    }

    pub fn get_mixin(&self, name: &str) -> IndexResult<Vec<AdminMixin>> {
        let values = self.mixin_index.get_values(name)?;
        Ok(overlay_live_legacy_values(
            values,
            &self.live_legacy_document_snapshots(),
            |value: &AdminMixin| value.file_path.as_str(),
            |document| document.mixins.as_slice(),
            Some(&|value: &AdminMixin| value.name == name),
        ))
    }

    pub fn get_all_modules(&self) -> IndexResult<Vec<AdminModule>> {
        let values = self.module_index.get_all_values()?;
        Ok(overlay_live_legacy_values(
            values,
            &self.live_legacy_document_snapshots(),
            |value: &AdminModule| value.file_path.as_str(),
            |document| document.modules.as_slice(),
            None,
        ))
    }

    pub fn get_module(&self, name: &str) -> IndexResult<Vec<AdminModule>> {
        let values = self.module_index.get_values(name)?;
        Ok(overlay_live_legacy_values(
            values,
            &self.live_legacy_document_snapshots(),
            |value: &AdminModule| value.file_path.as_str(),
            |document| document.modules.as_slice(),
            Some(&|value: &AdminModule| value.name == name),
        ))
    }

    pub fn get_all_module_routes(&self) -> IndexResult<Vec<AdminModuleRoute>> {
        Ok(self
            .get_all_modules()?
            .into_iter()
            .flat_map(|module| module.routes)
            .collect())
    }

    pub fn get_module_route(
        &self,
        name: &str,
    ) -> IndexResult<Option<(AdminModule, AdminModuleRoute)>> {
        for module in self.get_all_modules()? {
            if let Some(route) = module.routes.iter().find(|route| route.name == name) {
                let route = route.clone();
                return Ok(Some((module, route)));
            }
        }
        Ok(None)
    }

    /// All registered components (a copy of [`Self::get_all_components_view`]).
    pub fn get_all_components(&self) -> IndexResult<Vec<VueComponent>> {
        Ok(self.get_all_components_view()?.to_vec())
    }

    /// The immutable component catalog, shared with the repository cache
    /// unless open documents overlay it.
    pub fn get_all_components_view(&self) -> IndexResult<Arc<[VueComponent]>> {
        let components = self.component_index.get_all_values_view()?;
        Ok(self.registrations_with_live_documents(components, ""))
    }

    /// The component that uses the given template path.
    pub fn get_component_by_template_path(
        &self,
        template_path: &str,
    ) -> IndexResult<Option<VueComponent>> {
        let normalized_path = normalize_definition_path(template_path);
        if normalized_path.is_empty() {
            return Ok(None);
        }
        let epoch = read_lock(&self.effective_cache).epoch;
        if read_lock(&self.template_cache)
            .misses
            .contains(&normalized_path)
        {
            return Ok(None);
        }
        let result = self.resolve_component_by_template_path(&normalized_path)?;
        if result.is_none() {
            let mut cache = write_lock(&self.template_cache);
            if read_lock(&self.effective_cache).epoch == epoch {
                cache.misses.insert(normalized_path);
            }
        }
        Ok(result)
    }

    fn resolve_component_by_template_path(
        &self,
        normalized_path: &str,
    ) -> IndexResult<Option<VueComponent>> {
        let normalized_path = normalized_path.to_owned();
        let resolve_cached = || -> IndexResult<Option<Option<VueComponent>>> {
            let name = self.cached_template_component(&normalized_path);
            if name.is_empty() {
                return Ok(None);
            }
            let Some(component) = self.get_effective_component(&name)? else {
                return Ok(Some(None));
            };
            if normalize_definition_path(&component.template_path) == normalized_path {
                return Ok(Some(Some(component)));
            }
            Ok(None)
        };
        if let Some(component) = resolve_cached()? {
            return Ok(component);
        }
        let all_components = self.get_all_components_view()?;
        self.ensure_template_component_catalog(&all_components)?;
        if let Some(component) = resolve_cached()? {
            return Ok(component);
        }

        for component in all_components.iter() {
            if !component.template_path.is_empty()
                && normalize_definition_path(&component.template_path) == normalized_path
            {
                if let Ok(mut full) = self.get_component_with_definition(&component.name)
                    && !full.is_empty()
                {
                    let first = full.swap_remove(0);
                    self.cache_template_component(&normalized_path, &first.name);
                    return Ok(Some(first));
                }
                self.cache_template_component(&normalized_path, &component.name);
                return Ok(Some(component.clone()));
            }
        }

        // Wrapped TypeScript definitions can acquire their template only after
        // the effective component is assembled.
        let mut names = self.get_all_component_names()?;
        names.sort();
        for name in names {
            // Inspect the shared cached component; clone only the match.
            let Some(component) = self.effective_component(&name, &mut HashSet::new())? else {
                continue;
            };
            if normalize_definition_path(&component.template_path) != normalized_path {
                continue;
            }
            self.cache_template_component(&normalized_path, &component.name);
            return Ok(Some(Arc::unwrap_or_clone(component)));
        }
        Ok(None)
    }

    /// The source registration owning `template_path`, without sibling
    /// overrides folded into it.
    pub fn get_component_registration_by_template_path(
        &self,
        template_path: &str,
    ) -> IndexResult<Option<VueComponent>> {
        let normalized_path = normalize_definition_path(template_path);
        if normalized_path.is_empty() {
            return Ok(None);
        }
        let components = self.get_all_components_view()?;
        let mut matches = Vec::new();
        for component in components.iter() {
            // Compare the resolved template path first; only matches pay for
            // the full definition clone.
            let template_path = self.registration_template_path(component)?;
            if normalize_definition_path(&template_path) != normalized_path {
                continue;
            }
            matches.push(self.get_component_registration_with_definition(component)?);
        }
        matches.sort_by(|left, right| {
            left.file_path
                .cmp(&right.file_path)
                .then(left.line.cmp(&right.line))
                .then_with(|| left.name.cmp(&right.name))
        });
        Ok(matches.into_iter().next())
    }

    /// The effective contract immediately before the registration owning
    /// `template_path`: the named parent for extend, or the base registration
    /// plus preceding overrides for override.
    pub fn get_parent_component_for_template(
        &self,
        template_path: &str,
    ) -> IndexResult<Option<VueComponent>> {
        let Some(owner) = self.get_component_registration_by_template_path(template_path)? else {
            return Ok(None);
        };
        if owner.kind == ComponentRegistrationKind::Extend || !owner.extends_component.is_empty() {
            let parent_name = if owner.extends_component.is_empty() {
                &owner.target_component
            } else {
                &owner.extends_component
            };
            return self.get_effective_component(parent_name);
        }
        if owner.kind != ComponentRegistrationKind::Override {
            return Ok(None);
        }
        let mut components = self.get_component(&owner.name)?;
        for component in &mut components {
            self.populate_component_definition(component)?;
        }
        sort_registrations(&mut components, true);

        let mut result = VueComponent {
            name: owner.name.clone(),
            ..Default::default()
        };
        let mut found_predecessor = false;
        for component in components {
            if same_component_registration(&component, &owner) {
                break;
            }
            let mut own = self.component_with_mixins(&component)?;
            if !own.extends_component.is_empty()
                && let Some(parent) = self.get_effective_component(&own.extends_component)?
            {
                own = overlay_components(&parent, &own);
            }
            result = overlay_components(&result, &own);
            found_predecessor = true;
        }
        if !found_predecessor {
            return Ok(None);
        }
        Ok(Some(result))
    }

    /// Resolves a global component or an Options API component local to the
    /// owner of `template_path` (`owner` skips the owner lookup).
    pub fn get_component_for_template_tag(
        &self,
        template_path: &str,
        name: &str,
        owner: Option<&VueComponent>,
    ) -> IndexResult<Option<VueComponent>> {
        if !template_path.is_empty() {
            let looked_up;
            let owner = match owner {
                Some(owner) => Some(owner),
                None => {
                    looked_up = self.get_component_by_template_path(template_path)?;
                    looked_up.as_ref()
                }
            };
            if let Some(owner) = owner {
                let local = owner
                    .local_component(name)
                    .or_else(|| owner.local_component(&camel_to_kebab(name)));
                if let Some(local) = local {
                    return self.resolve_local_component(local).map(Some);
                }
            }
        }
        let mut components = self.get_component_with_definition(name)?;
        if components.is_empty() {
            return Ok(None);
        }
        let first = components.swap_remove(0);
        self.component_with_live_vue_document(first).map(Some)
    }

    /// [`Self::get_component_for_template_tag`] without copying the cached
    /// effective component when neither a local registration nor an open Vue
    /// document changes it. Prefer this in per-tag loops such as completion.
    pub fn get_component_for_template_tag_shared(
        &self,
        template_path: &str,
        name: &str,
        owner: Option<&VueComponent>,
    ) -> IndexResult<Option<Arc<VueComponent>>> {
        if !template_path.is_empty() {
            let looked_up;
            let owner = match owner {
                Some(owner) => Some(owner),
                None => {
                    looked_up = self.get_component_by_template_path(template_path)?;
                    looked_up.as_ref()
                }
            };
            if let Some(owner) = owner {
                let local = owner
                    .local_component(name)
                    .or_else(|| owner.local_component(&camel_to_kebab(name)));
                if let Some(local) = local {
                    return self
                        .resolve_local_component(local)
                        .map(|c| Some(Arc::new(c)));
                }
            }
        }
        let mut resolving = HashSet::new();
        let Some(component) = self.effective_component(name, &mut resolving)? else {
            return Ok(None);
        };
        self.shared_component_with_live_vue_document(component)
            .map(Some)
    }

    fn resolve_local_component(&self, local: &VueLocalComponent) -> IndexResult<VueComponent> {
        for target_name in local_component_target_names(local) {
            let Some(target) = self.get_effective_component(&target_name)? else {
                continue;
            };
            let mut resolved = self.component_with_live_vue_document(target)?;
            resolved.name = local.name.clone();
            resolved.file_path = local.file_path.clone();
            resolved.definition_path = local.file_path.clone();
            resolved.line = local.line;
            return Ok(resolved);
        }
        for definition_path in local_component_definition_candidates(local) {
            if let Some(mut live) = self.live_vue_component(&definition_path)? {
                live.name = local.name.clone();
                live.file_path = local.file_path.clone();
                live.definition_path = definition_path;
                live.line = local.line;
                return Ok(live);
            }
            let Some(definition) = self.get_component_definition(&definition_path)? else {
                continue;
            };
            let mut resolved = VueComponent {
                name: local.name.clone(),
                file_path: local.file_path.clone(),
                definition_path,
                line: local.line,
                ..Default::default()
            };
            apply_definition(&mut resolved, &definition);
            return Ok(resolved);
        }
        Ok(VueComponent {
            name: local.name.clone(),
            file_path: local.file_path.clone(),
            definition_path: local.file_path.clone(),
            line: local.line,
            ..Default::default()
        })
    }

    /// The complete finite component contract of a dynamic selector; `None`
    /// unless every candidate is a registered global or local component.
    pub fn resolve_dynamic_components(
        &self,
        template_path: &str,
        selector: &VueDynamicComponentSelector,
        owner: Option<&VueComponent>,
    ) -> IndexResult<Option<Vec<VueComponent>>> {
        if !selector.complete {
            return Ok(None);
        }
        let names = selector.names();
        if names.is_empty() {
            return Ok(None);
        }
        let mut result = Vec::with_capacity(names.len());
        for name in &names {
            if !is_component_tag(name) {
                return Ok(None);
            }
            let Some(component) =
                self.get_component_for_template_tag(template_path, name, owner)?
            else {
                return Ok(None);
            };
            result.push(component);
        }
        Ok(Some(result))
    }

    /// The Options API component alias declared at a JavaScript/TypeScript
    /// LSP position, with its owning component.
    pub fn get_local_component_at_definition_position(
        &self,
        definition_path: &str,
        line: u32,
        character: u32,
    ) -> IndexResult<Option<(VueComponent, VueLocalComponent)>> {
        for component in self.get_components_by_definition_path(definition_path)? {
            if let Some(local) = component.local_components.iter().find(|local| {
                admin_source_range_contains_position(&local.name_range, line, character)
            }) {
                let local = local.clone();
                return Ok(Some((component, local)));
            }
        }
        Ok(None)
    }

    pub fn get_local_directive_at_definition_position(
        &self,
        definition_path: &str,
        line: u32,
        character: u32,
    ) -> IndexResult<Option<(VueComponent, VueLocalDirective)>> {
        for component in self.get_components_by_definition_path(definition_path)? {
            if let Some(local) = component.local_directives.iter().find(|local| {
                admin_source_range_contains_position(&local.name_range, line, character)
            }) {
                let local = local.clone();
                return Ok(Some((component, local)));
            }
        }
        Ok(None)
    }

    pub(crate) fn cached_template_component(&self, path: &str) -> String {
        read_lock(&self.template_cache)
            .components
            .get(path)
            .cloned()
            .unwrap_or_default()
    }

    pub(crate) fn cache_template_component(&self, path: &str, name: &str) {
        if path.is_empty() || name.is_empty() {
            return;
        }
        write_lock(&self.template_cache)
            .components
            .insert(path.to_owned(), name.to_owned());
    }

    /// Projects the component and definition repositories into a
    /// template-path lookup once per index generation.
    pub(crate) fn ensure_template_component_catalog(
        &self,
        components: &[VueComponent],
    ) -> IndexResult<()> {
        let mut cache = write_lock(&self.template_cache);
        if cache.catalog_built {
            return Ok(());
        }
        let definitions = self.definition_index.get_all_values_view()?;
        let mut templates_by_definition: HashMap<String, String> =
            HashMap::with_capacity(definitions.len());
        for definition in definitions.iter() {
            let definition_path = normalize_definition_path(&definition.file_path);
            let template_path = normalize_definition_path(&definition.template_path);
            if definition_path.is_empty() || template_path.is_empty() {
                continue;
            }
            templates_by_definition
                .entry(definition_path)
                .or_insert(template_path);
        }
        let mut add = |template_path: &str, name: &str| {
            let template_path = normalize_definition_path(template_path);
            if template_path.is_empty() || name.is_empty() {
                return;
            }
            cache
                .components
                .entry(template_path)
                .or_insert_with(|| name.to_owned());
        };
        for component in components {
            add(&component.template_path, &component.name);
            let definition_path = normalize_definition_path(&component.definition_path);
            if let Some(template) = templates_by_definition.get(&definition_path) {
                add(template, &component.name);
            }
            if let Some(inline) = &component.inline_definition {
                add(&inline.template_path, &component.name);
            }
        }
        cache.catalog_built = true;
        Ok(())
    }

    pub(crate) fn invalidate_template_component_cache(&self) {
        {
            let mut cache = write_lock(&self.template_cache);
            cache.components = HashMap::new();
            cache.catalog_built = false;
            cache.misses = HashSet::new();
        }
        let mut effective = write_lock(&self.effective_cache);
        effective.components = HashMap::new();
        effective.epoch += 1;
    }
}

pub(crate) fn read_lock<T>(lock: &std::sync::RwLock<T>) -> std::sync::RwLockReadGuard<'_, T> {
    lock.read()
        .unwrap_or_else(std::sync::PoisonError::into_inner)
}

pub(crate) fn write_lock<T>(lock: &std::sync::RwLock<T>) -> std::sync::RwLockWriteGuard<'_, T> {
    lock.write()
        .unwrap_or_else(std::sync::PoisonError::into_inner)
}

pub(crate) fn same_component_registration(left: &VueComponent, right: &VueComponent) -> bool {
    left.name == right.name
        && left.kind == right.kind
        && left.line == right.line
        && normalize_definition_path(&left.file_path) == normalize_definition_path(&right.file_path)
        && normalize_definition_path(&left.template_path)
            == normalize_definition_path(&right.template_path)
}

pub(crate) fn admin_source_range_contains_position(
    range: &AdminSourceRange,
    line: u32,
    character: u32,
) -> bool {
    if line < range.start_line || line > range.end_line {
        return false;
    }
    if line == range.start_line && character < range.start_character {
        return false;
    }
    if line == range.end_line && character > range.end_character {
        return false;
    }
    true
}

pub(crate) fn local_component_target_names(local: &VueLocalComponent) -> Vec<String> {
    let mut result: Vec<String> = Vec::new();
    let mut append = |name: String| {
        let name = name.trim();
        if name.is_empty() || result.iter().any(|existing| existing == name) {
            return;
        }
        result.push(name.to_owned());
    };
    let symbol = local
        .symbol
        .strip_suffix("Original")
        .unwrap_or(&local.symbol);
    append(camel_to_kebab(symbol));
    if !local.import_path.is_empty() && local.import_path != METEOR_PACKAGE_PATH {
        let base = filepath::base(
            local
                .import_path
                .strip_suffix('/')
                .unwrap_or(&local.import_path),
        );
        let ext = filepath::ext(&base);
        let base = base.strip_suffix(ext).unwrap_or(&base);
        if base != "index" {
            append(camel_to_kebab(base));
        }
    }
    result
}

pub(crate) fn local_component_definition_candidates(local: &VueLocalComponent) -> Vec<String> {
    let mut result: Vec<String> = Vec::new();
    let mut append = |path: &str| {
        let path = filepath::clean(path);
        if path == "." || path.is_empty() {
            return;
        }
        let normalized = normalize_definition_path(&path);
        if result
            .iter()
            .any(|current| normalize_definition_path(current) == normalized)
        {
            return;
        }
        result.push(path);
    };
    append(&resolve_import_path(&local.file_path, &local.import_path));
    for candidate in admin_type_import_candidates(&local.file_path, &local.import_path) {
        append(&candidate);
    }
    result
}
