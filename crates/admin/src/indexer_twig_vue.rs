//! Twig/Vue template scope resolution against the index.
//!
//! A `template_path` of `""` means "no template".

use crate::*;

/// One resolved receiver step.
#[derive(Clone, Debug, Default)]
pub(crate) struct ResolvedReceiverSegment {
    pub type_: String,
    pub members: Vec<TwigVueMember>,
    pub complete: bool,
    pub context_path: String,
    pub found: bool,
}

/// Resolves `owner` or falls back to the component of `template_path`.
fn owned_or_fetched<'a>(
    idx: &AdminComponentIndexer,
    owner: Option<&'a VueComponent>,
    template_path: &str,
    storage: &'a mut Option<VueComponent>,
) -> IndexResult<Option<&'a VueComponent>> {
    if let Some(owner) = owner {
        return Ok(Some(owner));
    }
    if template_path.is_empty() {
        return Ok(None);
    }
    *storage = idx.get_component_by_template_path(template_path)?;
    Ok(storage.as_ref())
}

impl AdminComponentIndexer {
    pub fn resolve_twig_scoped_slot(
        &self,
        root: &SyntaxNode,
        offset: u32,
        template_path: &str,
    ) -> IndexResult<Option<ResolvedTwigScopedSlot>> {
        Ok(self
            .resolve_twig_scoped_slots_impl(root, offset, template_path, None)?
            .pop())
    }

    pub fn resolve_twig_scoped_slot_for_owner(
        &self,
        root: &SyntaxNode,
        offset: u32,
        template_path: &str,
        owner: Option<&VueComponent>,
    ) -> IndexResult<Option<ResolvedTwigScopedSlot>> {
        Ok(self
            .resolve_twig_scoped_slots_impl(root, offset, template_path, owner)?
            .pop())
    }

    /// Every lexical `v-slot` scope visible at the offset, outermost first.
    pub fn resolve_twig_scoped_slots(
        &self,
        root: &SyntaxNode,
        offset: u32,
        template_path: &str,
    ) -> IndexResult<Vec<ResolvedTwigScopedSlot>> {
        self.resolve_twig_scoped_slots_impl(root, offset, template_path, None)
    }

    pub fn resolve_twig_scoped_slots_for_owner(
        &self,
        root: &SyntaxNode,
        offset: u32,
        template_path: &str,
        owner: Option<&VueComponent>,
    ) -> IndexResult<Vec<ResolvedTwigScopedSlot>> {
        self.resolve_twig_scoped_slots_impl(root, offset, template_path, owner)
    }

    pub(crate) fn resolve_twig_scoped_slots_impl(
        &self,
        root: &SyntaxNode,
        offset: u32,
        template_path: &str,
        owner: Option<&VueComponent>,
    ) -> IndexResult<Vec<ResolvedTwigScopedSlot>> {
        let scopes = twig_scoped_slots_at_offset(root, offset);
        let mut resolved = Vec::with_capacity(scopes.len());
        for scope in &scopes {
            if let Some(current) =
                self.resolve_twig_scoped_slot_impl(root, scope, template_path, owner)?
            {
                resolved.push(current);
            }
        }
        Ok(resolved)
    }

    pub(crate) fn resolve_twig_scoped_slot_impl(
        &self,
        root: &SyntaxNode,
        scope: &TwigScopedSlot,
        template_path: &str,
        owner: Option<&VueComponent>,
    ) -> IndexResult<Option<ResolvedTwigScopedSlot>> {
        let open_slot = |component: VueComponent| ResolvedTwigScopedSlot {
            component,
            slot: VueComponentSlot {
                name: scope.slot_name.clone(),
                members_complete: false,
                ..Default::default()
            },
            scope: scope.clone(),
            ..Default::default()
        };
        if let Some(start_tag) = twig_scoped_slot_starting_tag(root, scope) {
            let (components, complete) =
                self.resolve_twig_slot_consumer_components(template_path, &start_tag, owner)?;
            if complete && !components.is_empty() {
                return Ok(Some(resolve_twig_scoped_slot_contracts(
                    scope,
                    &components,
                    complete,
                )));
            }
            let dynamic = twig_slot_owner_starting_tag(&start_tag)
                .is_some_and(|tag| twig_dynamic_component_selector(&tag).is_some());
            if dynamic {
                // Runtime-open selectors retain lexical locals but never
                // borrow a payload shape from only the visible branches.
                return Ok(Some(open_slot(VueComponent {
                    name: scope.component_name.clone(),
                    ..Default::default()
                })));
            }
        }
        let Some(component) = self.get_effective_component(&scope.component_name)? else {
            return Ok(Some(open_slot(VueComponent {
                name: scope.component_name.clone(),
                ..Default::default()
            })));
        };
        if let Some(slot) = component.component_slot(&scope.slot_name).cloned() {
            return Ok(Some(ResolvedTwigScopedSlot {
                contracts: vec![ResolvedTwigSlotContract {
                    component: component.clone(),
                    slot: slot.clone(),
                }],
                component,
                slot,
                scope: scope.clone(),
                contracts_complete: true,
            }));
        }
        Ok(Some(ResolvedTwigScopedSlot {
            component,
            slot: VueComponentSlot {
                name: scope.slot_name.clone(),
                ..Default::default()
            },
            scope: scope.clone(),
            ..Default::default()
        }))
    }
}

pub(crate) fn resolve_twig_scoped_slot_contracts(
    scope: &TwigScopedSlot,
    components: &[VueComponent],
    selector_complete: bool,
) -> ResolvedTwigScopedSlot {
    let mut contracts = Vec::with_capacity(components.len());
    let mut all_found = selector_complete;
    for component in components {
        let Some(slot) = component.component_slot(&scope.slot_name) else {
            all_found = false;
            continue;
        };
        contracts.push(ResolvedTwigSlotContract {
            component: component.clone(),
            slot: slot.clone(),
        });
    }
    let component = if components.len() == 1 {
        components[0].clone()
    } else {
        VueComponent {
            name: components
                .iter()
                .map(|candidate| candidate.name.as_str())
                .collect::<Vec<_>>()
                .join(" | "),
            ..Default::default()
        }
    };
    let contracts_complete = all_found && contracts.len() == components.len();
    let mut result = ResolvedTwigScopedSlot {
        component,
        slot: VueComponentSlot {
            name: scope.slot_name.clone(),
            members_complete: false,
            ..Default::default()
        },
        scope: scope.clone(),
        contracts,
        contracts_complete,
    };
    if !result.contracts_complete {
        return result;
    }
    if result.contracts.len() == 1 {
        result.slot = result.contracts[0].slot.clone();
        return result;
    }
    result.slot = common_twig_scoped_slot_contract(&scope.slot_name, &result.contracts);
    result
}

pub(crate) fn common_twig_scoped_slot_contract(
    name: &str,
    contracts: &[ResolvedTwigSlotContract],
) -> VueComponentSlot {
    let mut result = VueComponentSlot {
        name: name.to_owned(),
        members_complete: !contracts.is_empty(),
        ..Default::default()
    };
    let Some(first) = contracts.first() else {
        return result;
    };
    result.file_path = first.slot.file_path.clone();
    result.line = first.slot.line;
    // `None` once any payload type is missing.
    let mut payload_types: Option<Vec<String>> = Some(Vec::with_capacity(contracts.len()));
    for contract in contracts {
        result.members_complete = result.members_complete && contract.slot.members_complete;
        if contract.slot.file_path != result.file_path || contract.slot.line != result.line {
            result.file_path = String::new();
            result.line = 0;
        }
        if contract.slot.payload_type.is_empty() {
            payload_types = None;
        } else if let Some(types) = payload_types.as_mut() {
            types.push(contract.slot.payload_type.clone());
        }
    }
    // Merge only when every contract declares a payload type.
    if let Some(types) = payload_types
        && types.len() == contracts.len()
    {
        result.payload_type =
            merge_vue_types(&types.iter().map(String::as_str).collect::<Vec<_>>());
    }
    for candidate in &first.slot.members {
        let mut members = vec![candidate];
        let mut common = true;
        for contract in &contracts[1..] {
            let Some(member) = contract.slot.member(&candidate.name) else {
                common = false;
                break;
            };
            members.push(member);
        }
        if !common {
            continue;
        }
        let mut member = candidate.clone();
        let mut types = Vec::with_capacity(members.len());
        for current in &members {
            if !current.type_.is_empty() {
                types.push(current.type_.clone());
            }
            if current.file_path != member.file_path || current.line != member.line {
                member.file_path = String::new();
                member.line = 0;
            }
        }
        member.type_ = if types.len() == members.len() {
            merge_vue_types(&types.iter().map(String::as_str).collect::<Vec<_>>())
        } else {
            String::new()
        };
        result.members.push(member);
    }
    result
}

impl AdminComponentIndexer {
    /// Resolves an identifier in either the binding declaration or the
    /// evaluated body of a scoped slot.
    pub fn resolve_twig_scoped_slot_binding(
        &self,
        root: &SyntaxNode,
        node: &SyntaxNode,
        content: &str,
        offset: u32,
        template_path: &str,
    ) -> IndexResult<Option<ResolvedTwigSlotBinding>> {
        self.resolve_twig_scoped_slot_binding_impl(root, node, content, offset, template_path, None)
    }

    pub fn resolve_twig_scoped_slot_binding_for_owner(
        &self,
        root: &SyntaxNode,
        node: &SyntaxNode,
        content: &str,
        offset: u32,
        template_path: &str,
        owner: Option<&VueComponent>,
    ) -> IndexResult<Option<ResolvedTwigSlotBinding>> {
        self.resolve_twig_scoped_slot_binding_impl(
            root,
            node,
            content,
            offset,
            template_path,
            owner,
        )
    }

    pub(crate) fn resolve_twig_scoped_slot_binding_impl(
        &self,
        root: &SyntaxNode,
        node: &SyntaxNode,
        content: &str,
        offset: u32,
        template_path: &str,
        owner: Option<&VueComponent>,
    ) -> IndexResult<Option<ResolvedTwigSlotBinding>> {
        let Some((identifier, range_value)) = identifier_at_offset(content, offset) else {
            return Ok(None);
        };
        let scopes = twig_scoped_slots_at_offset(root, offset);
        for scope in scopes.iter().rev() {
            let in_binding = scope.is_binding_offset(offset);
            let mut current_identifier = identifier.clone();
            let mut current_range = range_value;
            if !in_binding {
                if !is_twig_vue_expression_at(node, offset) {
                    continue;
                }
                let Some((root_identifier, root_range)) =
                    expression_root_identifier_at_offset(content, offset)
                else {
                    continue;
                };
                current_identifier = root_identifier;
                current_range = root_range;
            }
            for binding in &scope.bindings {
                let matched = current_identifier == binding.local_name
                    || in_binding && current_identifier == binding.member_name;
                if !matched {
                    continue;
                }
                let Some(resolved) =
                    self.resolve_twig_scoped_slot_impl(root, scope, template_path, owner)?
                else {
                    return Ok(None);
                };
                let member = resolved.slot.member(&binding.member_name).cloned();
                let members =
                    resolved_twig_slot_contract_members(&resolved.contracts, &binding.member_name);
                return Ok(Some(ResolvedTwigSlotBinding {
                    scoped: resolved,
                    binding: binding.clone(),
                    member_found: member.is_some(),
                    member: member.unwrap_or_default(),
                    members,
                    identifier: current_identifier,
                    range: current_range,
                }));
            }
        }
        Ok(None)
    }

    /// Resolves a direct property accessed through a whole-object scoped-slot
    /// local. Returns a result even when `member_found` is false.
    pub fn resolve_twig_scoped_slot_member(
        &self,
        root: &SyntaxNode,
        node: &SyntaxNode,
        content: &str,
        offset: u32,
        template_path: &str,
    ) -> IndexResult<Option<ResolvedTwigSlotMember>> {
        self.resolve_twig_scoped_slot_member_impl(root, node, content, offset, template_path, None)
    }

    pub fn resolve_twig_scoped_slot_member_for_owner(
        &self,
        root: &SyntaxNode,
        node: &SyntaxNode,
        content: &str,
        offset: u32,
        template_path: &str,
        owner: Option<&VueComponent>,
    ) -> IndexResult<Option<ResolvedTwigSlotMember>> {
        self.resolve_twig_scoped_slot_member_impl(root, node, content, offset, template_path, owner)
    }

    pub(crate) fn resolve_twig_scoped_slot_member_impl(
        &self,
        root: &SyntaxNode,
        node: &SyntaxNode,
        content: &str,
        offset: u32,
        template_path: &str,
        owner: Option<&VueComponent>,
    ) -> IndexResult<Option<ResolvedTwigSlotMember>> {
        if !is_twig_vue_expression_at(node, offset) {
            return Ok(None);
        }
        let Some(access) = twig_vue_expression_member_at_offset(root, content, offset) else {
            return Ok(None);
        };
        let scopes = twig_scoped_slots_at_offset(root, offset);
        for scope in scopes.iter().rev() {
            for binding in &scope.bindings {
                if !binding.whole_object || binding.local_name != access.root {
                    continue;
                }
                let Some(resolved) =
                    self.resolve_twig_scoped_slot_impl(root, scope, template_path, owner)?
                else {
                    return Ok(None);
                };
                let mut result = ResolvedTwigSlotMember {
                    binding: binding.clone(),
                    access: access.clone(),
                    receiver_found: true,
                    receiver_members: slot_twig_vue_members(&resolved.slot.members),
                    members_complete: resolved.slot.members_complete,
                    receiver_type: resolved.slot.payload_type.clone(),
                    scoped: resolved,
                    ..Default::default()
                };
                let mut context_path = result.scoped.slot.file_path.clone();
                for segment in &access.receiver {
                    let index_type = self
                        .resolve_twig_vue_index_expression_type(root, content, segment, "", None)?;
                    let step = self.resolve_twig_vue_receiver_segment(
                        &result.receiver_type,
                        &result.receiver_members,
                        &context_path,
                        segment,
                        &index_type,
                        &[],
                    )?;
                    if !step.found {
                        result.receiver_found = false;
                        result.receiver_members = Vec::new();
                        result.members_complete = false;
                        return Ok(Some(result));
                    }
                    result.receiver_type = step.type_;
                    result.receiver_members = step.members;
                    result.members_complete = step.complete;
                    context_path = step.context_path;
                }
                let member = twig_vue_member_named(&result.receiver_members, &access.member);
                result.member_found = member.is_some();
                result.member = slot_member_from_twig_vue(&member.unwrap_or_default());
                if access.receiver.is_empty() {
                    result.members = resolved_twig_slot_contract_members(
                        &result.scoped.contracts,
                        &access.member,
                    );
                }
                return Ok(Some(result));
            }
        }
        Ok(None)
    }
}

pub(crate) fn resolved_twig_slot_contract_members(
    contracts: &[ResolvedTwigSlotContract],
    name: &str,
) -> Vec<VueComponentSlotMember> {
    let mut result: Vec<VueComponentSlotMember> = Vec::with_capacity(contracts.len());
    for contract in contracts {
        let Some(member) = contract.slot.member(name) else {
            continue;
        };
        if result.contains(member) {
            continue;
        }
        result.push(member.clone());
    }
    result
}

pub(crate) fn slot_twig_vue_members(members: &[VueComponentSlotMember]) -> Vec<TwigVueMember> {
    members
        .iter()
        .map(|member| TwigVueMember {
            name: member.name.clone(),
            type_: member.type_.clone(),
            definition_path: member.file_path.clone(),
            definition_line: member.line,
            definition_range: member.name_range.clone(),
            ..Default::default()
        })
        .collect()
}

pub(crate) fn slot_member_from_twig_vue(member: &TwigVueMember) -> VueComponentSlotMember {
    VueComponentSlotMember {
        name: member.name.clone(),
        type_: member.type_.clone(),
        file_path: member.definition_path.clone(),
        line: member.definition_line,
        name_range: member.definition_range.clone(),
    }
}

impl AdminComponentIndexer {
    /// Resolves document-local Vue template variables and enriches `$event`
    /// with the indexed component event payload.
    pub fn resolve_twig_vue_bindings(
        &self,
        root: &SyntaxNode,
        content: &str,
        offset: u32,
        template_path: &str,
    ) -> IndexResult<Vec<TwigVueBinding>> {
        self.resolve_twig_vue_bindings_impl(root, content, offset, template_path, None)
    }

    /// Resolves lexical bindings against a request-local owner component.
    pub fn resolve_twig_vue_bindings_for_component(
        &self,
        root: &SyntaxNode,
        content: &str,
        offset: u32,
        template_path: &str,
        component: Option<&VueComponent>,
    ) -> IndexResult<Vec<TwigVueBinding>> {
        self.resolve_twig_vue_bindings_impl(root, content, offset, template_path, component)
    }

    pub(crate) fn resolve_twig_vue_bindings_impl(
        &self,
        root: &SyntaxNode,
        content: &str,
        offset: u32,
        template_path: &str,
        component: Option<&VueComponent>,
    ) -> IndexResult<Vec<TwigVueBinding>> {
        let mut bindings = twig_vue_bindings_at_offset(root, content, offset);
        for index in 0..bindings.len() {
            bindings[index].members = twig_vue_binding_members(root, content, &bindings[index]);
            let (visible, rest) = bindings.split_at_mut(index);
            self.enrich_twig_vue_binding_with_visible(
                &mut rest[0],
                template_path,
                visible,
                component,
            )?;
        }
        Ok(bindings)
    }

    /// Resolves the lexical Vue variable under the cursor.
    pub fn resolve_twig_vue_binding(
        &self,
        root: &SyntaxNode,
        content: &str,
        offset: u32,
        template_path: &str,
    ) -> IndexResult<Option<TwigVueBinding>> {
        self.resolve_twig_vue_binding_impl(root, content, offset, template_path, None)
    }

    pub fn resolve_twig_vue_binding_for_component(
        &self,
        root: &SyntaxNode,
        content: &str,
        offset: u32,
        template_path: &str,
        component: Option<&VueComponent>,
    ) -> IndexResult<Option<TwigVueBinding>> {
        self.resolve_twig_vue_binding_impl(root, content, offset, template_path, component)
    }

    pub(crate) fn resolve_twig_vue_binding_impl(
        &self,
        root: &SyntaxNode,
        content: &str,
        offset: u32,
        template_path: &str,
        component: Option<&VueComponent>,
    ) -> IndexResult<Option<TwigVueBinding>> {
        let Some(target) = twig_vue_binding_at_offset(root, content, offset) else {
            return Ok(None);
        };
        let bindings =
            self.resolve_twig_vue_bindings_impl(root, content, offset, template_path, component)?;
        Ok(bindings
            .into_iter()
            .find(|binding| binding.same_identity(&target)))
    }

    /// Resolves one complete template expression against its lexical scope
    /// and effective component instance.
    pub fn resolve_twig_vue_expression_type(
        &self,
        root: &SyntaxNode,
        content: &str,
        expression: &str,
        offset: u32,
        template_path: &str,
    ) -> IndexResult<Option<String>> {
        self.resolve_twig_vue_expression_type_impl(
            root,
            content,
            expression,
            offset,
            template_path,
            None,
        )
    }

    /// The live-document counterpart of
    /// [`Self::resolve_twig_vue_expression_type`].
    pub fn resolve_twig_vue_expression_type_for_component(
        &self,
        root: &SyntaxNode,
        content: &str,
        expression: &str,
        offset: u32,
        template_path: &str,
        component: Option<&VueComponent>,
    ) -> IndexResult<Option<String>> {
        self.resolve_twig_vue_expression_type_impl(
            root,
            content,
            expression,
            offset,
            template_path,
            component,
        )
    }

    pub(crate) fn resolve_twig_vue_expression_type_impl(
        &self,
        root: &SyntaxNode,
        content: &str,
        expression: &str,
        offset: u32,
        template_path: &str,
        component: Option<&VueComponent>,
    ) -> IndexResult<Option<String>> {
        if expression.trim().is_empty() {
            return Ok(None);
        }
        let visible =
            self.resolve_twig_vue_bindings_impl(root, content, offset, template_path, component)?;
        let mut storage = None;
        let component = owned_or_fetched(self, component, template_path, &mut storage)?;
        if mutable_vue_data_expression(expression, &visible, component) {
            // An untyped data seed is not authoritative once methods assign
            // another value to it.
            return Ok(None);
        }
        let (resolved, found) =
            self.resolve_twig_vue_iterable_expression_type(expression, &visible, component)?;
        if !found || resolved.type_.is_empty() {
            return Ok(None);
        }
        Ok(Some(resolved.type_))
    }
}

pub(crate) fn mutable_vue_data_expression(
    expression: &str,
    visible: &[TwigVueBinding],
    component: Option<&VueComponent>,
) -> bool {
    let Some(component) = component else {
        return false;
    };
    let Some(path) = vue_static_template_expression(expression) else {
        return false;
    };
    let Some(first) = path.first() else {
        return false;
    };
    let root = &first.name;
    if visible.iter().rev().any(|binding| binding.name == *root) {
        return false;
    }
    let Some(member) = component.template_member(root) else {
        return false;
    };
    if member.kind != VueComponentMemberKind::Data {
        return false;
    }
    component
        .assignments
        .iter()
        .any(|assignment| assignment.target == *root)
}

impl AdminComponentIndexer {
    /// Resolves the property under the cursor through the structural type
    /// of a lexical Vue binding.
    pub fn resolve_twig_vue_member(
        &self,
        root: &SyntaxNode,
        content: &str,
        offset: u32,
        template_path: &str,
    ) -> IndexResult<Option<ResolvedTwigVueMember>> {
        self.resolve_twig_vue_member_impl(root, content, offset, template_path, None)
    }

    pub fn resolve_twig_vue_member_for_component(
        &self,
        root: &SyntaxNode,
        content: &str,
        offset: u32,
        template_path: &str,
        component: Option<&VueComponent>,
    ) -> IndexResult<Option<ResolvedTwigVueMember>> {
        self.resolve_twig_vue_member_impl(root, content, offset, template_path, component)
    }

    pub(crate) fn resolve_twig_vue_member_impl(
        &self,
        root: &SyntaxNode,
        content: &str,
        offset: u32,
        template_path: &str,
        component: Option<&VueComponent>,
    ) -> IndexResult<Option<ResolvedTwigVueMember>> {
        let Some(access) = twig_vue_expression_member_at_offset(root, content, offset) else {
            return Ok(None);
        };
        let Some(binding) = self.resolve_twig_vue_binding_impl(
            root,
            content,
            u32::from(access.root_range.start()),
            template_path,
            component,
        )?
        else {
            return Ok(None);
        };
        let live_files = component_live_type_files(component);
        let mut context_path = binding.type_context_path.clone();
        let mut resolved = ResolvedTwigVueMember {
            receiver_found: true,
            receiver_type: binding.type_.clone(),
            receiver_members: binding.members.clone(),
            members_complete: binding.members_complete,
            binding,
            access: access.clone(),
            ..Default::default()
        };
        if access.root_called {
            let receiver_type = vue_callable_return_type(&resolved.binding.type_);
            if receiver_type.is_empty() {
                resolved.receiver_found = false;
                resolved.receiver_members = Vec::new();
                resolved.members_complete = false;
                return Ok(Some(resolved));
            }
            let shape = self.resolve_vue_type(&receiver_type, &context_path, live_files)?;
            resolved.receiver_type = receiver_type;
            resolved.receiver_members = shape.members;
            resolved.members_complete = shape.complete;
        }
        for segment in &access.receiver {
            let index_type = self.resolve_twig_vue_index_expression_type(
                root,
                content,
                segment,
                template_path,
                component,
            )?;
            let step = self.resolve_twig_vue_receiver_segment(
                &resolved.receiver_type,
                &resolved.receiver_members,
                &context_path,
                segment,
                &index_type,
                live_files,
            )?;
            if !step.found {
                resolved.receiver_found = false;
                resolved.receiver_members = Vec::new();
                resolved.members_complete = false;
                return Ok(Some(resolved));
            }
            resolved.receiver_type = step.type_;
            resolved.receiver_members = step.members;
            resolved.members_complete = step.complete;
            context_path = step.context_path;
        }
        let member = twig_vue_member_named(&resolved.receiver_members, &access.member);
        resolved.member_found = member.is_some();
        resolved.member = member.unwrap_or_default();
        Ok(Some(resolved))
    }

    /// Resolves a property chain rooted in the effective component instance.
    /// Lexical Vue and scoped-slot bindings win over component members.
    pub fn resolve_twig_vue_instance_member(
        &self,
        root: &SyntaxNode,
        content: &str,
        offset: u32,
        template_path: &str,
    ) -> IndexResult<Option<ResolvedTwigVueInstanceMember>> {
        self.resolve_twig_vue_instance_member_impl(root, content, offset, template_path, None)
    }

    pub fn resolve_twig_vue_instance_member_for_component(
        &self,
        root: &SyntaxNode,
        content: &str,
        offset: u32,
        template_path: &str,
        component: Option<&VueComponent>,
    ) -> IndexResult<Option<ResolvedTwigVueInstanceMember>> {
        self.resolve_twig_vue_instance_member_impl(root, content, offset, template_path, component)
    }

    /// Resolves a pre-parsed access whose root the caller already proved is
    /// not a lexical binding.
    pub fn resolve_twig_vue_instance_member_access_for_component(
        &self,
        root: &SyntaxNode,
        content: &str,
        access: &TwigVueMemberAccess,
        template_path: &str,
        component: Option<&VueComponent>,
    ) -> IndexResult<Option<ResolvedTwigVueInstanceMember>> {
        self.resolve_twig_vue_instance_member_access(
            root,
            content,
            access,
            template_path,
            component,
            false,
        )
    }

    pub(crate) fn resolve_twig_vue_instance_member_impl(
        &self,
        root: &SyntaxNode,
        content: &str,
        offset: u32,
        template_path: &str,
        component: Option<&VueComponent>,
    ) -> IndexResult<Option<ResolvedTwigVueInstanceMember>> {
        if template_path.is_empty() {
            return Ok(None);
        }
        let Some(access) = twig_vue_expression_member_at_offset(root, content, offset) else {
            return Ok(None);
        };
        self.resolve_twig_vue_instance_member_access(
            root,
            content,
            &access,
            template_path,
            component,
            true,
        )
    }

    pub(crate) fn resolve_twig_vue_instance_member_access(
        &self,
        root: &SyntaxNode,
        content: &str,
        access: &TwigVueMemberAccess,
        template_path: &str,
        component: Option<&VueComponent>,
        check_lexical_scope: bool,
    ) -> IndexResult<Option<ResolvedTwigVueInstanceMember>> {
        let root_start = u32::from(access.root_range.start());
        if check_lexical_scope {
            if twig_vue_binding_at_offset(root, content, root_start).is_some() {
                return Ok(None);
            }
            if let Some(scope) = twig_scoped_slot_at_offset(root, root_start)
                && scope
                    .bindings
                    .iter()
                    .any(|binding| binding.local_name == access.root)
            {
                return Ok(None);
            }
        }
        let fetched;
        let component = match component {
            Some(component) => component,
            None => {
                fetched = self.get_component_by_template_path(template_path)?;
                match fetched.as_ref() {
                    Some(component) => component,
                    None => return Ok(None),
                }
            }
        };
        let Some(root_member) = component
            .template_member(&access.root)
            .or_else(|| vue_builtin_member(&access.root))
        else {
            return Ok(None);
        };
        let mut context_path = [
            &root_member.type_context_path,
            &root_member.file_path,
            &component.definition_path,
            &component.file_path,
        ]
        .into_iter()
        .find(|value| !value.is_empty())
        .cloned()
        .unwrap_or_default();
        let mut resolved = ResolvedTwigVueInstanceMember {
            component: component.clone(),
            root_member: root_member.clone(),
            access: access.clone(),
            receiver_found: true,
            receiver_type: root_member.type_.clone(),
            ..Default::default()
        };
        let Some(root_type) = twig_vue_receiver_type(&root_member.type_, access.root_called) else {
            resolved.receiver_found = false;
            return Ok(Some(resolved));
        };
        let live_files = component_live_type_files(Some(component));
        let shape = self.resolve_vue_type(&root_type, &context_path, live_files)?;
        resolved.receiver_type = root_type;
        resolved.members_complete = shape.complete && !root_member.open_runtime_shape;
        let open_runtime_shape = root_member.open_runtime_shape;
        let indexed_root = access
            .receiver
            .first()
            .is_some_and(|segment| segment.indexed);
        let shape_empty = shape.members.is_empty() && !shape.complete;
        resolved.receiver_members = shape.members;
        if root_member.type_.is_empty() || shape_empty && !indexed_root {
            resolved.receiver_found = false;
            return Ok(Some(resolved));
        }
        for segment in &access.receiver {
            let index_type = self.resolve_twig_vue_index_expression_type(
                root,
                content,
                segment,
                template_path,
                Some(component),
            )?;
            let step = self.resolve_twig_vue_receiver_segment(
                &resolved.receiver_type,
                &resolved.receiver_members,
                &context_path,
                segment,
                &index_type,
                live_files,
            )?;
            if !step.found {
                resolved.receiver_found = false;
                resolved.receiver_members = Vec::new();
                resolved.members_complete = false;
                return Ok(Some(resolved));
            }
            resolved.receiver_type = step.type_;
            resolved.receiver_members = step.members;
            resolved.members_complete = step.complete && !open_runtime_shape;
            context_path = step.context_path;
        }
        let member = twig_vue_member_named(&resolved.receiver_members, &access.member);
        resolved.member_found = member.is_some();
        resolved.member = member.unwrap_or_default();
        Ok(Some(resolved))
    }
}

/// The receiver type reached by a member, invoking it when `called`; `None`
/// when a called member is not callable.
pub(crate) fn twig_vue_receiver_type(member_type: &str, called: bool) -> Option<String> {
    if !called {
        return Some(member_type.to_owned());
    }
    let return_type = vue_callable_return_type(member_type);
    (!return_type.is_empty()).then_some(return_type)
}

impl AdminComponentIndexer {
    pub(crate) fn resolve_twig_vue_receiver_segment(
        &self,
        receiver_type: &str,
        receiver_members: &[TwigVueMember],
        context_path: &str,
        segment: &TwigVueMemberSegment,
        index_type: &str,
        live_files: &[AdminTypeFile],
    ) -> IndexResult<ResolvedReceiverSegment> {
        let not_found = |context_path: String| ResolvedReceiverSegment {
            context_path,
            ..Default::default()
        };
        let mut context_path = context_path.to_owned();
        if segment.indexed {
            let indexed_receiver_type = if segment.optional {
                without_vue_nullish_type(receiver_type)
            } else {
                receiver_type.to_owned()
            };
            let Some((indexed_type, indexed_context)) = self
                .resolve_vue_indexed_access_type_with_index_type(
                    &indexed_receiver_type,
                    &segment.index_expression,
                    index_type,
                    &context_path,
                    live_files,
                )?
            else {
                return Ok(not_found(context_path));
            };
            if !indexed_context.is_empty() {
                context_path = indexed_context;
            }
            let shape = self.resolve_vue_type(&indexed_type, &context_path, live_files)?;
            return Ok(ResolvedReceiverSegment {
                type_: indexed_type,
                members: shape.members,
                complete: shape.complete,
                context_path,
                found: true,
            });
        }
        let Some(member) = twig_vue_member_named(receiver_members, &segment.name) else {
            return Ok(not_found(context_path));
        };
        if !member.definition_path.is_empty() {
            context_path = member.definition_path.clone();
        }
        let Some(next_type) = twig_vue_receiver_type(&member.type_, segment.called) else {
            return Ok(not_found(context_path));
        };
        let shape = self.resolve_vue_type(&next_type, &context_path, live_files)?;
        Ok(ResolvedReceiverSegment {
            type_: next_type,
            members: shape.members,
            complete: shape.complete,
            context_path,
            found: true,
        })
    }

    pub(crate) fn resolve_twig_vue_index_expression_type(
        &self,
        root: &SyntaxNode,
        content: &str,
        segment: &TwigVueMemberSegment,
        template_path: &str,
        component: Option<&VueComponent>,
    ) -> IndexResult<String> {
        if !segment.indexed {
            return Ok(String::new());
        }
        let expression = unwrap_vue_expression_parentheses(segment.index_expression.trim());
        if expression.is_empty() {
            return Ok(String::new());
        }
        let static_type = vue_static_index_expression_type(&expression);
        if !static_type.is_empty() {
            return Ok(static_type);
        }
        let mut storage = None;
        let component = owned_or_fetched(self, component, template_path, &mut storage)?;
        let bindings = self.resolve_twig_vue_bindings_impl(
            root,
            content,
            u32::from(segment.index_range.start()),
            template_path,
            component,
        )?;
        let mut known: HashMap<String, String> = HashMap::with_capacity(bindings.len());
        for binding in &bindings {
            if !binding.name.is_empty() && !binding.type_.is_empty() {
                known.insert(binding.name.clone(), binding.type_.clone());
            }
        }
        if let Some(component) = component {
            for member in component.template_members() {
                if !member.name.is_empty() && !member.type_.is_empty() {
                    known.insert(member.name, member.type_);
                }
            }
        }
        let resolved = resolve_index_expression(&expression, &known);
        if !resolved.is_empty() {
            return Ok(resolved);
        }
        if segment.index_range.is_empty() {
            return Ok(String::new());
        }
        let mut member_offset = u32::from(segment.index_range.end());
        if member_offset > u32::from(segment.index_range.start()) {
            member_offset -= 1;
        }
        let resolved_binding = self.resolve_twig_vue_member_impl(
            root,
            content,
            member_offset,
            template_path,
            component,
        )?;
        if let Some(resolved) = resolved_binding
            && resolved.member_found
        {
            return Ok(resolved.member.type_);
        }
        let Some(component) = component else {
            return Ok(String::new());
        };
        if template_path.is_empty() {
            return Ok(String::new());
        }
        let resolved_instance = self.resolve_twig_vue_instance_member_impl(
            root,
            content,
            member_offset,
            template_path,
            Some(component),
        )?;
        if let Some(resolved) = resolved_instance
            && resolved.member_found
        {
            return Ok(resolved.member.type_);
        }
        Ok(String::new())
    }
}

/// Resolves an index expression recursively against `known` variable types.
fn resolve_index_expression(value: &str, known: &HashMap<String, String>) -> String {
    let value = unwrap_vue_expression_parentheses(value.trim());
    if value.is_empty() {
        return String::new();
    }
    let static_type = vue_static_index_expression_type(&value);
    if !static_type.is_empty() {
        return static_type;
    }
    if is_slot_identifier(&value) {
        return known.get(value.as_str()).cloned().unwrap_or_default();
    }
    if let Some((left, right)) = split_vue_top_level_operator(&value, "+") {
        let left_type = without_vue_nullish_type(&resolve_index_expression(&left, known));
        let right_type = without_vue_nullish_type(&resolve_index_expression(&right, known));
        if left_type == "number" && right_type == "number" {
            return "number".to_owned();
        }
        if left_type == "string" || right_type == "string" {
            return "string".to_owned();
        }
    }
    String::new()
}

pub(crate) fn twig_vue_member_named(
    members: &[TwigVueMember],
    name: &str,
) -> Option<TwigVueMember> {
    members.iter().find(|member| member.name == name).cloned()
}

impl AdminComponentIndexer {
    pub(crate) fn enrich_twig_vue_binding_with_visible(
        &self,
        binding: &mut TwigVueBinding,
        template_path: &str,
        visible: &[TwigVueBinding],
        component: Option<&VueComponent>,
    ) -> IndexResult<()> {
        if binding.kind == TwigVueBindingKind::For {
            return self.enrich_twig_vue_for_binding(binding, template_path, visible, component);
        }
        if binding.kind != TwigVueBindingKind::Event {
            return Ok(());
        }
        if binding.component_name.is_empty() {
            binding.type_ = native_event_payload_type(&binding.event_name);
            return Ok(());
        }
        let Some(component) = self.get_effective_component(&binding.component_name)? else {
            binding.type_ = native_event_payload_type(&binding.event_name);
            return Ok(());
        };
        let Some(event) = component.component_event(&binding.event_name) else {
            binding.type_ = native_event_payload_type(&binding.event_name);
            return Ok(());
        };
        binding.type_ = event_payload_type(&event.type_);
        binding.definition_path = [
            &event.file_path,
            &component.definition_path,
            &component.file_path,
        ]
        .into_iter()
        .find(|value| !value.is_empty())
        .cloned()
        .unwrap_or_default();
        binding.definition_line = event.line;
        Ok(())
    }

    pub(crate) fn enrich_twig_vue_for_binding(
        &self,
        binding: &mut TwigVueBinding,
        template_path: &str,
        visible: &[TwigVueBinding],
        component: Option<&VueComponent>,
    ) -> IndexResult<()> {
        if template_path.is_empty() {
            return Ok(());
        }
        let mut storage = None;
        let component = owned_or_fetched(self, component, template_path, &mut storage)?;
        let (resolved, found) =
            self.resolve_twig_vue_iterable_expression_type(&binding.iterable, visible, component)?;
        if !found || resolved.type_.is_empty() {
            return Ok(());
        }
        let type_context_path = resolved.context_path.clone();
        let binding_type = vue_iterable_binding_type(&resolved.type_, binding.ordinal);
        let mut shape = self.resolve_vue_type(
            &binding_type,
            &type_context_path,
            component_live_type_files(component),
        )?;
        let trimmed = binding_type.trim();
        let members_complete = shape.complete
            && trimmed != "unknown"
            && trimmed != "any"
            && !resolved.open_runtime_shape;
        if binding.ordinal == 0 {
            shape.members = merge_twig_vue_members(
                &shape.members,
                &component_iterable_element_members(&binding.iterable, component),
            );
        }
        binding.members = if members_complete {
            merge_known_twig_vue_members(&shape.members, &binding.members)
        } else {
            merge_twig_vue_members(&shape.members, &binding.members)
        };
        binding.members_complete = members_complete;
        binding.type_context_path = type_context_path;
        binding.type_ = binding_type;
        Ok(())
    }
}

pub(crate) fn component_iterable_element_members(
    expression: &str,
    component: Option<&VueComponent>,
) -> Vec<TwigVueMember> {
    let Some(component) = component else {
        return Vec::new();
    };
    let Some(path) = vue_static_template_expression(expression) else {
        return Vec::new();
    };
    if path.len() != 1 || path[0].called || path[0].optional {
        return Vec::new();
    }
    let Some(member) = component.template_member(&path[0].name) else {
        return Vec::new();
    };
    member
        .element_members
        .iter()
        .map(|element| TwigVueMember {
            name: element.name.clone(),
            type_: element.type_.clone(),
            definition_path: element.file_path.clone(),
            definition_line: element.line,
            ..Default::default()
        })
        .collect()
}

impl AdminComponentIndexer {
    pub(crate) fn resolve_twig_vue_iterable_expression_type(
        &self,
        expression: &str,
        visible: &[TwigVueBinding],
        component: Option<&VueComponent>,
    ) -> ResolvedExpression {
        let expression = trim_vue_source_expression(expression);
        if expression.is_empty() {
            return Ok((ResolvedVueExpressionType::default(), false));
        }
        if let Some((left, right)) = split_vue_top_level_operator(&expression, "??") {
            let (left_resolved, left_found) =
                self.resolve_twig_vue_iterable_expression_type(&left, visible, component)?;
            let (right_resolved, right_found) =
                self.resolve_twig_vue_iterable_expression_type(&right, visible, component)?;
            if !left_found {
                return Ok(unknown_or_default(right_resolved, right_found));
            }
            let mut result = left_resolved.clone();
            result.type_ = merge_vue_nullish_types(&left_resolved.type_, &right_resolved.type_);
            if result.context_path.is_empty() {
                result.context_path = right_resolved.context_path.clone();
            }
            result.open_runtime_shape =
                left_resolved.open_runtime_shape || right_resolved.open_runtime_shape;
            let found = !result.type_.is_empty();
            return Ok((result, found));
        }
        let context_path = component
            .map(|component| {
                if component.definition_path.is_empty() {
                    component.file_path.clone()
                } else {
                    component.definition_path.clone()
                }
            })
            .unwrap_or_default();
        let resolve_argument = |argument: &str| {
            self.resolve_twig_vue_iterable_expression_type(argument, visible, component)
        };
        let (transformed, found) = self.resolve_vue_object_transform_expression_type(
            &expression,
            &context_path,
            Some(&resolve_argument),
        )?;
        if found {
            return Ok((transformed, true));
        }
        let literal_type = vue_static_literal_type(&expression);
        if !literal_type.is_empty() {
            return Ok((
                ResolvedVueExpressionType {
                    type_: literal_type,
                    context_path,
                    ..Default::default()
                },
                true,
            ));
        }
        let literal_type = vue_expression_text_type(&expression, None);
        if !literal_type.is_empty() {
            return Ok((
                ResolvedVueExpressionType {
                    type_: literal_type,
                    context_path,
                    ..Default::default()
                },
                true,
            ));
        }
        let not_found = || Ok((ResolvedVueExpressionType::default(), false));
        let Some(iterable_path) = vue_static_template_expression(&expression) else {
            return not_found();
        };
        let Some(first) = iterable_path.first() else {
            return not_found();
        };
        let mut root_type = String::new();
        let mut root_context = String::new();
        let mut root_open_runtime_shape = false;
        if let Some(candidate) = visible
            .iter()
            .rev()
            .find(|candidate| candidate.name == first.name)
        {
            root_type = candidate.type_.clone();
            root_context = candidate.type_context_path.clone();
        }
        if root_type.is_empty()
            && let Some(component) = component
        {
            let Some(member) = component
                .template_member(&first.name)
                .or_else(|| vue_builtin_member(&first.name))
            else {
                return not_found();
            };
            root_type = member.type_.clone();
            root_context = component_member_type_context(&member, component);
            root_open_runtime_shape = member.open_runtime_shape;
        }
        if root_type.is_empty() {
            return not_found();
        }
        if first.called {
            root_type = vue_callable_return_type(&root_type);
            if root_type.is_empty() {
                return not_found();
            }
        }
        let (mut resolved, found) = self.resolve_vue_static_type_chain_with_optional(
            &root_type,
            &root_context,
            &iterable_path[1..],
            first.optional,
            component_live_type_files(component),
        )?;
        resolved.open_runtime_shape = resolved.open_runtime_shape || root_open_runtime_shape;
        Ok((resolved, found))
    }
}

/// The static `this.`-relative member path of a template expression.
pub(crate) fn vue_static_template_expression(
    expression: &str,
) -> Option<Vec<VueStaticExpressionSegment>> {
    let mut expression = expression.trim().to_owned();
    if let Some((left, right)) = split_vue_top_level_operator(&expression, "??")
        && unwrap_vue_expression_parentheses(&right) == "[]"
    {
        expression = left.to_string();
    }
    while expression.len() >= 2
        && expression.as_bytes()[0] == b'('
        && matching_slot_delimiter(&expression, 0, b'(', b')') == Some(expression.len() - 1)
    {
        expression = expression[1..expression.len() - 1].trim().to_owned();
    }
    if expression.is_empty() {
        return None;
    }
    if expression.starts_with("this.") {
        return vue_static_this_expression(&expression);
    }
    if !is_vue_identifier_start(expression.as_bytes()[0]) {
        return None;
    }
    vue_static_this_expression(&format!("this.{expression}"))
}

pub(crate) fn merge_twig_vue_members(
    base: &[TwigVueMember],
    overlay: &[TwigVueMember],
) -> Vec<TwigVueMember> {
    let mut result = base.to_vec();
    let mut positions: HashMap<String, usize> = HashMap::with_capacity(result.len());
    for (index, member) in result.iter().enumerate() {
        positions.insert(member.name.clone(), index);
    }
    for member in overlay {
        let mut member = member.clone();
        if let Some(&index) = positions.get(&member.name) {
            let existing = &result[index];
            if member.documentation.is_empty() {
                member.documentation = existing.documentation.clone();
            }
            if member.type_.is_empty() {
                member.type_ = existing.type_.clone();
            }
            if member.definition_path.is_empty() {
                member.definition_path = existing.definition_path.clone();
                member.definition_line = existing.definition_line;
            }
            if member.definition_range == AdminSourceRange::default() {
                member.definition_range = existing.definition_range.clone();
            }
            if member.nested_members.is_empty() && !member.nested_complete {
                member.nested_members = existing.nested_members.clone();
                member.nested_complete = existing.nested_complete;
            }
            result[index] = member;
            continue;
        }
        positions.insert(member.name.clone(), result.len());
        result.push(member);
    }
    result.sort_by(|left, right| left.name.cmp(&right.name));
    result
}

pub(crate) fn merge_known_twig_vue_members(
    base: &[TwigVueMember],
    overlay: &[TwigVueMember],
) -> Vec<TwigVueMember> {
    let known: HashSet<&str> = base.iter().map(|member| member.name.as_str()).collect();
    let filtered: Vec<TwigVueMember> = overlay
        .iter()
        .filter(|member| known.contains(member.name.as_str()))
        .cloned()
        .collect();
    merge_twig_vue_members(base, &filtered)
}
