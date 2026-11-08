//! `.phpstorm.meta.php` return contracts evaluated at the signature boundary
//! (port of `resolver/call_contracts.go`).

use swlsp_php_semantic::{CallReturnContract, CallReturnKind, CallValue, CallValueKind};
use swlsp_php_types::{Joiner, Kind, Relations, Type, equal_fold, go_to_lower};

use crate::signatures::{Argument, shape_iterable_types};

/// Evaluates normalized return contracts against already inferred call
/// arguments, joining the results of every applicable contract. Diagnostics,
/// inference, hover, and signature help consume the same effective result.
pub fn effective_call_return_type(
    relations: Relations<'_>,
    arguments: &[Argument],
    contracts: &[CallReturnContract],
) -> Option<Type> {
    let mut joiner: Option<Joiner<'_>> = None;
    for contract in contracts {
        let Some(value) = effective_contract_type(relations, arguments, contract) else {
            continue;
        };
        match joiner.as_mut() {
            Some(joiner) => joiner.add(value),
            None => joiner = Some(Joiner::new(relations, value)),
        }
    }
    joiner.map(|mut joiner| joiner.value())
}

fn effective_contract_type(
    relations: Relations<'_>,
    arguments: &[Argument],
    contract: &CallReturnContract,
) -> Option<Type> {
    let argument = &arguments.get(usize::from(contract.argument))?.ty;
    if argument.is_unknown() {
        return None;
    }
    match contract.kind {
        CallReturnKind::ArgumentType => Some(argument.clone()),
        CallReturnKind::ArgumentElementType => {
            let (_, element) = iterable_types(argument, relations);
            (!element.is_unknown()).then_some(element)
        }
        CallReturnKind::ArgumentMap => effective_call_map(arguments, contract),
        CallReturnKind::Unknown => None,
    }
}

fn effective_call_map(arguments: &[Argument], contract: &CallReturnContract) -> Option<Type> {
    let argument = &arguments[usize::from(contract.argument)];
    let mut fallback = None;
    for entry in &contract.map {
        if entry.key.kind == CallValueKind::String && entry.key.value.is_empty() {
            fallback = Some(entry);
            continue;
        }
        if value_matches(&entry.key, argument) {
            return map_result(arguments, &entry.result, &argument.ty);
        }
    }
    fallback.and_then(|entry| map_result(arguments, &entry.result, &argument.ty))
}

fn value_matches(value: &CallValue, argument: &Argument) -> bool {
    match value.kind {
        CallValueKind::String => {
            argument.ty.kind() == Kind::LiteralString && argument.ty.name() == &*value.value
        }
        CallValueKind::Number => argument.ty.text() == normalize_expression(&value.expression),
        CallValueKind::ClassConstant => {
            let expected = class_constant_name(&value.expression);
            equal_fold(&expected, argument_class_name(&argument.ty))
        }
        _ => equal_fold(
            &normalize_expression(&value.expression),
            &normalize_expression(&argument.expression),
        ),
    }
}

fn class_constant_name(expression: &str) -> String {
    let normalized = normalize_expression(expression);
    let name = normalized.strip_prefix('\\').unwrap_or(&normalized);
    name.strip_suffix("::class").unwrap_or(name).to_owned()
}

fn map_result(arguments: &[Argument], result: &CallValue, selected: &Type) -> Option<Type> {
    match result.kind {
        CallValueKind::ClassConstant => {
            let name = class_constant_name(&result.expression);
            if !name.is_empty() {
                return Some(Type::named(&name, Vec::new()));
            }
        }
        CallValueKind::String => {
            let pattern = &*result.value;
            if let Some(index) = pattern.strip_prefix('$')
                && let Ok(index) = index.parse::<i64>()
                && let Ok(index) = usize::try_from(index)
                && let Some(argument) = arguments.get(index)
            {
                let name = argument_class_name(&argument.ty);
                if !name.is_empty() {
                    return Some(Type::named(name, Vec::new()));
                }
            }
            if pattern.contains('@') {
                let name = argument_class_name(selected);
                if !name.is_empty() {
                    return Some(Type::named(&pattern.replace('@', name), Vec::new()));
                }
            }
            if !pattern.is_empty() {
                return Some(Type::named(
                    pattern.strip_prefix('\\').unwrap_or(pattern),
                    Vec::new(),
                ));
            }
        }
        _ => {}
    }
    None
}

fn argument_class_name(value: &Type) -> &str {
    let name = match value.kind() {
        Kind::LiteralString | Kind::Object => value.name(),
        Kind::ClassString => match value.arguments() {
            [argument] if argument.kind() == Kind::Object => argument.name(),
            _ => "",
        },
        _ => "",
    };
    name.strip_prefix('\\').unwrap_or(name)
}

/// Key and element types of an iterable value: arrays, lists, shapes,
/// unions of those, `Traversable`-like generics, and objects projected onto
/// `IteratorAggregate`/`Traversable`. `unknown` when not iterable.
pub fn iterable_types(value: &Type, relations: Relations<'_>) -> (Type, Type) {
    match value.kind() {
        Kind::Array | Kind::NonEmptyArray | Kind::Iterable if value.argument_count() == 2 => {
            if value.kind() != Kind::Iterable && value.argument(1).kind() == Kind::Never {
                return (Type::never(), Type::never());
            }
            return (value.argument(0), value.argument(1));
        }
        Kind::List | Kind::NonEmptyList if value.argument_count() == 1 => {
            return (Type::int(), value.argument(0));
        }
        Kind::ArrayShape => return shape_iterable_types(value, relations),
        Kind::Union => {
            let mut keys = Joiner::new(relations, Type::never());
            let mut values = Joiner::new(relations, Type::never());
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
                let name = value.name();
                let name = name.strip_prefix('\\').unwrap_or(name);
                if matches!(
                    go_to_lower(name).as_str(),
                    "iterator" | "iteratoraggregate" | "traversable"
                ) {
                    return (Type::array_key(), value.argument(0));
                }
            }
            if let Some(hierarchy) = relations.hierarchy {
                for target in ["IteratorAggregate", "Traversable"] {
                    let Some(projected) = hierarchy.as_supertype(value, target) else {
                        continue;
                    };
                    match projected.argument_count() {
                        1 => return (Type::array_key(), projected.argument(0)),
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

fn normalize_expression(value: &str) -> String {
    value.split_whitespace().collect()
}
