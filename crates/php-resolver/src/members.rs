//! Effective member lookup across traits, parents, interfaces, trait aliases,
//! and class templates (port of `resolver/members.go`).

use rustc_hash::{FxHashMap, FxHashSet};
use swlsp_php_semantic::{
    Snapshot, Str, Symbol, SymbolId, SymbolKind, SymbolView, TemplateParameter,
};
use swlsp_php_types::{Kind, Type, equal_fold, go_to_lower, substitute};

/// Template name to bound type.
pub type TemplateBindings = FxHashMap<Str, Type>;

/// Resolves effective members of receiver types over one snapshot.
#[derive(Clone, Copy, Default)]
pub struct MemberResolver<'a> {
    pub snapshot: Option<&'a Snapshot>,
}

/// A member specialized for its receiver. `ty` is the property/constant type
/// or the method return type.
#[derive(Clone, Debug, PartialEq)]
pub struct ResolvedMember {
    pub symbol: Symbol,
    pub ty: Type,
}

struct NameTracker {
    named: bool,
    matched: bool,
    mixed_kinds: bool,
    seen: FxHashSet<String>,
}

impl NameTracker {
    fn new(name: &str, mixed_kinds: bool) -> Self {
        NameTracker {
            named: !name.is_empty(),
            matched: false,
            mixed_kinds,
            seen: FxHashSet::default(),
        }
    }

    fn contains(&self, name: &str, kind: SymbolKind) -> bool {
        if self.named {
            return self.matched;
        }
        self.seen
            .contains(&member_resolution_key(name, kind, self.mixed_kinds))
    }

    fn add(&mut self, name: &str, kind: SymbolKind) -> bool {
        if self.named {
            if self.matched {
                return false;
            }
            self.matched = true;
            return true;
        }
        self.seen
            .insert(member_resolution_key(name, kind, self.mixed_kinds))
    }
}

/// Mutable traversal state of the materializing member walk.
struct MemberWalk<'w, 'a> {
    seen_names: &'w mut NameTracker,
    visited: &'w mut FxHashSet<&'a str>,
    visit: &'w mut dyn FnMut(ResolvedMember) -> bool,
}

/// Mutable traversal state of the ID-only member walk.
struct IdWalk<'w, 'a> {
    seen_names: &'w mut FxHashSet<String>,
    visited: &'w mut FxHashSet<&'a str>,
    seen_ids: Option<&'w mut FxHashSet<&'a str>>,
    visit: &'w mut dyn FnMut(SymbolView<'a>) -> bool,
}

fn is_composite(receiver: &Type) -> bool {
    matches!(receiver.kind(), Kind::Union | Kind::Intersection)
}

impl<'a> MemberResolver<'a> {
    pub fn new(snapshot: &'a Snapshot) -> Self {
        MemberResolver {
            snapshot: Some(snapshot),
        }
    }

    pub fn methods(&self, receiver: &Type, name: &str) -> Vec<ResolvedMember> {
        self.members(receiver, name, Some(SymbolKind::Method))
    }

    /// Visits effective matching methods without materializing a result.
    /// Returning `false` stops the traversal.
    pub fn visit_methods(
        &self,
        receiver: &Type,
        name: &str,
        mut visit: impl FnMut(ResolvedMember) -> bool,
    ) -> bool {
        if self.snapshot.is_none() {
            return true;
        }
        if is_composite(receiver) {
            for member in self.methods(receiver, name) {
                if !visit(member) {
                    return false;
                }
            }
            return true;
        }
        self.visit_object_members(receiver, name, Some(SymbolKind::Method), &mut visit)
    }

    pub fn properties(&self, receiver: &Type, name: &str) -> Vec<ResolvedMember> {
        self.members(receiver, name, Some(SymbolKind::Property))
    }

    /// Matching class constants followed by enum cases.
    pub fn constants(&self, receiver: &Type, name: &str) -> Vec<ResolvedMember> {
        let mut result = self.members(receiver, name, Some(SymbolKind::ClassConstant));
        result.extend(self.members(receiver, name, Some(SymbolKind::EnumCase)));
        result
    }

    /// Every effective member, including trait method aliases; type aliases
    /// are omitted.
    pub fn all(&self, receiver: &Type) -> Vec<ResolvedMember> {
        self.members(receiver, "", None)
    }

    pub fn method_ids(&self, receiver: &Type, name: &str) -> Vec<SymbolId> {
        self.member_ids(receiver, name, SymbolKind::Method)
    }

    /// Visits effective matching method declarations. Returning `false`
    /// stops the traversal.
    pub fn visit_method_ids(
        &self,
        receiver: &Type,
        name: &str,
        mut visit: impl FnMut(SymbolView<'a>) -> bool,
    ) -> bool {
        self.visit_member_ids(receiver, name, SymbolKind::Method, &mut visit)
    }

    /// The effective methods visible on `receiver`. Overridden declarations
    /// are omitted, just as they are for named member resolution.
    pub fn all_method_ids(&self, receiver: &Type) -> Vec<SymbolId> {
        self.member_ids(receiver, "", SymbolKind::Method)
    }

    pub fn visit_all_method_ids(
        &self,
        receiver: &Type,
        mut visit: impl FnMut(SymbolView<'a>) -> bool,
    ) -> bool {
        self.visit_member_ids(receiver, "", SymbolKind::Method, &mut visit)
    }

    pub fn property_ids(&self, receiver: &Type, name: &str) -> Vec<SymbolId> {
        self.member_ids(receiver, name, SymbolKind::Property)
    }

    pub fn visit_property_ids(
        &self,
        receiver: &Type,
        name: &str,
        mut visit: impl FnMut(SymbolView<'a>) -> bool,
    ) -> bool {
        self.visit_member_ids(receiver, name, SymbolKind::Property, &mut visit)
    }

    pub fn constant_ids(&self, receiver: &Type, name: &str) -> Vec<SymbolId> {
        let mut result = self.member_ids(receiver, name, SymbolKind::ClassConstant);
        result.extend(self.member_ids(receiver, name, SymbolKind::EnumCase));
        result
    }

    /// Visits matching class constants followed by enum cases.
    pub fn visit_constant_ids(
        &self,
        receiver: &Type,
        name: &str,
        mut visit: impl FnMut(SymbolView<'a>) -> bool,
    ) -> bool {
        self.visit_member_ids(receiver, name, SymbolKind::ClassConstant, &mut visit)
            && self.visit_member_ids(receiver, name, SymbolKind::EnumCase, &mut visit)
    }

    pub fn property_types(&self, receiver: &Type, name: &str) -> Vec<Type> {
        self.member_type_values(receiver, name, SymbolKind::Property)
    }

    pub fn constant_types(&self, receiver: &Type, name: &str) -> Vec<Type> {
        let mut result = self.member_type_values(receiver, name, SymbolKind::ClassConstant);
        result.extend(self.member_type_values(receiver, name, SymbolKind::EnumCase));
        result
    }

    // ----- type values -----

    fn member_type_values(&self, receiver: &Type, name: &str, kind: SymbolKind) -> Vec<Type> {
        let Some(snapshot) = self.snapshot else {
            return Vec::new();
        };
        let mut result = Vec::new();
        let mut seen_ids = is_composite(receiver).then(FxHashSet::default);
        Self::append_member_type_values(snapshot, receiver, name, kind, &mut seen_ids, &mut result);
        result
    }

    fn append_member_type_values(
        snapshot: &'a Snapshot,
        receiver: &Type,
        name: &str,
        kind: SymbolKind,
        seen_ids: &mut Option<FxHashSet<&'a str>>,
        result: &mut Vec<Type>,
    ) {
        if is_composite(receiver) {
            for member in receiver.arguments() {
                Self::append_member_type_values(snapshot, member, name, kind, seen_ids, result);
            }
            return;
        }
        if receiver.kind() != Kind::Object || receiver.name().is_empty() {
            return;
        }
        let mut seen_names = FxHashSet::default();
        let mut visited = FxHashSet::default();
        snapshot.visit_class_views(receiver.name(), |class_view| {
            let class = class_view.materialize();
            let templates = bind_class_templates(&class, receiver.arguments());
            Self::collect_class_member_type_values(
                snapshot,
                class_view.id(),
                &class,
                name,
                kind,
                &templates,
                &mut seen_names,
                &mut visited,
                seen_ids,
                result,
            );
            true
        });
    }

    #[allow(clippy::too_many_arguments)]
    fn collect_class_member_type_values(
        snapshot: &'a Snapshot,
        class_id: &'a str,
        class: &Symbol,
        name: &str,
        kind: SymbolKind,
        templates: &TemplateBindings,
        seen_names: &mut FxHashSet<String>,
        visited: &mut FxHashSet<&'a str>,
        seen_ids: &mut Option<FxHashSet<&'a str>>,
        result: &mut Vec<Type>,
    ) {
        if !visited.insert(class_id) {
            return;
        }
        let mut visit_candidate = |candidate_view: SymbolView<'a>| {
            if candidate_view.kind() != kind {
                return true;
            }
            let key = member_resolution_key(candidate_view.name(), kind, false);
            if !seen_names.insert(key) {
                return true;
            }
            if let Some(seen_ids) = seen_ids.as_mut()
                && !seen_ids.insert(candidate_view.id())
            {
                return true;
            }
            let candidate = candidate_view.materialize();
            let value = if candidate.is_function_like() {
                &candidate.return_type
            } else {
                &candidate.ty
            };
            if templates.is_empty() {
                result.push(value.clone());
            } else {
                let mut effective = templates.clone();
                for template in candidate.templates() {
                    effective.remove(&template.name);
                }
                result.push(substitute(value, &effective));
            }
            true
        };
        if name.is_empty() {
            for candidate in snapshot.member_views_of(class_id) {
                visit_candidate(candidate);
            }
        } else {
            snapshot.visit_member_views(class_id, name, visit_candidate);
        }

        for trait_name in class.traits() {
            snapshot.visit_class_views(trait_name, |trait_view| {
                Self::collect_class_member_type_values(
                    snapshot,
                    trait_view.id(),
                    &trait_view.materialize(),
                    name,
                    kind,
                    templates,
                    seen_names,
                    visited,
                    seen_ids,
                    result,
                );
                true
            });
        }
        let parent_types: Vec<&Type> = class
            .extends_types()
            .iter()
            .chain(class.implements_types())
            .collect();
        for parent_name in class.extends().iter().chain(class.implements()) {
            snapshot.visit_class_views(parent_name, |parent_view| {
                let parent = parent_view.materialize();
                let arguments: Vec<Type> = parent_types
                    .iter()
                    .find(|parent_type| equal_fold(parent_type.name(), parent_name))
                    .map(|parent_type| {
                        parent_type
                            .arguments()
                            .iter()
                            .map(|argument| substitute(argument, templates))
                            .collect()
                    })
                    .unwrap_or_default();
                Self::collect_class_member_type_values(
                    snapshot,
                    parent_view.id(),
                    &parent,
                    name,
                    kind,
                    &bind_class_templates(&parent, &arguments),
                    seen_names,
                    visited,
                    seen_ids,
                    result,
                );
                true
            });
        }
    }

    // ----- IDs -----

    fn member_ids(&self, receiver: &Type, name: &str, kind: SymbolKind) -> Vec<SymbolId> {
        let mut result = Vec::new();
        self.visit_member_ids(receiver, name, kind, &mut |view| {
            result.push(view.symbol_id());
            true
        });
        result
    }

    fn visit_member_ids(
        &self,
        receiver: &Type,
        name: &str,
        kind: SymbolKind,
        visit: &mut dyn FnMut(SymbolView<'a>) -> bool,
    ) -> bool {
        let Some(snapshot) = self.snapshot else {
            return true;
        };
        let mut seen_ids = is_composite(receiver).then(FxHashSet::default);
        Self::visit_member_type_ids(snapshot, receiver, name, kind, seen_ids.as_mut(), visit)
    }

    fn visit_member_type_ids(
        snapshot: &'a Snapshot,
        receiver: &Type,
        name: &str,
        kind: SymbolKind,
        mut seen_ids: Option<&mut FxHashSet<&'a str>>,
        visit: &mut dyn FnMut(SymbolView<'a>) -> bool,
    ) -> bool {
        if is_composite(receiver) {
            for member in receiver.arguments() {
                if !Self::visit_member_type_ids(
                    snapshot,
                    member,
                    name,
                    kind,
                    seen_ids.as_deref_mut(),
                    visit,
                ) {
                    return false;
                }
            }
            return true;
        }
        if receiver.kind() != Kind::Object || receiver.name().is_empty() {
            return true;
        }
        let mut seen_names = FxHashSet::default();
        let mut visited = FxHashSet::default();
        let mut walk = IdWalk {
            seen_names: &mut seen_names,
            visited: &mut visited,
            seen_ids,
            visit,
        };
        snapshot.visit_class_views(receiver.name(), |class| {
            Self::visit_class_member_ids(snapshot, class, name, kind, &mut walk)
        })
    }

    fn visit_class_member_ids(
        snapshot: &'a Snapshot,
        class: SymbolView<'a>,
        name: &str,
        kind: SymbolKind,
        walk: &mut IdWalk<'_, 'a>,
    ) -> bool {
        if !walk.visited.insert(class.id()) {
            return true;
        }
        let mut visit_candidate = |candidate: SymbolView<'a>| {
            if candidate.kind() != kind {
                return true;
            }
            if !walk
                .seen_names
                .insert(member_resolution_key(candidate.name(), kind, false))
            {
                return true;
            }
            if let Some(seen_ids) = walk.seen_ids.as_deref_mut()
                && !seen_ids.insert(candidate.id())
            {
                return true;
            }
            (walk.visit)(candidate)
        };
        if name.is_empty() {
            for candidate in snapshot.member_views_of(class.id()) {
                if !visit_candidate(candidate) {
                    return false;
                }
            }
        } else if !snapshot.visit_member_views(class.id(), name, visit_candidate) {
            return false;
        }
        if kind == SymbolKind::Method && !Self::visit_trait_alias_ids(snapshot, class, name, walk) {
            return false;
        }
        let (traits, extends, implements) = class.hierarchy_names();
        for related_names in [traits, extends, implements] {
            for related_name in related_names {
                if !snapshot.visit_class_views(related_name, |related| {
                    Self::visit_class_member_ids(snapshot, related, name, kind, walk)
                }) {
                    return false;
                }
            }
        }
        true
    }

    fn visit_trait_alias_ids(
        snapshot: &'a Snapshot,
        class: SymbolView<'a>,
        name: &str,
        walk: &mut IdWalk<'_, 'a>,
    ) -> bool {
        if name.is_empty() {
            // ID-only enumeration cannot represent two effective names
            // pointing at one declaration. The original trait method is
            // visited by the trait walk; alias names are surfaced by the
            // materialized `all` path instead.
            return true;
        }
        for alias in class.trait_aliases() {
            if !equal_fold(&alias.alias, name) {
                continue;
            }
            let key = member_resolution_key(&alias.alias, SymbolKind::Method, false);
            if !walk.seen_names.insert(key) {
                continue;
            }
            let mut completed = true;
            let mut seen_names = FxHashSet::default();
            let mut visited = FxHashSet::default();
            let mut inner = IdWalk {
                seen_names: &mut seen_names,
                visited: &mut visited,
                seen_ids: walk.seen_ids.as_deref_mut(),
                visit: &mut *walk.visit,
            };
            let all = snapshot.visit_class_views(&alias.trait_name, |trait_view| {
                completed = Self::visit_class_member_ids(
                    snapshot,
                    trait_view,
                    &alias.method,
                    SymbolKind::Method,
                    &mut inner,
                );
                completed
            });
            if !all || !completed {
                return false;
            }
        }
        true
    }

    // ----- materialized members -----

    fn members(
        &self,
        receiver: &Type,
        name: &str,
        kind: Option<SymbolKind>,
    ) -> Vec<ResolvedMember> {
        let Some(snapshot) = self.snapshot else {
            return Vec::new();
        };
        if is_composite(receiver) {
            let mut result: Vec<ResolvedMember> = Vec::new();
            let mut seen: FxHashMap<SymbolId, usize> = FxHashMap::default();
            let relations = snapshot.relations();
            for member_type in receiver.arguments() {
                for member in self.members(member_type, name, kind) {
                    if let Some(&existing) = seen.get(&member.symbol.id) {
                        let target = &mut result[existing];
                        target.ty = relations.join(&target.ty, &member.ty);
                        if member.symbol.is_function_like() {
                            target.symbol.return_type = relations
                                .join(&target.symbol.return_type, &member.symbol.return_type);
                        }
                        continue;
                    }
                    seen.insert(member.symbol.id.clone(), result.len());
                    result.push(member);
                }
            }
            return result;
        }
        if receiver.kind() != Kind::Object || receiver.name().is_empty() {
            return Vec::new();
        }
        let mut result = Vec::new();
        self.visit_object_members(receiver, name, kind, &mut |member| {
            result.push(member);
            true
        });
        result
    }

    fn visit_object_members(
        &self,
        receiver: &Type,
        name: &str,
        kind: Option<SymbolKind>,
        visit: &mut dyn FnMut(ResolvedMember) -> bool,
    ) -> bool {
        let Some(snapshot) = self.snapshot else {
            return true;
        };
        if receiver.kind() != Kind::Object || receiver.name().is_empty() {
            return true;
        }
        let mut seen_names = NameTracker::new(name, kind.is_none());
        let mut visited = FxHashSet::default();
        let mut walk = MemberWalk {
            seen_names: &mut seen_names,
            visited: &mut visited,
            visit,
        };
        snapshot.visit_class_views(receiver.name(), |class_view| {
            let class = class_view.materialize();
            let templates = bind_class_templates(&class, receiver.arguments());
            Self::visit_class_members(
                snapshot,
                class_view.id(),
                &class,
                name,
                kind,
                &templates,
                &mut walk,
            )
        })
    }

    fn visit_class_members(
        snapshot: &'a Snapshot,
        class_id: &'a str,
        class: &Symbol,
        name: &str,
        kind: Option<SymbolKind>,
        templates: &TemplateBindings,
        walk: &mut MemberWalk<'_, 'a>,
    ) -> bool {
        if !walk.visited.insert(class_id) {
            return true;
        }
        let mut visit_candidate = |candidate_view: SymbolView<'a>| {
            let candidate_kind = candidate_view.kind();
            match kind {
                None if candidate_kind == SymbolKind::TypeAlias => return true,
                Some(kind) if candidate_kind != kind => return true,
                _ => {}
            }
            if !walk.seen_names.add(candidate_view.name(), candidate_kind) {
                return true;
            }
            let specialized = specialize_member(candidate_view.materialize(), class, templates);
            let ty = if specialized.is_function_like() {
                specialized.return_type.clone()
            } else {
                specialized.ty.clone()
            };
            (walk.visit)(ResolvedMember {
                symbol: specialized,
                ty,
            })
        };
        if name.is_empty() {
            for candidate in snapshot.member_views_of(class_id) {
                if !visit_candidate(candidate) {
                    return false;
                }
            }
        } else if !snapshot.visit_member_views(class_id, name, visit_candidate) {
            return false;
        }
        if matches!(kind, None | Some(SymbolKind::Method))
            && !Self::visit_trait_alias_members(snapshot, class, name, templates, walk)
        {
            return false;
        }
        for trait_name in class.traits() {
            if !snapshot.visit_class_views(trait_name, |trait_view| {
                Self::visit_class_members(
                    snapshot,
                    trait_view.id(),
                    &trait_view.materialize(),
                    name,
                    kind,
                    templates,
                    walk,
                )
            }) {
                return false;
            }
        }
        Self::visit_related_class_members(
            snapshot,
            class.extends(),
            class.extends_types(),
            name,
            kind,
            templates,
            walk,
        ) && Self::visit_related_class_members(
            snapshot,
            class.implements(),
            class.implements_types(),
            name,
            kind,
            templates,
            walk,
        )
    }

    fn visit_related_class_members(
        snapshot: &'a Snapshot,
        parent_names: &[Str],
        parent_types: &[Type],
        name: &str,
        kind: Option<SymbolKind>,
        templates: &TemplateBindings,
        walk: &mut MemberWalk<'_, 'a>,
    ) -> bool {
        for parent_name in parent_names {
            if !snapshot.visit_class_views(parent_name, |parent_view| {
                let parent = parent_view.materialize();
                let mut arguments = Vec::new();
                if !parent.templates().is_empty()
                    && let Some(parent_type) = parent_types
                        .iter()
                        .find(|parent_type| equal_fold(parent_type.name(), parent_name))
                {
                    arguments = parent_type
                        .arguments()
                        .iter()
                        .map(|argument| substitute(argument, templates))
                        .collect();
                }
                let parent_templates = bind_class_templates(&parent, &arguments);
                Self::visit_class_members(
                    snapshot,
                    parent_view.id(),
                    &parent,
                    name,
                    kind,
                    &parent_templates,
                    walk,
                )
            }) {
                return false;
            }
        }
        true
    }

    fn visit_trait_alias_members(
        snapshot: &'a Snapshot,
        class: &Symbol,
        name: &str,
        templates: &TemplateBindings,
        walk: &mut MemberWalk<'_, 'a>,
    ) -> bool {
        for alias in class.trait_aliases() {
            if !name.is_empty() && !equal_fold(&alias.alias, name) {
                continue;
            }
            if walk.seen_names.contains(&alias.alias, SymbolKind::Method) {
                continue;
            }
            let mut targets = Vec::new();
            let mut target_names = NameTracker::new(&alias.method, false);
            let mut target_visited = walk.visited.clone();
            let mut collect = |member: ResolvedMember| {
                targets.push(member);
                true
            };
            let mut inner = MemberWalk {
                seen_names: &mut target_names,
                visited: &mut target_visited,
                visit: &mut collect,
            };
            snapshot.visit_class_views(&alias.trait_name, |trait_view| {
                Self::visit_class_members(
                    snapshot,
                    trait_view.id(),
                    &trait_view.materialize(),
                    &alias.method,
                    Some(SymbolKind::Method),
                    templates,
                    &mut inner,
                )
            });
            if targets.is_empty() {
                continue;
            }
            walk.seen_names.add(&alias.alias, SymbolKind::Method);
            for target in targets {
                let mut symbol = target.symbol;
                symbol.name = alias.alias.clone();
                symbol.fully_qualified =
                    Str::from(format!("{}::{}", class.fully_qualified, alias.alias));
                symbol.container = class.id.clone();
                if alias.has_visibility {
                    symbol.visibility = alias.visibility;
                }
                if !(walk.visit)(ResolvedMember {
                    symbol,
                    ty: target.ty,
                }) {
                    return false;
                }
            }
        }
        true
    }
}

/// Binds `class`'s templates to `arguments` positionally, falling back to
/// declared defaults.
pub fn bind_class_templates(class: &Symbol, arguments: &[Type]) -> TemplateBindings {
    let class_templates = class.templates();
    let mut result = TemplateBindings::default();
    if class_templates.is_empty() {
        return result;
    }
    result.reserve(class_templates.len());
    for (index, template) in class_templates.iter().enumerate() {
        if let Some(argument) = arguments.get(index) {
            result.insert(template.name.clone(), argument.clone());
        } else if !template.default.is_unknown() {
            result.insert(template.name.clone(), template.default.clone());
        }
    }
    result
}

fn substitute_symbol_types(
    member: &mut Symbol,
    throws: &mut [Type],
    assertions: &mut [swlsp_php_semantic::TypeAssertion],
    map: &TemplateBindings,
) {
    if map.is_empty() {
        return;
    }
    member.ty = substitute(&member.ty, map);
    member.native_type = substitute(&member.native_type, map);
    member.doc_type = substitute(&member.doc_type, map);
    member.return_type = substitute(&member.return_type, map);
    for parameter in &mut member.parameters {
        parameter.ty = substitute(&parameter.ty, map);
        parameter.native_type = substitute(&parameter.native_type, map);
        parameter.doc_type = substitute(&parameter.doc_type, map);
    }
    for value in throws.iter_mut() {
        *value = substitute(value, map);
    }
    for assertion in assertions.iter_mut() {
        assertion.ty = substitute(&assertion.ty, map);
    }
}

fn specialize_member(mut member: Symbol, class: &Symbol, templates: &TemplateBindings) -> Symbol {
    if templates.is_empty() && class.templates().is_empty() {
        // Materialized snapshot data is immutable. When neither the receiver
        // nor its declaring class contributes templates, specialization is an
        // identity operation and nested signature data stays shared.
        return member;
    }
    let mut method_templates = member.templates().to_vec();
    let mut throws = member.throws().to_vec();
    let mut assertions = member.assertions().to_vec();
    let literal_returns = member.literal_returns().to_vec();
    let constant_returns = member.constant_returns().to_vec();

    rename_colliding_method_templates(
        &mut member,
        &mut method_templates,
        &mut throws,
        &mut assertions,
        templates,
    );
    let mut effective = templates.clone();
    for template in &method_templates {
        effective.remove(&template.name);
    }
    substitute_symbol_types(&mut member, &mut throws, &mut assertions, &effective);

    let mut merged = merge_templates(class.templates(), &method_templates);
    for template in &mut merged {
        template.bound = substitute(&template.bound, &effective);
        template.default = substitute(&template.default, &effective);
        if !template_declared_by_method(&method_templates, &template.name)
            && let Some(bound) = templates.get(&template.name)
            && bound.kind() == Kind::Template
            && bound.name() == &*template.name
        {
            // A method called on Generic<T> inside Generic already has T bound
            // by its receiver. Retain the declaration for bound validation,
            // but do not replace that symbolic T with its class-level default
            // merely because no method argument re-infers it.
            template.default = Type::UNKNOWN;
        }
    }
    member.set_signature_extras(
        merged,
        throws,
        assertions,
        literal_returns,
        constant_returns,
    );
    member
}

fn rename_colliding_method_templates(
    member: &mut Symbol,
    member_templates: &mut [TemplateParameter],
    throws: &mut [Type],
    assertions: &mut [swlsp_php_semantic::TypeAssertion],
    class_templates: &TemplateBindings,
) {
    if member_templates.is_empty() || class_templates.is_empty() {
        return;
    }
    let mut occupied: FxHashSet<String> = class_templates
        .keys()
        .map(|name| name.to_string())
        .chain(
            member_templates
                .iter()
                .map(|template| template.name.to_string()),
        )
        .collect();
    let mut renames = TemplateBindings::default();
    for template in member_templates.iter() {
        let collision = class_templates
            .values()
            .any(|value| type_contains_template_named(value, &template.name));
        if !collision {
            continue;
        }
        let base = format!("{}Method", template.name);
        let mut name = base.clone();
        let mut suffix = 2;
        while occupied.contains(&name) {
            name = format!("{base}{suffix}");
            suffix += 1;
        }
        occupied.insert(name.clone());
        renames.insert(template.name.clone(), Type::template(&name));
    }
    if renames.is_empty() {
        return;
    }
    substitute_symbol_types(member, throws, assertions, &renames);
    for template in member_templates.iter_mut() {
        template.bound = substitute(&template.bound, &renames);
        template.default = substitute(&template.default, &renames);
        if let Some(renamed) = renames.get(&template.name) {
            template.name = Str::from(renamed.name());
        }
    }
}

pub(crate) fn type_contains_template_named(value: &Type, name: &str) -> bool {
    match value.kind() {
        Kind::Template => value.name() == name,
        Kind::Callable => {
            value
                .parameters()
                .iter()
                .any(|parameter| type_contains_template_named(&parameter.ty, name))
                || type_contains_template_named(value.result(), name)
        }
        _ => value
            .arguments()
            .iter()
            .any(|argument| type_contains_template_named(argument, name)),
    }
}

fn template_declared_by_method(templates: &[TemplateParameter], name: &str) -> bool {
    templates.iter().any(|template| &*template.name == name)
}

fn merge_templates(
    class_templates: &[TemplateParameter],
    member_templates: &[TemplateParameter],
) -> Vec<TemplateParameter> {
    if class_templates.is_empty() {
        return member_templates.to_vec();
    }
    let mut result = class_templates.to_vec();
    let class_count = result.len();
    for template in member_templates {
        if let Some(existing) = result[..class_count]
            .iter_mut()
            .find(|existing| existing.name == template.name)
        {
            *existing = template.clone();
        } else {
            result.push(template.clone());
        }
    }
    result
}

fn member_resolution_key(name: &str, kind: SymbolKind, mixed_kinds: bool) -> String {
    if kind == SymbolKind::Property {
        if mixed_kinds {
            return format!("${name}");
        }
        return name.to_owned();
    }
    go_to_lower(name)
}
