//! Symbol usage queries over the persisted usage index.

use crate::*;

fn is_twig_path(path: &str) -> bool {
    filepath::ext(path).eq_ignore_ascii_case(".twig")
}

fn is_component_contract_kind(kind: AdminSymbolKind) -> bool {
    matches!(
        kind,
        AdminSymbolKind::ComponentProp
            | AdminSymbolKind::ComponentEvent
            | AdminSymbolKind::ComponentSlot
    )
}

#[derive(Clone, Debug, PartialEq, Eq, Hash)]
struct DynamicUsageResolutionKey {
    file_path: String,
    selector: String,
    router_view: bool,
}

#[derive(Clone, Debug, Default)]
struct DynamicUsageResolution {
    components: Vec<VueComponent>,
    complete: bool,
}

impl AdminComponentIndexer {
    pub fn get_usages(
        &self,
        kind: AdminSymbolKind,
        owner: &str,
        name: &str,
    ) -> IndexResult<Vec<AdminUsageSet>> {
        self.usage_index
            .get_values(&admin_usage_key(kind, owner, name))
    }

    /// Expands source-owned component props, events and slots through every
    /// effective component exposing the same declaration. Other kinds keep
    /// their direct persisted identity.
    pub fn get_symbol_usages(&self, target: &AdminSymbolTarget) -> IndexResult<Vec<AdminUsageSet>> {
        let sets = self.get_usages(target.kind, &target.owner, &target.name)?;
        if target.kind == AdminSymbolKind::Directive {
            return self.directive_symbol_usages(target, sets);
        }
        if target.kind == AdminSymbolKind::ComponentMember {
            return self.component_member_symbol_usages(target, sets);
        }
        if !is_component_contract_kind(target.kind) {
            return Ok(sets);
        }
        let sets = self.exposed_component_symbol_usages(target, sets)?;
        let mut sets = self.local_component_symbol_usages(target, sets)?;
        sets.extend(self.dynamic_component_symbol_usages(target)?);
        Ok(unique_admin_usage_sets(sets))
    }

    pub(crate) fn exposed_component_symbol_usages(
        &self,
        target: &AdminSymbolTarget,
        mut sets: Vec<AdminUsageSet>,
    ) -> IndexResult<Vec<AdminUsageSet>> {
        for component in self.get_components_exposing_symbol(target)? {
            for set in self.get_usages(target.kind, &component.name, &target.name)? {
                if self.component_usage_set_matches_source(&set, &component.name, target)? {
                    sets.push(set);
                }
            }
            for set in self.component_model_usage_sets(&component, &component.name, target)? {
                if self.component_model_usage_set_matches_source(&set, &component.name, target)? {
                    sets.push(set);
                }
            }
        }
        Ok(sets)
    }

    pub(crate) fn local_component_symbol_usages(
        &self,
        target: &AdminSymbolTarget,
        mut sets: Vec<AdminUsageSet>,
    ) -> IndexResult<Vec<AdminUsageSet>> {
        for name in self.get_all_component_names()? {
            let Some(owner) = self.get_effective_component(&name)? else {
                continue;
            };
            if owner.template_path.is_empty() {
                continue;
            }
            let owner_template = normalize_definition_path(&owner.template_path);
            for local in &owner.local_components {
                let Some(component) =
                    self.get_component_for_template_tag(&owner.template_path, &local.name, None)?
                else {
                    continue;
                };
                let Some(source) = component.symbol_source(target.kind, &target.name) else {
                    continue;
                };
                if filepath::clean(&source) != filepath::clean(&target.owner) {
                    continue;
                }
                for set in self.get_usages(target.kind, &local.name, &target.name)? {
                    if normalize_definition_path(&set.file_path) == owner_template {
                        sets.push(set);
                    }
                }
                for set in self.component_model_usage_sets(&component, &local.name, target)? {
                    if normalize_definition_path(&set.file_path) == owner_template {
                        sets.push(set);
                    }
                }
            }
        }
        Ok(sets)
    }

    /// Resolves symbolically persisted dynamic component usages against the
    /// latest effective component graph at query time.
    pub(crate) fn dynamic_component_symbol_usages(
        &self,
        target: &AdminSymbolTarget,
    ) -> IndexResult<Vec<AdminUsageSet>> {
        if !is_component_contract_kind(target.kind) {
            return Ok(Vec::new());
        }
        let mut raw = self.get_usages(
            target.kind,
            ADMIN_DYNAMIC_COMPONENT_USAGE_OWNER,
            &target.name,
        )?;
        if matches!(
            target.kind,
            AdminSymbolKind::ComponentProp | AdminSymbolKind::ComponentEvent
        ) {
            let prefix = admin_usage_key(
                AdminSymbolKind::ComponentModel,
                ADMIN_DYNAMIC_COMPONENT_USAGE_OWNER,
                "",
            );
            for key in self.usage_index.get_all_keys()? {
                let Some(name) = key.strip_prefix(&prefix) else {
                    continue;
                };
                raw.extend(self.get_usages(
                    AdminSymbolKind::ComponentModel,
                    ADMIN_DYNAMIC_COMPONENT_USAGE_OWNER,
                    name,
                )?);
            }
        }
        let mut cache: HashMap<DynamicUsageResolutionKey, DynamicUsageResolution> = HashMap::new();
        let mut result = Vec::new();
        for set in raw {
            let mut filtered = set.clone();
            filtered.occurrences = Vec::new();
            for occurrence in &set.occurrences {
                let key = DynamicUsageResolutionKey {
                    file_path: normalize_definition_path(&set.file_path),
                    selector: occurrence.dynamic_component_selector.clone(),
                    router_view: occurrence.dynamic_router_view,
                };
                if !cache.contains_key(&key) {
                    let (components, complete) = self
                        .resolve_persisted_dynamic_usage_components(&set.file_path, occurrence)?;
                    cache.insert(
                        key.clone(),
                        DynamicUsageResolution {
                            components,
                            complete,
                        },
                    );
                }
                let resolution = &cache[&key];
                if !resolution.complete
                    || !dynamic_usage_components_match_target(&resolution.components, &set, target)
                {
                    continue;
                }
                filtered.occurrences.push(occurrence.clone());
            }
            if !filtered.occurrences.is_empty() {
                result.push(filtered);
            }
        }
        Ok(result)
    }

    pub(crate) fn resolve_persisted_dynamic_usage_components(
        &self,
        template_path: &str,
        occurrence: &AdminSourceRange,
    ) -> IndexResult<(Vec<VueComponent>, bool)> {
        let Some(owner) = self.get_component_by_template_path(template_path)? else {
            return Ok((Vec::new(), false));
        };
        let resolved = if occurrence.dynamic_router_view {
            self.resolve_router_view_route_component_names(&owner)?
        } else {
            self.resolve_dynamic_component_names(
                &owner,
                &occurrence.dynamic_component_selector,
                &mut HashSet::new(),
            )
        };
        if !resolved.found || !resolved.complete || resolved.names.is_empty() {
            return Ok((Vec::new(), false));
        }
        let selector = VueDynamicComponentSelector {
            complete: true,
            candidates: resolved
                .names
                .into_iter()
                .map(|name| VueDynamicComponentCandidate {
                    name,
                    ..Default::default()
                })
                .collect(),
            ..Default::default()
        };
        Ok(
            match self.resolve_dynamic_components(template_path, &selector, None)? {
                Some(components) => (components, true),
                None => (Vec::new(), false),
            },
        )
    }

    /// Whether rewriting a dynamically owned attribute keeps the same
    /// declaration identity for every possible component.
    pub fn dynamic_component_usage_rename_safe(
        &self,
        set: &AdminUsageSet,
        occurrence: &AdminSourceRange,
        target: &AdminSymbolTarget,
    ) -> IndexResult<bool> {
        if occurrence.dynamic_component_selector.is_empty() && !occurrence.dynamic_router_view {
            return Ok(true);
        }
        let (components, complete) =
            self.resolve_persisted_dynamic_usage_components(&set.file_path, occurrence)?;
        if !complete || components.is_empty() {
            return Ok(false);
        }
        if set.kind != target.kind || set.name != target.name {
            return Ok(false);
        }
        let owner = normalize_definition_path(&target.owner);
        Ok(components.iter().all(|component| {
            component
                .symbol_source(set.kind, &set.name)
                .is_some_and(|source| normalize_definition_path(&source) == owner)
        }))
    }

    pub(crate) fn directive_symbol_usages(
        &self,
        target: &AdminSymbolTarget,
        direct: Vec<AdminUsageSet>,
    ) -> IndexResult<Vec<AdminUsageSet>> {
        let mut result = Vec::new();
        if !target.owner.is_empty() {
            // Source-owned sets contain the local declaration; template
            // references are persisted without an owner and scoped below.
            result.extend(direct);
        }
        for set in self.get_usages(AdminSymbolKind::Directive, "", &target.name)? {
            if !is_twig_path(&set.file_path) {
                if target.owner.is_empty() {
                    result.push(set);
                }
                continue;
            }
            let local = self.get_local_directive_for_template(&set.file_path, &target.name)?;
            if target.owner.is_empty() {
                if local.is_none() {
                    result.push(set);
                }
                continue;
            }
            if local.is_some_and(|local| {
                normalize_definition_path(&local.file_path)
                    == normalize_definition_path(&target.owner)
            }) {
                result.push(set);
            }
        }
        Ok(unique_admin_usage_sets(result))
    }

    pub(crate) fn component_model_usage_sets(
        &self,
        component: &VueComponent,
        usage_owner: &str,
        target: &AdminSymbolTarget,
    ) -> IndexResult<Vec<AdminUsageSet>> {
        if !matches!(
            target.kind,
            AdminSymbolKind::ComponentProp | AdminSymbolKind::ComponentEvent
        ) {
            return Ok(Vec::new());
        }
        let mut result = Vec::new();
        for binding in component.component_models() {
            if !component_model_binding_matches_target(component, &binding, target) {
                continue;
            }
            result.extend(self.get_usages(
                AdminSymbolKind::ComponentModel,
                usage_owner,
                &binding.attribute_name,
            )?);
        }
        Ok(result)
    }

    pub(crate) fn component_model_usage_set_matches_source(
        &self,
        set: &AdminUsageSet,
        component_name: &str,
        target: &AdminSymbolTarget,
    ) -> IndexResult<bool> {
        if !is_twig_path(&set.file_path) {
            return Ok(true);
        }
        let Some(owner) = self.get_component_by_template_path(&set.file_path)? else {
            return Ok(true);
        };
        if owner.local_component(component_name).is_none() {
            return Ok(true);
        }
        let Some(component) =
            self.get_component_for_template_tag(&set.file_path, component_name, None)?
        else {
            return Ok(false);
        };
        Ok(component.component_models().iter().any(|binding| {
            binding.attribute_name == set.name
                && component_model_binding_matches_target(&component, binding, target)
        }))
    }

    pub(crate) fn component_member_symbol_usages(
        &self,
        target: &AdminSymbolTarget,
        mut sets: Vec<AdminUsageSet>,
    ) -> IndexResult<Vec<AdminUsageSet>> {
        for set in self.get_usages(AdminSymbolKind::ComponentMember, "", &target.name)? {
            if self.component_member_usage_set_matches_source(&set, target)? {
                sets.push(set);
            }
        }
        Ok(unique_admin_usage_sets(sets))
    }

    pub(crate) fn component_member_usage_set_matches_source(
        &self,
        set: &AdminUsageSet,
        target: &AdminSymbolTarget,
    ) -> IndexResult<bool> {
        let mut identities: HashSet<String> = HashSet::new();
        let mut add = |component: &VueComponent| {
            let Some(member) = component.template_member(&target.name) else {
                return;
            };
            if !member.renameable() {
                return;
            }
            let identity = member.source_identity();
            if !identity.is_empty() {
                identities.insert(identity);
            }
        };
        if is_twig_path(&set.file_path) {
            if let Some(component) = self.get_component_by_template_path(&set.file_path)? {
                add(&component);
            }
        } else {
            for component in self.get_components_by_definition_path(&set.file_path)? {
                add(&component);
            }
        }
        Ok(identities.len() == 1 && identities.contains(&target.owner))
    }

    pub fn get_components_exposing_member(
        &self,
        target: &AdminSymbolTarget,
    ) -> IndexResult<Vec<VueComponent>> {
        if target.kind != AdminSymbolKind::ComponentMember
            || target.name.is_empty()
            || target.owner.is_empty()
        {
            return Ok(Vec::new());
        }
        let mut result = Vec::new();
        for name in self.get_all_component_names()? {
            let Some(component) = self.effective_component(&name, &mut HashSet::new())? else {
                continue;
            };
            let exposes = component
                .template_member(&target.name)
                .is_some_and(|member| {
                    member.renameable() && member.source_identity() == target.owner
                });
            if exposes {
                result.push(std::sync::Arc::unwrap_or_clone(component));
            }
        }
        Ok(result)
    }

    pub(crate) fn component_usage_set_matches_source(
        &self,
        set: &AdminUsageSet,
        component_name: &str,
        target: &AdminSymbolTarget,
    ) -> IndexResult<bool> {
        if !is_twig_path(&set.file_path) {
            return Ok(true);
        }
        let Some(owner) = self.get_component_by_template_path(&set.file_path)? else {
            return Ok(true);
        };
        if owner.local_component(component_name).is_none() {
            return Ok(true);
        }
        let Some(component) =
            self.get_component_for_template_tag(&set.file_path, component_name, None)?
        else {
            return Ok(false);
        };
        Ok(component
            .symbol_source(target.kind, &target.name)
            .is_some_and(|source| filepath::clean(&source) == filepath::clean(&target.owner)))
    }

    pub fn get_components_exposing_symbol(
        &self,
        target: &AdminSymbolTarget,
    ) -> IndexResult<Vec<VueComponent>> {
        if !is_component_contract_kind(target.kind) {
            return Ok(Vec::new());
        }
        let owner = filepath::clean(&target.owner);
        let mut result = Vec::new();
        for name in self.get_all_component_names()? {
            let Some(component) = self.effective_component(&name, &mut HashSet::new())? else {
                continue;
            };
            let exposes = component
                .symbol_source(target.kind, &target.name)
                .is_some_and(|source| filepath::clean(&source) == owner);
            if exposes {
                result.push(std::sync::Arc::unwrap_or_clone(component));
            }
        }
        Ok(result)
    }

    /// Whether a resolved concrete consumer name is owned by a computed slot
    /// family, which cannot be renamed safely.
    pub fn is_dynamic_component_slot(&self, target: &AdminSymbolTarget) -> IndexResult<bool> {
        if target.kind != AdminSymbolKind::ComponentSlot {
            return Ok(false);
        }
        Ok(self
            .get_components_exposing_symbol(target)?
            .iter()
            .any(|component| {
                component
                    .component_slot(&target.name)
                    .is_some_and(VueComponentSlot::is_dynamic_name)
            }))
    }

    /// The source declaration encoded by a component-member symbol identity.
    pub fn resolve_component_member_target(
        &self,
        target: &AdminSymbolTarget,
    ) -> IndexResult<Option<VueComponentMember>> {
        if target.kind != AdminSymbolKind::ComponentMember
            || target.owner.is_empty()
            || target.name.is_empty()
        {
            return Ok(None);
        }
        let Some(separator) = target.owner.find('\0').filter(|separator| *separator > 0) else {
            return Ok(None);
        };
        let members = self.component_members_declared_in(&target.owner[..separator])?;
        Ok(members
            .into_iter()
            .find(|member| member.source_identity() == target.owner && member.name == target.name))
    }

    /// Resolves a Pinia action declaration by its indexed source line
    /// (0-based `line`).
    pub fn store_action_targets_at_definition_position(
        &self,
        file_path: &str,
        name: &str,
        line: u32,
    ) -> IndexResult<Vec<AdminSymbolTarget>> {
        if file_path.is_empty() || name.is_empty() {
            return Ok(Vec::new());
        }
        let normalized = normalize_definition_path(file_path);
        let mut result = Vec::new();
        let mut seen = HashSet::new();
        for store in self.get_all_stores()? {
            let Some(member) = store.member(name) else {
                continue;
            };
            if member.kind != AdminStoreMemberKind::Action
                || normalize_definition_path(&member.file_path) != normalized
                || member.line != line + 1
            {
                continue;
            }
            let target = AdminSymbolTarget {
                kind: AdminSymbolKind::StoreMember,
                owner: store.name.clone(),
                name: member.name.clone(),
            };
            if seen.insert(target.clone()) {
                result.push(target);
            }
        }
        result.sort_by(|left, right| {
            left.owner
                .cmp(&right.owner)
                .then_with(|| left.name.cmp(&right.name))
        });
        Ok(result)
    }

    pub fn twig_component_member_at(
        &self,
        file_path: &str,
        root: &SyntaxNode,
        content: &str,
        offset: u32,
    ) -> IndexResult<Option<(AdminSymbolTarget, VueComponentMember)>> {
        let Some((name, range_value)) =
            twig_vue_expression_root_identifier_at_offset(root, content, offset)
        else {
            return Ok(None);
        };
        let probe = TwigVueMember {
            name: name.clone(),
            range: range_value,
            ..Default::default()
        };
        if twig_vue_root_identifier_is_local(root, content, &probe) {
            return Ok(None);
        }
        let Some(component) = self.get_component_by_template_path(file_path)? else {
            return Ok(None);
        };
        let Some(member) = component.template_member(&name) else {
            return Ok(None);
        };
        if !member.renameable() {
            return Ok(None);
        }
        Ok(Some((component_member_target(&member), member)))
    }

    /// The renameable member whose name range contains the 0-based UTF-16
    /// position.
    pub fn get_component_member_at_definition_position(
        &self,
        file_path: &str,
        line: u32,
        character: u32,
    ) -> IndexResult<Option<VueComponentMember>> {
        Ok(self
            .component_members_declared_in(file_path)?
            .into_iter()
            .find(|member| {
                member.renameable()
                    && admin_source_range_contains_position(&member.name_range, line, character)
            }))
    }

    pub(crate) fn component_members_declared_in(
        &self,
        file_path: &str,
    ) -> IndexResult<Vec<VueComponentMember>> {
        let normalized = normalize_definition_path(file_path);
        let mut seen = HashSet::new();
        let mut result = Vec::new();
        let mut add = |definition: &ComponentDefinition| {
            if normalize_definition_path(&definition.file_path) != normalized {
                return;
            }
            for member in &definition.members {
                let identity = member.source_identity();
                if identity.is_empty() || !seen.insert(identity) {
                    continue;
                }
                result.push(member.clone());
            }
        };
        for definition in self.definition_index.get_all_values_view()?.iter() {
            add(definition);
        }
        for mixin in self.get_all_mixins()? {
            add(&mixin.definition);
        }
        Ok(result)
    }

    /// Resolves a Twig symbol and its source-owned identity.
    pub fn twig_symbol_at(
        &self,
        file_path: &str,
        root: &SyntaxNode,
        offset: u32,
    ) -> IndexResult<Option<AdminSymbolTarget>> {
        let Some(target) = twig_symbol_at_offset(root, offset) else {
            return Ok(None);
        };
        if !is_component_contract_kind(target.kind) && target.kind != AdminSymbolKind::Directive {
            return Ok(Some(target));
        }
        if target.kind == AdminSymbolKind::Directive {
            if let Some(local) = self.get_local_directive_for_template(file_path, &target.name)? {
                return Ok(Some(AdminSymbolTarget {
                    kind: AdminSymbolKind::Directive,
                    owner: local.file_path,
                    name: local.name,
                }));
            }
            return Ok(Some(target));
        }
        let component = if !target.owner.is_empty() {
            self.get_component_for_template_tag(file_path, &target.owner, None)?
        } else if target.kind == AdminSymbolKind::ComponentSlot {
            self.get_component_by_template_path(file_path)?
        } else {
            None
        };
        let Some(component) = component else {
            return Ok(None);
        };
        let first = |values: [&str; 3]| {
            values
                .into_iter()
                .find(|value| !value.is_empty())
                .map(str::to_owned)
        };
        match target.kind {
            AdminSymbolKind::ComponentProp => {
                let Some(prop) = component.component_prop(&target.name) else {
                    return Ok(None);
                };
                let Some(owner) = first([
                    &prop.file_path,
                    &component.definition_path,
                    &component.file_path,
                ]) else {
                    return Ok(None);
                };
                Ok(Some(AdminSymbolTarget {
                    kind: AdminSymbolKind::ComponentProp,
                    owner,
                    name: prop.name.clone(),
                }))
            }
            AdminSymbolKind::ComponentEvent => {
                let Some(event) = component.component_event(&target.name) else {
                    return Ok(None);
                };
                let Some(owner) = first([
                    &event.file_path,
                    &component.definition_path,
                    &component.file_path,
                ]) else {
                    return Ok(None);
                };
                Ok(Some(AdminSymbolTarget {
                    kind: AdminSymbolKind::ComponentEvent,
                    owner,
                    name: canonical_event_name(&event.name),
                }))
            }
            AdminSymbolKind::ComponentSlot => {
                let Some(slot) = component.component_slot(&target.name) else {
                    return Ok(None);
                };
                let Some(owner) = first([&slot.file_path, &component.template_path, ""]) else {
                    return Ok(None);
                };
                Ok(Some(AdminSymbolTarget {
                    kind: AdminSymbolKind::ComponentSlot,
                    owner,
                    name: target.name.clone(),
                }))
            }
            _ => Ok(None),
        }
    }
}

pub(crate) fn dynamic_usage_components_match_target(
    components: &[VueComponent],
    set: &AdminUsageSet,
    target: &AdminSymbolTarget,
) -> bool {
    let owner = normalize_definition_path(&target.owner);
    for component in components {
        if set.kind == AdminSymbolKind::ComponentModel {
            if component.component_models().iter().any(|binding| {
                binding.attribute_name == set.name
                    && component_model_binding_matches_target(component, binding, target)
            }) {
                return true;
            }
            continue;
        }
        if component
            .symbol_source(target.kind, &target.name)
            .is_some_and(|source| normalize_definition_path(&source) == owner)
        {
            return true;
        }
    }
    false
}

pub(crate) fn component_model_binding_matches_target(
    component: &VueComponent,
    binding: &VueComponentModelBinding,
    target: &AdminSymbolTarget,
) -> bool {
    let name = if target.kind == AdminSymbolKind::ComponentEvent {
        &binding.event_name
    } else {
        &binding.prop_name
    };
    if *name != target.name {
        return false;
    }
    component
        .symbol_source(target.kind, &target.name)
        .is_some_and(|source| filepath::clean(&source) == filepath::clean(&target.owner))
}

pub(crate) fn unique_admin_usage_sets(values: Vec<AdminUsageSet>) -> Vec<AdminUsageSet> {
    let mut seen = HashSet::with_capacity(values.len());
    values
        .into_iter()
        .filter(|value| {
            seen.insert(format!(
                "{}\x00{}",
                admin_usage_key(value.kind, &value.owner, &value.name),
                normalize_definition_path(&value.file_path)
            ))
        })
        .collect()
}

pub(crate) fn component_member_target(member: &VueComponentMember) -> AdminSymbolTarget {
    AdminSymbolTarget {
        kind: AdminSymbolKind::ComponentMember,
        owner: member.source_identity(),
        name: member.name.clone(),
    }
}
