//! Semantic relationships between types: subtyping, PHP assignability,
//! joins, narrowing, and exclusion.

use std::collections::{HashMap, HashSet};

use smallvec::SmallVec;
use swlsp_textutil::eq_ignore_case;

use crate::kind::Kind;
use crate::ty::{ShapeField, Type, union_impl};

/// Declared template variance of a generic class-like type.
#[derive(Clone, Copy, Debug, Default, PartialEq, Eq, Hash)]
pub enum Variance {
    #[default]
    Invariant,
    Covariant,
    Contravariant,
}

/// Supplies class/interface inheritance without coupling the type algebra to
/// a particular index implementation.
///
/// Generic, supertype, type-alias and callable information are optional
/// capabilities: their default methods behave like a hierarchy that has no
/// such information.
pub trait Hierarchy: Send + Sync {
    fn is_subtype_of(&self, candidate: &str, target: &str) -> bool;

    /// Declared template variance; hierarchies without generic information
    /// use safe invariance.
    fn template_variance(&self, _name: &str, _index: usize) -> Variance {
        Variance::Invariant
    }

    /// Projects `candidate` onto the named supertype, resolving generic
    /// arguments through the extends/implements graph.
    fn as_supertype(&self, _candidate: &Type, _target: &str) -> Option<Type> {
        None
    }

    /// Expands nominal PHPDoc alias references.
    fn resolve_type_alias(&self, _value: &Type) -> Option<Type> {
        None
    }

    /// Resolves the effective `__invoke` signature of a named object.
    fn callable_signature(&self, _candidate: &Type) -> Option<Type> {
        None
    }
}

/// Implements semantic relationships between types.
#[derive(Clone, Copy, Default)]
pub struct Relations<'h> {
    pub hierarchy: Option<&'h dyn Hierarchy>,
}

pub(crate) const MAX_PRECISE_LITERAL_UNION_MEMBERS: usize = 8;
pub(crate) const MAX_PRECISE_UNION_MEMBERS: usize = 32;

pub(crate) fn same_object_name(left: &str, right: &str) -> bool {
    eq_ignore_case(
        left.strip_prefix('\\').unwrap_or(left),
        right.strip_prefix('\\').unwrap_or(right),
    )
}

fn trim_quotes(name: &str) -> &str {
    name.trim_matches(['"', '\''])
}

fn is_literal_like(kind: Kind) -> bool {
    matches!(
        kind,
        Kind::Null
            | Kind::True
            | Kind::False
            | Kind::LiteralInt
            | Kind::LiteralFloat
            | Kind::LiteralString
    )
}

impl<'h> Relations<'h> {
    pub fn new(hierarchy: &'h dyn Hierarchy) -> Self {
        Relations {
            hierarchy: Some(hierarchy),
        }
    }

    // ----- subtyping -----

    /// Reports whether every value represented by `candidate` is accepted by
    /// `target`.
    pub fn is_subtype(&self, candidate: &Type, target: &Type) -> bool {
        let candidate = self.resolve_type_alias(candidate);
        let target = self.resolve_type_alias(target);
        let (candidate, target) = (&candidate, &target);
        if let Some(result) = self.composite_subtype(candidate, target) {
            return result;
        }
        match candidate.kind() {
            Kind::True | Kind::False => target.kind() == Kind::Bool,
            Kind::LiteralInt => matches!(target.kind(), Kind::Int | Kind::ArrayKey),
            Kind::LiteralFloat => target.kind() == Kind::Float,
            Kind::LiteralString => matches!(target.kind(), Kind::String | Kind::ArrayKey),
            Kind::Int | Kind::String => target.kind() == Kind::ArrayKey,
            Kind::List | Kind::NonEmptyList => self.list_subtype(candidate, target),
            Kind::Array | Kind::NonEmptyArray => self.array_subtype(candidate, target),
            Kind::Iterable => {
                target.kind() == Kind::Iterable
                    && self.arguments_subtype(candidate.arguments(), target.arguments())
            }
            Kind::Callable => {
                target.kind() == Kind::Callable && self.callable_subtype(candidate, target)
            }
            Kind::ArrayShape => self.array_shape_subtype(candidate, target),
            Kind::ClassString => self.class_string_subtype(candidate, target),
            Kind::Object => self.object_subtype(candidate, target),
            Kind::ObjectShape => {
                target.kind() == Kind::ObjectShape && self.shape_subtype(candidate, target)
                    || target.kind() == Kind::Object && target.name().is_empty()
            }
            _ => false,
        }
    }

    fn composite_subtype(&self, candidate: &Type, target: &Type) -> Option<bool> {
        if candidate == target {
            return Some(true);
        }
        if candidate.is_unknown() || target.is_unknown() {
            return Some(false);
        }
        if candidate.kind() == Kind::Error || target.kind() == Kind::Error {
            return Some(true);
        }
        if candidate.kind() == Kind::Never || target.kind() == Kind::Mixed {
            return Some(true);
        }
        if candidate.kind() == Kind::Conditional {
            let arguments = candidate.arguments();
            return Some(
                arguments.len() == 4
                    && self.is_subtype(&arguments[2], target)
                    && self.is_subtype(&arguments[3], target),
            );
        }
        if candidate.kind() == Kind::Union {
            return Some(
                candidate
                    .arguments()
                    .iter()
                    .all(|member| self.is_subtype(member, target)),
            );
        }
        if target.kind() == Kind::Union {
            if let Some(result) = self.shorthand_subtype_of_union(candidate, target) {
                return Some(result);
            }
            return Some(
                target
                    .arguments()
                    .iter()
                    .any(|member| self.is_subtype(candidate, member)),
            );
        }
        if target.kind() == Kind::Intersection {
            return Some(
                target
                    .arguments()
                    .iter()
                    .all(|member| self.is_subtype(candidate, member)),
            );
        }
        if candidate.kind() == Kind::Intersection {
            return Some(
                candidate
                    .arguments()
                    .iter()
                    .any(|member| self.is_subtype(member, target)),
            );
        }
        None
    }

    fn shorthand_subtype_of_union(&self, candidate: &Type, target: &Type) -> Option<bool> {
        match candidate.kind() {
            Kind::ArrayKey => {
                Some(self.is_subtype(&Type::INT, target) && self.is_subtype(&Type::STRING, target))
            }
            Kind::Bool => {
                Some(self.is_subtype(&Type::TRUE, target) && self.is_subtype(&Type::FALSE, target))
            }
            Kind::Iterable => {
                let arguments = candidate.arguments();
                if arguments.len() != 2 {
                    return Some(false);
                }
                // PHP's iterable declaration is exactly array|Traversable.
                Some(
                    self.is_subtype(
                        &Type::array(arguments[0].clone(), arguments[1].clone()),
                        target,
                    ) && self.is_subtype(&Type::named("Traversable", arguments.to_vec()), target),
                )
            }
            _ => None,
        }
    }

    fn list_subtype(&self, candidate: &Type, target: &Type) -> bool {
        let non_empty = candidate.kind() == Kind::NonEmptyList;
        if target.kind() == Kind::List || non_empty && target.kind() == Kind::NonEmptyList {
            return self.arguments_subtype(candidate.arguments(), target.arguments());
        }
        if target.kind() == Kind::Array || non_empty && target.kind() == Kind::NonEmptyArray {
            let candidate_args = candidate.arguments();
            let target_args = target.arguments();
            return candidate_args.len() == 1
                && target_args.len() == 2
                && self.is_subtype(&Type::INT, &target_args[0])
                && self.is_subtype(&candidate_args[0], &target_args[1]);
        }
        target.kind() == Kind::Iterable
            && self.iterable_arguments_subtype(
                &Type::INT,
                candidate.arguments(),
                target.arguments(),
            )
    }

    fn array_subtype(&self, candidate: &Type, target: &Type) -> bool {
        let candidate_args = candidate.arguments();
        if candidate.kind() == Kind::Array
            && candidate_args.len() == 2
            && candidate_args[1].kind() == Kind::Never
        {
            return self.empty_array_subtype(target);
        }
        if target.kind() == Kind::Array
            || candidate.kind() == Kind::NonEmptyArray && target.kind() == Kind::NonEmptyArray
        {
            return self.arguments_subtype(candidate_args, target.arguments());
        }
        if target.kind() != Kind::Iterable {
            return false;
        }
        let target_args = target.arguments();
        candidate_args.len() == 2
            && target_args.len() == 2
            && self.is_subtype(&candidate_args[0], &target_args[0])
            && self.is_subtype(&candidate_args[1], &target_args[1])
    }

    fn empty_array_subtype(&self, target: &Type) -> bool {
        match target.kind() {
            Kind::Array | Kind::List | Kind::Iterable => true,
            Kind::ArrayShape => target.fields().iter().all(|field| field.optional),
            _ => false,
        }
    }

    fn array_shape_subtype(&self, candidate: &Type, target: &Type) -> bool {
        match target.kind() {
            Kind::Array | Kind::Iterable | Kind::NonEmptyArray => {
                let (key, value) = self.shape_iterable_types(candidate);
                let target_args = target.arguments();
                (target.kind() != Kind::NonEmptyArray || array_shape_has_required_field(candidate))
                    && target_args.len() == 2
                    && self.is_subtype(&key, &target_args[0])
                    && self.is_subtype(&value, &target_args[1])
            }
            Kind::List => self.array_shape_list_subtype(candidate, target),
            Kind::NonEmptyList => {
                self.array_shape_list_subtype(candidate, target)
                    && array_shape_known_non_empty(candidate)
            }
            Kind::ArrayShape => self.shape_subtype(candidate, target),
            _ => false,
        }
    }

    fn class_string_subtype(&self, candidate: &Type, target: &Type) -> bool {
        if matches!(target.kind(), Kind::String | Kind::ArrayKey) {
            return true;
        }
        target.kind() == Kind::ClassString
            && self.arguments_subtype(candidate.arguments(), target.arguments())
    }

    fn object_subtype(&self, candidate: &Type, target: &Type) -> bool {
        if target.kind() == Kind::Callable {
            let Some(hierarchy) = self.hierarchy else {
                return false;
            };
            return hierarchy
                .callable_signature(candidate)
                .is_some_and(|signature| self.callable_subtype(&signature, target));
        }
        if target.kind() == Kind::Iterable {
            return self.object_iterable_subtype(candidate, target);
        }
        if target.kind() != Kind::Object {
            return false;
        }
        if target.name().is_empty() {
            return true;
        }
        if candidate.name().is_empty() {
            return false;
        }
        if same_object_name(candidate.name(), target.name()) {
            return self.object_arguments_subtype(
                target.name(),
                candidate.arguments(),
                target.arguments(),
            );
        }
        if let Some(hierarchy) = self.hierarchy
            && let Some(projected) = hierarchy.as_supertype(candidate, target.name())
        {
            return self.is_subtype(&projected, target);
        }
        if !target.arguments().is_empty() {
            return false;
        }
        self.hierarchy
            .is_some_and(|hierarchy| hierarchy.is_subtype_of(candidate.name(), target.name()))
    }

    fn object_iterable_subtype(&self, candidate: &Type, target: &Type) -> bool {
        if !same_object_name(candidate.name(), "Traversable")
            && !self
                .hierarchy
                .is_some_and(|hierarchy| hierarchy.is_subtype_of(candidate.name(), "Traversable"))
        {
            return false;
        }
        let target_args = target.arguments();
        if target_args.len() != 2 {
            return false;
        }
        // What the object yields is what its class binds to Traversable
        // through the extends/implements graph (an IteratorAggregate, a
        // collection). A class that binds nothing yields unknown values.
        let projected = self
            .hierarchy
            .and_then(|hierarchy| hierarchy.as_supertype(candidate, "Traversable"));
        let (key, value) = match projected.as_ref().map(Type::arguments) {
            Some([key, value]) => (key, value),
            _ => (&Type::ARRAY_KEY, &Type::MIXED),
        };
        self.is_subtype(key, &target_args[0]) && self.is_subtype(value, &target_args[1])
    }

    // ----- assignability -----

    /// Subtyping plus PHP's safe parameter/return widening rules.
    pub fn is_assignable_to(&self, value: &Type, target: &Type) -> bool {
        let value = self.resolve_type_alias(value);
        let target = self.resolve_type_alias(target);
        let (value, target) = (&value, &target);
        if self.is_subtype(value, target) {
            return true;
        }
        if value.kind() == Kind::ArrayKey && matches!(target.kind(), Kind::Int | Kind::String) {
            // PHPStan models array-key as a benevolent int|string union.
            return true;
        }
        if target.kind() == Kind::Callable && possibly_callable(value) {
            // Strings and arrays may be callables at runtime; stay
            // conservative.
            return true;
        }
        if value.kind() == Kind::Union {
            return value
                .arguments()
                .iter()
                .all(|alternative| self.is_assignable_to(alternative, target))
                && value.argument_count() > 0;
        }
        if value.kind() == Kind::Conditional {
            let arguments = value.arguments();
            return arguments.len() == 4
                && self.is_assignable_to(&arguments[2], target)
                && self.is_assignable_to(&arguments[3], target);
        }
        if target.kind() == Kind::Conditional {
            let arguments = target.arguments();
            return arguments.len() == 4
                && self.is_assignable_to(value, &arguments[2])
                && self.is_assignable_to(value, &arguments[3]);
        }
        if value.kind() == Kind::ClassString
            && target.kind() == Kind::ClassString
            && value.argument_count() == 1
            && target.argument_count() == 1
        {
            // class-string carries PHPDoc-only generics like an ordinary
            // object.
            return self.is_assignable_to(&value.arguments()[0], &target.arguments()[0]);
        }
        if self.container_assignable(value, target) {
            return true;
        }
        if value.kind() == Kind::Object
            && target.kind() == Kind::Object
            && same_object_name(value.name(), target.name())
            && value.argument_count() == 0
            && target.argument_count() > 0
        {
            // A raw native declaration carries unknown type arguments.
            return true;
        }
        if target.kind() == Kind::Union
            && target
                .arguments()
                .iter()
                .any(|alternative| self.is_assignable_to(value, alternative))
        {
            return true;
        }
        if value.kind() == Kind::Array
            && target.kind() == Kind::Union
            && self.array_assignable_to_container_union(value, target)
        {
            return true;
        }
        if matches!(value.kind(), Kind::Int | Kind::LiteralInt) {
            return target.kind() == Kind::Float;
        }
        false
    }

    fn container_assignable(&self, value: &Type, target: &Type) -> bool {
        match value.kind() {
            Kind::List | Kind::NonEmptyList => {
                if value.argument_count() != 1 {
                    return false;
                }
                let element = &value.arguments()[0];
                match target.kind() {
                    Kind::List => {
                        target.argument_count() == 1
                            && self.is_assignable_to(element, &target.arguments()[0])
                    }
                    Kind::NonEmptyList => {
                        value.kind() == Kind::NonEmptyList
                            && target.argument_count() == 1
                            && self.is_assignable_to(element, &target.arguments()[0])
                    }
                    Kind::Array | Kind::Iterable => {
                        target.argument_count() == 2
                            && self.is_assignable_to(&Type::INT, &target.arguments()[0])
                            && self.is_assignable_to(element, &target.arguments()[1])
                    }
                    _ => false,
                }
            }
            Kind::Array | Kind::NonEmptyArray => {
                if value.argument_count() != 2 {
                    return false;
                }
                let both = || {
                    target.argument_count() == 2
                        && self.is_assignable_to(&value.arguments()[0], &target.arguments()[0])
                        && self.is_assignable_to(&value.arguments()[1], &target.arguments()[1])
                };
                match target.kind() {
                    Kind::Array | Kind::Iterable => both(),
                    Kind::NonEmptyArray => value.kind() == Kind::NonEmptyArray && both(),
                    _ => false,
                }
            }
            Kind::ArrayShape => {
                if target.kind() == Kind::ArrayShape {
                    return self.shape_assignable(value, target);
                }
                if matches!(
                    target.kind(),
                    Kind::Array | Kind::Iterable | Kind::NonEmptyArray
                ) {
                    if target.argument_count() != 2 {
                        return false;
                    }
                    if target.kind() == Kind::NonEmptyArray
                        && !array_shape_has_required_field(value)
                    {
                        return false;
                    }
                    let (key, element) = self.shape_iterable_types(value);
                    return self.is_assignable_to(&key, &target.arguments()[0])
                        && self.is_assignable_to(&element, &target.arguments()[1]);
                }
                false
            }
            _ => false,
        }
    }

    fn shape_assignable(&self, value: &Type, target: &Type) -> bool {
        self.shape_relation(value, target, |actual, expected| {
            self.is_assignable_to(actual, expected)
        })
    }

    fn shape_subtype(&self, candidate: &Type, target: &Type) -> bool {
        self.shape_relation(candidate, target, |actual, expected| {
            self.is_subtype(actual, expected)
        })
    }

    fn shape_relation(
        &self,
        value: &Type,
        target: &Type,
        related: impl Fn(&Type, &Type) -> bool,
    ) -> bool {
        // A later duplicate key replaces the earlier field.
        let actual_fields: HashMap<&str, &ShapeField> = value
            .fields()
            .iter()
            .map(|field| (trim_quotes(&field.name), field))
            .collect();
        for expected in target.fields() {
            let Some(actual) = actual_fields.get(trim_quotes(&expected.name)) else {
                if expected.optional {
                    continue;
                }
                return false;
            };
            if !expected.optional && actual.optional {
                return false;
            }
            if !related(&actual.ty, &expected.ty) {
                return false;
            }
        }
        true
    }

    fn resolve_type_alias(&self, value: &Type) -> Type {
        let Some(hierarchy) = self.hierarchy else {
            return value.clone();
        };
        let mut value = value.clone();
        let mut seen: Option<HashSet<String>> = None;
        for _ in 0..16 {
            let Some(resolved) = hierarchy.resolve_type_alias(&value) else {
                return value;
            };
            if resolved.is_unknown() || resolved == value {
                return value;
            }
            let key = value.key().into_owned();
            if !seen.get_or_insert_with(HashSet::new).insert(key) {
                return value;
            }
            value = resolved;
        }
        value
    }

    fn array_assignable_to_container_union(&self, value: &Type, target: &Type) -> bool {
        let value_args = value.arguments();
        if value_args.len() != 2 {
            return false;
        }
        let array_key_members = [Type::INT, Type::STRING];
        let keys: &[Type] = match value_args[0].kind() {
            Kind::ArrayKey => &array_key_members,
            Kind::Union => value_args[0].arguments(),
            _ => &value_args[..1],
        };
        keys.iter().all(|key| {
            target
                .arguments()
                .iter()
                .any(|alternative| match alternative.kind() {
                    Kind::List => {
                        let arguments = alternative.arguments();
                        arguments.len() == 1
                            && self.is_subtype(key, &Type::INT)
                            && self.has_assignable_alternative(&value_args[1], &arguments[0])
                    }
                    Kind::Array => {
                        let arguments = alternative.arguments();
                        arguments.len() == 2
                            && self.is_subtype(key, &arguments[0])
                            && self.has_assignable_alternative(&value_args[1], &arguments[1])
                    }
                    _ => false,
                })
        })
    }

    fn has_assignable_alternative(&self, value: &Type, target: &Type) -> bool {
        if value.kind() != Kind::Union {
            return self.is_assignable_to(value, target);
        }
        value
            .arguments()
            .iter()
            .any(|alternative| self.is_assignable_to(alternative, target))
    }

    // ----- join / narrow / without -----

    /// The least upper bound used by flow analysis, widening very large
    /// unions.
    pub fn join(&self, left: &Type, right: &Type) -> Type {
        if left.is_unknown() || right.is_unknown() {
            return Type::UNKNOWN;
        }
        if let Some(joined) = self.join_array_shape_branches(left, right) {
            return joined;
        }
        if self.is_subtype(left, right) {
            return right.clone();
        }
        if self.is_subtype(right, left) {
            return left.clone();
        }
        let joined = Type::union([left.clone(), right.clone()]);
        if joined.kind() == Kind::Union
            && (joined.argument_count() > MAX_PRECISE_UNION_MEMBERS
                || literal_members_too_wide(joined.arguments()))
        {
            return widen_complex_union(&joined);
        }
        joined
    }

    fn join_array_shape_branches(&self, left: &Type, right: &Type) -> Option<Type> {
        let (left_fields, left_open) = branch_array_shape(left)?;
        let (right_fields, right_open) = branch_array_shape(right)?;
        if left_fields.is_empty()
            && !right_fields.is_empty()
            && let Some(element) = self.array_shape_list_element(right_fields, right_open)
        {
            return Some(Type::list(element));
        }
        if right_fields.is_empty()
            && !left_fields.is_empty()
            && let Some(element) = self.array_shape_list_element(left_fields, left_open)
        {
            return Some(Type::list(element));
        }

        let mut fields: Vec<ShapeField> =
            Vec::with_capacity(left_fields.len() + right_fields.len());
        let mut indices: HashMap<&str, usize> = HashMap::new();
        for field in left_fields {
            indices.insert(trim_quotes(&field.name), fields.len());
            let mut field = field.clone();
            field.optional = field.optional || right_fields.is_empty();
            fields.push(field);
        }
        for field in right_fields {
            let name = trim_quotes(&field.name);
            if let Some(&index) = indices.get(name) {
                fields[index].ty = self.join(&fields[index].ty, &field.ty);
                fields[index].optional = fields[index].optional || field.optional;
                continue;
            }
            let mut field = field.clone();
            field.optional = true;
            indices.insert(name, fields.len());
            fields.push(field);
        }
        if !left_fields.is_empty() && !right_fields.is_empty() {
            let right_names: HashSet<&str> = right_fields
                .iter()
                .map(|field| trim_quotes(&field.name))
                .collect();
            for field in &mut fields {
                if !right_names.contains(trim_quotes(&field.name)) {
                    field.optional = true;
                }
            }
        }
        Some(Type::array_shape(fields, left_open || right_open))
    }

    fn array_shape_list_element(&self, fields: &[ShapeField], open: bool) -> Option<Type> {
        if open || fields.is_empty() {
            return None;
        }
        let mut element = Type::NEVER;
        for (index, field) in fields.iter().enumerate() {
            if field.optional || trim_quotes(&field.name) != index.to_string() {
                return None;
            }
            element = self.join(&element, &field.ty);
        }
        Some(element)
    }

    /// Applies a positive type constraint.
    pub fn narrow(&self, original: &Type, constraint: &Type) -> Type {
        if original.is_unknown() {
            return constraint.clone();
        }
        if original.kind() == Kind::Iterable
            && constraint.kind() == Kind::Object
            && same_object_name(constraint.name(), "Traversable")
            && original.argument_count() == 2
        {
            return Type::named("Traversable", original.arguments().to_vec());
        }
        if self.is_subtype(original, constraint) {
            return original.clone();
        }
        if self.is_subtype(constraint, original) {
            return constraint.clone();
        }
        if original.kind() == Kind::Union {
            let members: Vec<Type> = original
                .arguments()
                .iter()
                .filter(|member| {
                    self.is_subtype(member, constraint) || self.is_subtype(constraint, member)
                })
                .map(|member| self.narrow(member, constraint))
                .collect();
            if members.is_empty() {
                return Type::NEVER;
            }
            return Type::union(members);
        }
        Type::intersection([original.clone(), constraint.clone()])
    }

    /// Removes a proven-negative type from a union.
    pub fn without(&self, original: &Type, excluded: &Type) -> Type {
        if original.kind() == Kind::Iterable
            && excluded.kind() == Kind::Object
            && same_object_name(excluded.name(), "Traversable")
            && original.argument_count() == 2
        {
            return Type::array(original.argument(0), original.argument(1));
        }
        if original.kind() != Kind::Union {
            if self.is_subtype(original, excluded) {
                return Type::NEVER;
            }
            return original.clone();
        }
        Type::union(
            original
                .arguments()
                .iter()
                .filter(|member| !self.is_subtype(member, excluded))
                .cloned()
                .collect::<Vec<_>>(),
        )
    }

    // ----- argument helpers -----

    fn arguments_subtype(&self, candidate: &[Type], target: &[Type]) -> bool {
        if target.is_empty() {
            return true;
        }
        candidate.len() == target.len()
            && candidate
                .iter()
                .zip(target)
                .all(|(candidate, target)| self.is_subtype(candidate, target))
    }

    fn object_arguments_subtype(&self, name: &str, candidate: &[Type], target: &[Type]) -> bool {
        if target.is_empty() {
            return true;
        }
        if candidate.len() != target.len() {
            return false;
        }
        for (index, (candidate, target)) in candidate.iter().zip(target).enumerate() {
            let variance = self.hierarchy.map_or(Variance::Invariant, |hierarchy| {
                hierarchy.template_variance(name, index)
            });
            let compatible = match variance {
                Variance::Covariant => self.is_subtype(candidate, target),
                Variance::Contravariant => self.is_subtype(target, candidate),
                // Invariance requires semantic equivalence, not identical
                // syntax, and raw generic arguments are not proof of
                // incompatibility.
                Variance::Invariant => {
                    self.is_assignable_to(candidate, target)
                        && self.is_assignable_to(target, candidate)
                }
            };
            if !compatible {
                return false;
            }
        }
        true
    }

    fn iterable_arguments_subtype(&self, key: &Type, candidate: &[Type], target: &[Type]) -> bool {
        candidate.len() == 1
            && target.len() == 2
            && self.is_subtype(key, &target[0])
            && self.is_subtype(&candidate[0], &target[1])
    }

    fn callable_subtype(&self, candidate: &Type, target: &Type) -> bool {
        let candidate_parameters = candidate.parameters();
        let target_parameters = target.parameters();
        if target_parameters.is_empty() && target.result().kind() == Kind::Mixed {
            return true;
        }
        let common = candidate_parameters.len().min(target_parameters.len());
        for index in 0..common {
            let candidate_parameter = &candidate_parameters[index];
            let target_parameter = &target_parameters[index];
            if target_parameter.optional
                && !candidate_parameter.optional
                && !candidate_parameter.variadic
            {
                return false;
            }
            if target_parameter.variadic && !candidate_parameter.variadic {
                return false;
            }
            if target_parameter.by_reference != candidate_parameter.by_reference {
                return false;
            }
            // Parameters are contravariant.
            if !self.is_subtype(&target_parameter.ty, &candidate_parameter.ty) {
                return false;
            }
        }
        if candidate_parameters[common..]
            .iter()
            .any(|parameter| !parameter.optional && !parameter.variadic)
        {
            return false;
        }
        self.is_subtype(candidate.result(), target.result())
    }

    fn shape_iterable_types(&self, shape: &Type) -> (Type, Type) {
        let mut keys = Joiner::new(*self, Type::NEVER);
        let mut values = Joiner::new(*self, Type::NEVER);
        for field in shape.fields() {
            let name = trim_quotes(&field.name);
            let key = if name.parse::<i64>().is_ok() {
                Type::literal_int(name)
            } else {
                Type::literal_string(name)
            };
            keys.add(key);
            values.add(field.ty.clone());
        }
        (keys.value(), values.value())
    }

    fn array_shape_list_subtype(&self, candidate: &Type, target: &Type) -> bool {
        if candidate.is_open_shape() || target.arguments().len() != 1 {
            return false;
        }
        let fields: HashMap<&str, &ShapeField> = candidate
            .fields()
            .iter()
            .map(|field| (trim_quotes(&field.name), field))
            .collect();
        let mut optional = false;
        for index in 0..fields.len() {
            let Some(field) = fields.get(index.to_string().as_str()) else {
                return false;
            };
            if !self.is_subtype(&field.ty, &target.arguments()[0]) {
                return false;
            }
            if field.optional {
                optional = true;
            } else if optional {
                // Required numeric fields after an optional gap form a sparse
                // array.
                return false;
            }
        }
        true
    }
}

fn array_shape_known_non_empty(candidate: &Type) -> bool {
    candidate
        .fields()
        .iter()
        .any(|field| trim_quotes(&field.name) == "0" && !field.optional)
}

fn array_shape_has_required_field(candidate: &Type) -> bool {
    candidate.fields().iter().any(|field| !field.optional)
}

fn possibly_callable(value: &Type) -> bool {
    match value.kind() {
        Kind::String
        | Kind::LiteralString
        | Kind::ClassString
        | Kind::Array
        | Kind::NonEmptyArray
        | Kind::List
        | Kind::NonEmptyList => true,
        Kind::Object => value.name().is_empty(),
        Kind::ArrayShape => {
            if value.is_open_shape() {
                return true;
            }
            let fields = value.fields();
            if fields.len() != 2 {
                return false;
            }
            if trim_quotes(&fields[0].name) != "0"
                || trim_quotes(&fields[1].name) != "1"
                || fields[0].optional
                || fields[1].optional
            {
                return false;
            }
            let first_possible = matches!(
                fields[0].ty.kind(),
                Kind::Object | Kind::String | Kind::LiteralString | Kind::ClassString
            );
            let second_possible = matches!(fields[1].ty.kind(), Kind::String | Kind::LiteralString);
            first_possible && second_possible
        }
        _ => false,
    }
}

fn branch_array_shape(value: &Type) -> Option<(&[ShapeField], bool)> {
    if value.kind() == Kind::ArrayShape {
        return Some((value.fields(), value.is_open_shape()));
    }
    if value.kind() == Kind::Array
        && value.argument_count() == 2
        && value.arguments()[1].kind() == Kind::Never
    {
        return Some((&[], false));
    }
    None
}

pub(crate) fn literal_members_too_wide(members: &[Type]) -> bool {
    members.len() > MAX_PRECISE_LITERAL_UNION_MEMBERS
        && members.iter().all(|member| is_literal_like(member.kind()))
}

pub(crate) fn widen_complex_union(value: &Type) -> Type {
    if value.kind() != Kind::Union {
        return widen_type(value);
    }
    Type::union(value.arguments().iter().map(widen_type).collect::<Vec<_>>())
}

fn widen_type(value: &Type) -> Type {
    match value.kind() {
        Kind::True | Kind::False => Type::BOOL,
        Kind::LiteralInt => Type::INT,
        Kind::LiteralFloat => Type::FLOAT,
        Kind::LiteralString => Type::STRING,
        Kind::Object | Kind::ObjectShape | Kind::SelfType | Kind::Static | Kind::Parent => {
            Type::OBJECT
        }
        Kind::Array | Kind::ArrayShape => Type::ARRAY,
        Kind::NonEmptyArray => Type::NON_EMPTY_ARRAY,
        Kind::List => Type::LIST,
        Kind::NonEmptyList => Type::NON_EMPTY_LIST,
        Kind::Iterable => Type::ITERABLE,
        Kind::Callable => Type::CALLABLE,
        Kind::ClassString => Type::CLASS_STRING,
        Kind::Template | Kind::Intersection => Type::MIXED,
        _ => value.clone(),
    }
}

/// Incrementally joins a sequence while deferring canonical-text
/// construction until [`Joiner::value`].
pub struct Joiner<'h> {
    relations: Relations<'h>,
    value: Type,
    members: SmallVec<[Type; MAX_PRECISE_LITERAL_UNION_MEMBERS]>,
    union: bool,
}

impl<'h> Joiner<'h> {
    /// Starts an incremental join at `initial`.
    pub fn new(relations: Relations<'h>, initial: Type) -> Self {
        Joiner {
            relations,
            value: initial,
            members: SmallVec::new(),
            union: false,
        }
    }

    /// Joins `value` into the accumulated type.
    pub fn add(&mut self, value: Type) {
        if !self.union {
            if self.value.is_unknown() || value.is_unknown() {
                self.value = Type::UNKNOWN;
            } else if self.relations.is_subtype(&self.value, &value) {
                self.value = value;
            } else if !self.relations.is_subtype(&value, &self.value) {
                self.union = true;
                let current = std::mem::take(&mut self.value);
                self.append_members(current);
                self.append_members(value);
                self.widen_if_needed();
            }
            return;
        }
        if value.is_unknown() {
            self.reset_members();
            self.value = Type::UNKNOWN;
            return;
        }
        if self.members_subtype(&value) {
            self.reset_members();
            self.value = value;
            return;
        }
        if self.type_subtype_members(&value) {
            return;
        }
        self.append_members(value);
        self.widen_if_needed();
    }

    /// Finishes and returns the canonical accumulated type.
    pub fn value(&mut self) -> Type {
        if self.union {
            self.value = Type::union(std::mem::take(&mut self.members));
            self.reset_members();
        }
        self.value.clone()
    }

    #[cfg(test)]
    pub(crate) fn member_count(&self) -> usize {
        self.members.len()
    }

    #[cfg(test)]
    pub(crate) fn pending_value(&self) -> &Type {
        &self.value
    }

    fn members_subtype(&self, target: &Type) -> bool {
        self.members
            .iter()
            .all(|member| self.relations.is_subtype(member, target))
    }

    fn type_subtype_members(&self, candidate: &Type) -> bool {
        if candidate.kind() == Kind::Union {
            return candidate
                .arguments()
                .iter()
                .all(|member| self.type_subtype_members(member));
        }
        self.members
            .iter()
            .any(|member| self.relations.is_subtype(candidate, member))
    }

    fn append_members(&mut self, value: Type) {
        if value.kind() == Kind::Union {
            for member in value.arguments() {
                self.append_member(member.clone());
            }
            return;
        }
        self.append_member(value);
    }

    fn append_member(&mut self, value: Type) {
        let mut has_bool = false;
        for member in &self.members {
            if *member == value {
                return;
            }
            if member.kind() == Kind::Bool {
                has_bool = true;
            }
        }
        if has_bool && matches!(value.kind(), Kind::True | Kind::False) {
            return;
        }
        if value.kind() == Kind::Bool {
            self.members
                .retain(|member| !matches!(member.kind(), Kind::True | Kind::False));
        }
        self.members.push(value);
    }

    fn widen_if_needed(&mut self) {
        if self.members.len() <= MAX_PRECISE_UNION_MEMBERS
            && !literal_members_too_wide(&self.members)
        {
            return;
        }
        let members = std::mem::take(&mut self.members);
        self.value = widen_complex_union(&union_impl(members, false));
        self.reset_members();
    }

    fn reset_members(&mut self) {
        self.members.clear();
        self.union = false;
    }
}
