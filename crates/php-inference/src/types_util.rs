//! Type helpers shared by expression and flow analysis (port of the type
//! helpers in `helpers.go`, `expression_array.go` and `builtins.go`).

use swlsp_php_types::{CallableParameter, Joiner, Kind, Relations, ShapeField, Type, go_to_lower};

use crate::syntax::{trim_backslash, trim_quotes};

/// Fluent builders can carry a parent stack in nested generic arguments.
/// Keep enough headroom for valid chains while bounding adversarial graphs.
pub(crate) const MAX_SPECIAL_TYPE_DEPTH: usize = 128;

/// Bounds `self`/`static` substitution cost; see Go's `maxSpecialTypeNodes`.
pub(crate) const MAX_SPECIAL_TYPE_NODES: usize = 4096;

pub(crate) const MAX_INFERRED_TUPLE_FIELDS: usize = 16;

/// Go `strconv.Atoi` success check (optional sign, decimal digits, fits i64).
pub(crate) fn parse_go_int(text: &str) -> Option<i64> {
    let digits = text.strip_prefix(['+', '-']).unwrap_or(text);
    if digits.is_empty() || !digits.bytes().all(|byte| byte.is_ascii_digit()) {
        return None;
    }
    text.parse::<i64>().ok()
}

pub(crate) fn is_int_like(value: &Type) -> bool {
    matches!(value.kind(), Kind::Int | Kind::LiteralInt)
}

pub(crate) fn is_float_like(value: &Type) -> bool {
    matches!(value.kind(), Kind::Float | Kind::LiteralFloat)
}

/// Key and value types produced by iterating `value`.
pub(crate) fn iterable_types(value: &Type, relations: Relations<'_>) -> (Type, Type) {
    match value.kind() {
        Kind::Array | Kind::NonEmptyArray | Kind::Iterable => {
            if value.argument_count() == 2 {
                if value.kind() != Kind::Iterable && value.argument(1).kind() == Kind::Never {
                    return (Type::NEVER, Type::NEVER);
                }
                return (value.argument(0), value.argument(1));
            }
        }
        Kind::List | Kind::NonEmptyList => {
            if value.argument_count() == 1 {
                return (Type::INT, value.argument(0));
            }
        }
        Kind::ArrayShape => return array_shape_iterable_types(value, relations),
        Kind::Union => {
            let mut keys = Joiner::new(relations, Type::NEVER);
            let mut values = Joiner::new(relations, Type::NEVER);
            for member in value.arguments() {
                let (key, element) = iterable_types(member, relations);
                if key.is_unknown() || element.is_unknown() {
                    return (Type::UNKNOWN, Type::UNKNOWN);
                }
                keys.add(key);
                values.add(element);
            }
            return (keys.value(), values.value());
        }
        Kind::Object => {
            if value.argument_count() == 1 {
                let name = go_to_lower(trim_backslash(value.name()));
                if matches!(
                    name.as_str(),
                    "iterator" | "iteratoraggregate" | "traversable"
                ) {
                    return (Type::ARRAY_KEY, value.argument(0));
                }
            }
            if let Some(hierarchy) = relations.hierarchy {
                for target in ["IteratorAggregate", "Traversable"] {
                    let Some(projected) = hierarchy.as_supertype(value, target) else {
                        continue;
                    };
                    match projected.argument_count() {
                        1 => return (Type::ARRAY_KEY, projected.argument(0)),
                        2 => return (projected.argument(0), projected.argument(1)),
                        _ => {}
                    }
                }
            }
        }
        _ => {}
    }
    (Type::UNKNOWN, Type::UNKNOWN)
}

pub(crate) fn array_shape_iterable_types(value: &Type, relations: Relations<'_>) -> (Type, Type) {
    let mut keys = Joiner::new(relations, Type::NEVER);
    let mut values = Joiner::new(relations, Type::NEVER);
    for field in value.fields() {
        let name = trim_quotes(&field.name);
        match parse_go_int(name) {
            Some(numeric) => keys.add(Type::literal_int(&numeric.to_string())),
            None => keys.add(Type::literal_string(name)),
        }
        values.add(field.ty.clone());
    }
    (keys.value(), values.value())
}

pub(crate) fn array_shape_is_list(value: &Type) -> bool {
    if value.kind() != Kind::ArrayShape || value.is_open_shape() {
        return false;
    }
    let fields = value.fields();
    if fields.iter().any(|field| field.optional) {
        return false;
    }
    (0..fields.len()).all(|index| {
        let expected = index.to_string();
        fields
            .iter()
            .any(|field| trim_quotes(&field.name) == expected)
    })
}

pub(crate) fn join_types(relations: Relations<'_>, values: &[Type]) -> Type {
    let Some((first, rest)) = values.split_first() else {
        return Type::UNKNOWN;
    };
    let mut joiner = Joiner::new(relations, first.clone());
    for value in rest {
        joiner.add(value.clone());
    }
    joiner.value()
}

pub(crate) fn resolve_special_type(value: &Type, receiver: &Type, current_class: &Type) -> Type {
    resolve_special_type_at_depth(value, receiver, current_class, 0)
}

fn resolve_special_type_at_depth(
    value: &Type,
    receiver: &Type,
    current_class: &Type,
    depth: usize,
) -> Type {
    if depth >= MAX_SPECIAL_TYPE_DEPTH {
        return Type::UNKNOWN;
    }
    let next = depth + 1;
    let arguments =
        |reject: bool| resolve_special_type_arguments(value, receiver, current_class, next, reject);
    match value.kind() {
        Kind::SelfType => apply_special_type_arguments(current_class, value),
        Kind::Static => apply_special_type_arguments(receiver, value),
        Kind::Parent => Type::UNKNOWN,
        Kind::Union => match arguments(false) {
            (Some(members), _) => Type::union(members),
            _ => value.clone(),
        },
        Kind::Intersection => match arguments(false) {
            (Some(members), _) => Type::intersection(members),
            _ => value.clone(),
        },
        Kind::Object => {
            if value.name().is_empty() {
                return value.clone();
            }
            match arguments(true) {
                // A concrete nested type was truncated by the depth bound.
                (_, false) => Type::UNKNOWN,
                (Some(members), true) => Type::named(value.name(), members),
                (None, true) => value.clone(),
            }
        }
        Kind::Array | Kind::NonEmptyArray | Kind::Iterable => {
            if value.argument_count() != 2 {
                return value.clone();
            }
            match arguments(false) {
                (Some(mut members), _) => {
                    let element = members.pop().unwrap_or_default();
                    let key = members.pop().unwrap_or_default();
                    match value.kind() {
                        Kind::Array => Type::array(key, element),
                        Kind::NonEmptyArray => Type::non_empty_array(key, element),
                        _ => Type::iterable(key, element),
                    }
                }
                _ => value.clone(),
            }
        }
        Kind::List | Kind::NonEmptyList | Kind::ClassString => {
            if value.argument_count() != 1 {
                return value.clone();
            }
            match arguments(false) {
                (Some(mut members), _) => {
                    let element = members.pop().unwrap_or_default();
                    match value.kind() {
                        Kind::List => Type::list(element),
                        Kind::NonEmptyList => Type::non_empty_list(element),
                        _ => Type::class_string(element),
                    }
                }
                _ => value.clone(),
            }
        }
        Kind::Callable => resolve_special_callable_type(value, receiver, current_class, next),
        Kind::ArrayShape | Kind::ObjectShape => {
            resolve_special_shape_type(value, receiver, current_class, next)
        }
        Kind::Conditional => {
            if value.argument_count() != 4 {
                return value.clone();
            }
            match arguments(false) {
                (Some(members), _) => {
                    let mut members = members.into_iter();
                    let mut next = || members.next().unwrap_or_default();
                    let (subject, target, if_true, if_false) = (next(), next(), next(), next());
                    Type::conditional(subject, target, if_true, if_false)
                }
                _ => value.clone(),
            }
        }
        _ => value.clone(),
    }
}

/// Resolved arguments when any changed, and whether the result is valid
/// (false when a concrete argument was truncated and `reject` is set).
fn resolve_special_type_arguments(
    value: &Type,
    receiver: &Type,
    current_class: &Type,
    depth: usize,
    reject_truncated_concrete: bool,
) -> (Option<Vec<Type>>, bool) {
    let originals = value.arguments();
    let mut result: Option<Vec<Type>> = None;
    for (index, original) in originals.iter().enumerate() {
        let resolved = resolve_special_type_at_depth(original, receiver, current_class, depth);
        if reject_truncated_concrete && resolved.is_unknown() && !original.is_unknown() {
            return (None, false);
        }
        if resolved == *original {
            if let Some(result) = result.as_mut() {
                result.push(original.clone());
            }
            continue;
        }
        let result = result.get_or_insert_with(|| {
            let mut copy = Vec::with_capacity(originals.len());
            copy.extend(originals[..index].iter().cloned());
            copy
        });
        result.push(resolved);
    }
    (result, true)
}

fn resolve_special_callable_type(
    value: &Type,
    receiver: &Type,
    current_class: &Type,
    depth: usize,
) -> Type {
    let result = resolve_special_type_at_depth(value.result(), receiver, current_class, depth);
    let result_changed = result != *value.result();
    let mut parameters: Option<Vec<CallableParameter>> = None;
    for (index, parameter) in value.parameters().iter().enumerate() {
        let resolved = resolve_special_type_at_depth(&parameter.ty, receiver, current_class, depth);
        if resolved == parameter.ty {
            continue;
        }
        let parameters = parameters.get_or_insert_with(|| value.parameters().to_vec());
        parameters[index].ty = resolved;
    }
    if parameters.is_none() && !result_changed {
        return value.clone();
    }
    let parameters = parameters.unwrap_or_else(|| value.parameters().to_vec());
    Type::callable(parameters, result)
}

fn resolve_special_shape_type(
    value: &Type,
    receiver: &Type,
    current_class: &Type,
    depth: usize,
) -> Type {
    let mut fields: Option<Vec<ShapeField>> = None;
    for (index, field) in value.fields().iter().enumerate() {
        let resolved = resolve_special_type_at_depth(&field.ty, receiver, current_class, depth);
        if resolved == field.ty {
            continue;
        }
        let fields = fields.get_or_insert_with(|| value.fields().to_vec());
        fields[index].ty = resolved;
    }
    let Some(fields) = fields else {
        return value.clone();
    };
    if value.kind() == Kind::ArrayShape {
        Type::array_shape(fields, value.is_open_shape())
    } else {
        Type::object_shape(fields, value.is_open_shape())
    }
}

/// Substitutes a resolved `self`/`static`, refusing oversized bases.
fn apply_special_type_arguments(base: &Type, special: &Type) -> Type {
    if exceeds_special_type_nodes(base, MAX_SPECIAL_TYPE_NODES) {
        return Type::UNKNOWN;
    }
    if special.argument_count() == 0 || base.kind() != Kind::Object || base.name().is_empty() {
        return base.clone();
    }
    Type::named(base.name(), special.arguments().to_vec())
}

pub(crate) fn exceeds_special_type_nodes(value: &Type, limit: usize) -> bool {
    count_special_type_nodes(value, 0, limit) > limit
}

fn count_special_type_nodes(value: &Type, counted: usize, limit: usize) -> usize {
    let mut counted = counted + 1;
    if counted > limit {
        return counted;
    }
    for argument in value.arguments() {
        counted = count_special_type_nodes(argument, counted, limit);
        if counted > limit {
            return counted;
        }
    }
    for parameter in value.parameters() {
        counted = count_special_type_nodes(&parameter.ty, counted, limit);
        if counted > limit {
            return counted;
        }
    }
    for field in value.fields() {
        counted = count_special_type_nodes(&field.ty, counted, limit);
        if counted > limit {
            return counted;
        }
    }
    if value.kind() == Kind::Callable {
        counted = count_special_type_nodes(value.result(), counted, limit);
    }
    counted
}

/// Caches the last constructed nominal type.
#[derive(Clone, Default)]
pub(crate) struct NamedTypeCache {
    name: String,
    value: Type,
}

impl NamedTypeCache {
    pub(crate) fn type_for(&mut self, name: &str) -> Type {
        if !name.is_empty() && name == self.name {
            return self.value.clone();
        }
        let value = Type::named(name, Vec::new());
        if !name.is_empty() {
            self.name = name.to_owned();
            self.value = value.clone();
        }
        value
    }
}

/// The field name of a literal array key.
pub(crate) fn array_literal_key(value: &Type) -> Option<&str> {
    match value.kind() {
        Kind::LiteralInt | Kind::LiteralString => Some(value.name()),
        _ => None,
    }
}

pub(crate) fn predicate_type(name: &str) -> Type {
    match name {
        "is_string" => Type::STRING,
        "is_int" | "is_integer" | "is_long" => Type::INT,
        "is_float" | "is_double" | "is_real" => Type::FLOAT,
        "is_bool" => Type::BOOL,
        "is_array" => Type::array(Type::MIXED, Type::MIXED),
        "is_callable" => Type::callable(Vec::new(), Type::MIXED),
        "is_iterable" => Type::iterable(Type::MIXED, Type::MIXED),
        "is_object" => Type::OBJECT,
        "is_resource" => Type::RESOURCE,
        "is_null" => Type::NULL,
        _ => Type::UNKNOWN,
    }
}

pub(crate) fn without_falsy_literals(relations: Relations<'_>, value: &Type) -> Type {
    let mut value = value.clone();
    for falsy in [
        Type::NULL,
        Type::FALSE,
        Type::literal_int("0"),
        Type::literal_float("0"),
        Type::literal_string(""),
    ] {
        value = relations.without(&value, &falsy);
    }
    value
}

pub(crate) fn array_type_known_non_empty(value: &Type) -> bool {
    match value.kind() {
        Kind::NonEmptyArray | Kind::NonEmptyList => true,
        Kind::ArrayShape => value.fields().iter().any(|field| !field.optional),
        Kind::Union => {
            value.argument_count() > 0 && value.arguments().iter().all(array_type_known_non_empty)
        }
        _ => false,
    }
}

pub(crate) fn type_definitely_truthy(value: &Type) -> bool {
    match value.kind() {
        Kind::True | Kind::Object | Kind::Resource | Kind::NonEmptyArray | Kind::NonEmptyList => {
            true
        }
        Kind::LiteralString => !value.name().is_empty() && value.name() != "0",
        Kind::LiteralInt | Kind::LiteralFloat => value.name() != "0" && value.name() != "0.0",
        Kind::ArrayShape => array_type_known_non_empty(value),
        Kind::Union | Kind::Intersection => {
            value.argument_count() > 0 && value.arguments().iter().all(type_definitely_truthy)
        }
        _ => false,
    }
}

pub(crate) fn is_array_container_type(value: &Type) -> bool {
    match value.kind() {
        Kind::Array | Kind::NonEmptyArray | Kind::List | Kind::NonEmptyList | Kind::ArrayShape => {
            true
        }
        Kind::Union => {
            value.argument_count() > 0 && value.arguments().iter().all(is_array_container_type)
        }
        _ => false,
    }
}

/// Collects template names referenced by `value`.
pub(crate) fn collect_type_template_names(value: &Type, result: &mut Vec<String>) {
    if value.kind() == Kind::Template {
        result.push(value.name().to_owned());
        return;
    }
    for argument in value.arguments() {
        collect_type_template_names(argument, result);
    }
}

pub(crate) fn callable_result(value: &Type) -> Option<Type> {
    if value.kind() == Kind::Callable {
        return Some(value.result().clone());
    }
    if value.kind() != Kind::Union {
        return None;
    }
    let results: Vec<Type> = value
        .arguments()
        .iter()
        .filter(|alternative| alternative.kind() == Kind::Callable)
        .map(|alternative| alternative.result().clone())
        .collect();
    if results.is_empty() {
        return None;
    }
    Some(Type::union(results))
}

/// `class-string<T>` → `T`, objects as-is; plain strings stay uncertain.
pub(crate) fn dynamic_object_type(value: &Type) -> Type {
    match value.kind() {
        Kind::ClassString if value.argument_count() == 1 => return value.argument(0),
        Kind::Object => return value.clone(),
        Kind::Union => {
            let objects: Vec<Type> = value
                .arguments()
                .iter()
                .map(dynamic_object_type)
                .filter(|object| object.kind() == Kind::Object)
                .collect();
            if !objects.is_empty() {
                return Type::union(objects);
            }
        }
        _ => {}
    }
    Type::UNKNOWN
}

pub(crate) fn regex_callback_matches_type() -> Type {
    Type::array_shape(vec![ShapeField::new("0", Type::STRING)], true)
}
