//! Workspace hierarchy services used by the type relations.

use std::collections::{HashMap, HashSet};

use swlsp_php_types::{
    CallableParameter, Hierarchy, Kind as TypeKind, Relations, Type, Variance, phpdoc_alias_parts,
    substitute,
};
use swlsp_textutil::eq_ignore_case;

use crate::snapshot::Snapshot;
use crate::str::Str;
use crate::symbol::{Flags, Symbol, SymbolKind};
use crate::text::{lower_equals, normalize_symbol_name};
use crate::view::SymbolView;

impl Snapshot {
    /// Type relations backed by this generation's class hierarchy.
    pub fn relations(&self) -> Relations<'_> {
        Relations {
            hierarchy: Some(self),
        }
    }

    /// Whether `candidate` names `target` or one of its (transitive) parent
    /// classes or interfaces. Class names are case-insensitive and
    /// `class_alias` declarations are followed for the target.
    pub fn is_subtype_of(&self, candidate: &str, target: &str) -> bool {
        let target = self.class_alias_canonical_name(target);
        let normalized_target = normalize_symbol_name(&target, false);
        if lower_equals(trim_class(candidate), &normalized_target) {
            return true;
        }
        let mut visited = HashSet::new();
        self.is_subtype_inner(candidate, &normalized_target, &mut visited)
    }

    fn class_alias_canonical_name(&self, name: &str) -> String {
        let Some(mut alias_target) = self.class_alias_target_name(name) else {
            return name.to_owned();
        };
        let mut visited = HashSet::new();
        visited.insert(normalize_symbol_name(name, false));
        loop {
            if !visited.insert(normalize_symbol_name(&alias_target, false)) {
                return alias_target;
            }
            match self.class_alias_target_name(&alias_target) {
                Some(next) => alias_target = next,
                None => return alias_target,
            }
        }
    }

    fn class_alias_target_name(&self, name: &str) -> Option<String> {
        let mut result = None;
        self.visit_class_views(name, |view| {
            if !view.flags().has(Flags::CLASS_ALIAS) {
                return true;
            }
            let (_, extends, _) = view.hierarchy_names();
            if extends.len() == 1 {
                result = Some(extends[0].to_string());
                return false;
            }
            true
        });
        result.filter(|target: &String| !target.is_empty())
    }

    fn is_subtype_inner(
        &self,
        candidate: &str,
        normalized_target: &str,
        visited: &mut HashSet<String>,
    ) -> bool {
        if !visited.insert(normalize_symbol_name(candidate, false)) {
            return false;
        }
        let mut parents: Vec<Str> = Vec::new();
        self.visit_class_views(candidate, |class| {
            let (_, extends, implements) = class.hierarchy_names();
            parents.extend(extends.iter().chain(implements).cloned());
            true
        });
        // Short-circuit per class: a class's parents are checked before the
        // next declaration with the same name.
        for parent in &parents {
            if lower_equals(trim_class(parent), normalized_target)
                || self.is_subtype_inner(parent, normalized_target, visited)
            {
                return true;
            }
        }
        false
    }

    /// The effective `__invoke` contract of an object, following traits and
    /// parents while preserving generic class arguments.
    pub fn callable_signature(&self, candidate: &Type) -> Option<Type> {
        self.callable_signature_inner(candidate, &mut HashSet::new())
    }

    fn callable_signature_inner(
        &self,
        candidate: &Type,
        visited: &mut HashSet<String>,
    ) -> Option<Type> {
        if candidate.kind() != TypeKind::Object || candidate.name().is_empty() {
            return None;
        }
        if !visited.insert(candidate.key().into_owned()) {
            return None;
        }
        let mut classes = Vec::new();
        self.visit_class_views(candidate.name(), |class| {
            classes.push(class);
            true
        });
        for class_view in classes {
            let class = class_view.materialize();
            let templates = class_template_bindings(&class, candidate);
            let mut result = None;
            self.visit_member_views(&class.id, "__invoke", |member_view| {
                let member = member_view.materialize();
                if member.kind != SymbolKind::Method {
                    return true;
                }
                let parameters = member
                    .parameters
                    .iter()
                    .map(|parameter| CallableParameter {
                        name: swlsp_php_types::Name::from(parameter.name.as_str()),
                        ty: substitute(&parameter.ty, &templates),
                        optional: parameter.optional,
                        variadic: parameter.flags.has(Flags::VARIADIC),
                        by_reference: parameter.flags.has(Flags::BY_REFERENCE),
                    })
                    .collect();
                result = Some(Type::callable(
                    parameters,
                    substitute(&member.return_type, &templates),
                ));
                false
            });
            if result.is_some() {
                return result;
            }
            for trait_name in class.traits() {
                if let Some(signature) =
                    self.callable_signature_inner(&Type::named(trait_name, Vec::new()), visited)
                {
                    return Some(signature);
                }
            }
            for parent in class_parent_types(&class) {
                let parent = substitute(&parent, &templates);
                if let Some(signature) = self.callable_signature_inner(&parent, visited) {
                    return Some(signature);
                }
            }
        }
        None
    }

    /// Expands a nominal PHPDoc alias through the declaring class's
    /// synthetic alias member.
    pub fn resolve_type_alias(&self, value: &Type) -> Option<Type> {
        let (class_name, alias) = phpdoc_alias_parts(value)?;
        let mut resolved = None;
        self.visit_class_views(class_name, |class| {
            self.visit_member_views(class.id(), alias, |alias_view| {
                if alias_view.kind() != SymbolKind::TypeAlias {
                    return true;
                }
                let ty = alias_view.materialize().ty;
                if ty.is_unknown() {
                    return true;
                }
                resolved = Some(ty);
                false
            });
            resolved.is_none()
        });
        resolved
    }

    /// Declared variance of a class template parameter.
    pub fn template_variance(&self, name: &str, index: usize) -> Variance {
        let mut result = Variance::Invariant;
        self.visit_class_views(name, |class_view| {
            let class = class_view.materialize();
            let Some(template) = class.templates().get(index) else {
                return true;
            };
            if template.covariant {
                result = Variance::Covariant;
            } else if template.contravariant {
                result = Variance::Contravariant;
            }
            false
        });
        result
    }

    /// Projects `candidate` onto the named supertype, resolving generic
    /// arguments through the extends/implements graph.
    pub fn as_supertype(&self, candidate: &Type, target: &str) -> Option<Type> {
        if candidate.kind() != TypeKind::Object || candidate.name().is_empty() {
            return None;
        }
        self.as_supertype_inner(candidate, target, &mut HashSet::new())
    }

    fn as_supertype_inner(
        &self,
        candidate: &Type,
        target: &str,
        visited: &mut HashSet<String>,
    ) -> Option<Type> {
        let normalized_target = normalize_symbol_name(target, false);
        if lower_equals(trim_class(candidate.name()), &normalized_target) {
            return Some(candidate.clone());
        }
        if !visited.insert(candidate.key().into_owned()) {
            return None;
        }
        let mut classes: Vec<SymbolView<'_>> = Vec::new();
        self.visit_class_views(candidate.name(), |class| {
            classes.push(class);
            true
        });
        for class_view in classes {
            let (extends_types, implements_types) = class_view.hierarchy_types();
            let (_, extends, implements) = class_view.hierarchy_names();
            let parents =
                class_parent_types_from_edges(extends_types, implements_types, extends, implements);
            // Template bindings live in the (possibly lazy) signature payload.
            // Without generic arguments there is nothing to substitute, so the
            // resident hierarchy suffices.
            let needs_templates = candidate.argument_count() > 0
                || parents.iter().any(|parent| parent.argument_count() > 0);
            if !needs_templates {
                for parent in parents {
                    if lower_equals(trim_class(parent.name()), &normalized_target) {
                        return Some(parent);
                    }
                    if let Some(projected) = self.as_supertype_inner(&parent, target, visited) {
                        return Some(projected);
                    }
                }
                continue;
            }
            let class = class_view.materialize();
            let templates = class_template_bindings(&class, candidate);
            for parent in class_parent_types(&class) {
                let parent = substitute(&parent, &templates);
                let parent = inherit_explicit_arguments(&class, candidate, parent);
                if lower_equals(trim_class(parent.name()), &normalized_target) {
                    return Some(parent);
                }
                if let Some(projected) = self.as_supertype_inner(&parent, target, visited) {
                    return Some(projected);
                }
            }
        }
        None
    }
}

fn trim_class(name: &str) -> &str {
    name.strip_prefix('\\').unwrap_or(name)
}

fn class_template_bindings(class: &Symbol, candidate: &Type) -> HashMap<Str, Type> {
    let mut templates = HashMap::new();
    for (index, template) in class.templates().iter().enumerate() {
        if index < candidate.argument_count() {
            templates.insert(template.name.clone(), candidate.argument(index));
        } else if !template.default.is_unknown() {
            templates.insert(template.name.clone(), template.default.clone());
        }
    }
    templates
}

/// Preserves a PHPDoc specialization across non-template bridge classes,
/// e.g. `CollectionSubclass<Element>` where the subclass does not redeclare
/// the template inherited from a generic ancestor.
fn inherit_explicit_arguments(class: &Symbol, candidate: &Type, parent: Type) -> Type {
    if !class.templates().is_empty()
        || candidate.argument_count() == 0
        || parent.kind() != TypeKind::Object
        || parent.name().is_empty()
    {
        return parent;
    }
    let mut arguments = parent.arguments().to_vec();
    if arguments.len() < candidate.argument_count() {
        arguments.resize(candidate.argument_count(), Type::unknown());
    }
    for (index, argument) in candidate.arguments().iter().enumerate() {
        arguments[index] = argument.clone();
    }
    Type::named(parent.name(), arguments)
}

fn class_parent_types(class: &Symbol) -> Vec<Type> {
    class_parent_types_from_edges(
        class.extends_types(),
        class.implements_types(),
        class.extends(),
        class.implements(),
    )
}

fn class_parent_types_from_edges(
    extends_types: &[Type],
    implements_types: &[Type],
    extends: &[Str],
    implements: &[Str],
) -> Vec<Type> {
    let declared: Vec<&Type> = extends_types.iter().chain(implements_types).collect();
    let mut result: Vec<Type> = declared.iter().map(|value| (*value).clone()).collect();
    for name in extends.iter().chain(implements) {
        if !declared
            .iter()
            .any(|value| eq_ignore_case(value.name(), name))
        {
            result.push(Type::named(name, Vec::new()));
        }
    }
    result
}

impl Hierarchy for Snapshot {
    fn is_subtype_of(&self, candidate: &str, target: &str) -> bool {
        Snapshot::is_subtype_of(self, candidate, target)
    }

    fn template_variance(&self, name: &str, index: usize) -> Variance {
        Snapshot::template_variance(self, name, index)
    }

    fn as_supertype(&self, candidate: &Type, target: &str) -> Option<Type> {
        Snapshot::as_supertype(self, candidate, target)
    }

    fn resolve_type_alias(&self, value: &Type) -> Option<Type> {
        Snapshot::resolve_type_alias(self, value)
    }

    fn callable_signature(&self, candidate: &Type) -> Option<Type> {
        Snapshot::callable_signature(self, candidate)
    }
}
