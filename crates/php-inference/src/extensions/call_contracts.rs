//! `.phpstorm.meta.php` dynamic return metadata.

use std::borrow::Cow;

use swlsp_php_resolver::{
    MemberResolver, effective_call_return_type, resolve_signature_with_contracts,
};
use swlsp_php_semantic::{
    CallContract, CallReturnContract, CallReturnKind, CallValue, CallValueKind, Confidence, Symbol,
    TypeFact, TypeSource,
};
use swlsp_php_types::{Kind, Type};
use swlsp_syntax::TextRange;
use swlsp_textutil::eq_ignore_case;

use super::{CallArgument, CallContext, Extension};
use crate::types_util::join_types;

/// Evaluates dynamic return metadata published from `.phpstorm.meta.php`
/// files. Hand-written extensions can run before this generic provider.
#[derive(Clone, Copy, Debug, Default)]
pub struct CallContracts;

impl Extension for CallContracts {
    fn infer_call(&self, context: &CallContext<'_>) -> Option<TypeFact> {
        let snapshot = context.snapshot?;
        let mut inferred: Vec<Type> = Vec::new();
        let mut return_contracts: Vec<CallReturnContract> = Vec::new();
        let mut visit = |contract: &CallContract| {
            if contract.exit_point && exit_point_matches(context, contract) {
                inferred.push(Type::NEVER);
            }
            if contract.return_contract.kind != CallReturnKind::Unknown {
                return_contracts.push(contract.return_contract.clone());
            }
            true
        };
        if context.receiver.is_unknown() {
            let name_context = context.name_context();
            name_context.visit_function_names(context.name, |candidate| {
                snapshot.visit_function_call_contracts(candidate, &mut visit)
            });
        } else {
            snapshot.visit_method_call_contracts(&context.receiver, context.name, &mut visit);
        }
        if let Some(contracted) = resolve_contracted_call_signatures(context, &return_contracts) {
            inferred.push(contracted);
        }
        if inferred.is_empty() {
            return None;
        }
        Some(TypeFact {
            ty: join_types(snapshot.relations(), &inferred),
            confidence: Confidence::Declared,
            source: TypeSource::Signature,
            origin: TextRange::default(),
            reason: Cow::Borrowed(".phpstorm.meta.php call contract"),
        })
    }
}

fn resolve_contracted_call_signatures(
    context: &CallContext<'_>,
    contracts: &[CallReturnContract],
) -> Option<Type> {
    let snapshot = context.snapshot?;
    if contracts.is_empty() {
        return None;
    }
    let relations = snapshot.relations();
    let mut found_symbol = false;
    let mut effective: Vec<Type> = Vec::new();
    let mut visit_symbol = |symbol: Symbol| {
        found_symbol = true;
        let resolved =
            resolve_signature_with_contracts(relations, symbol, context.arguments, contracts);
        if resolved.compatible && resolved.contract_applied {
            effective.push(resolved.return_type);
        }
        true
    };
    if context.receiver.is_unknown() {
        let name_context = context.name_context();
        name_context.visit_function_names(context.name, |candidate| {
            snapshot.visit_function_views(candidate, |view| visit_symbol(view.materialize()));
            true
        });
    } else {
        MemberResolver::new(snapshot).visit_methods(&context.receiver, context.name, |member| {
            visit_symbol(member.symbol)
        });
    }
    if !found_symbol {
        return effective_call_return_type(relations, context.arguments, contracts);
    }
    if effective.is_empty() {
        return None;
    }
    Some(join_types(relations, &effective))
}

fn exit_point_matches(context: &CallContext<'_>, contract: &CallContract) -> bool {
    for condition in &contract.exit_arguments {
        let index = usize::from(condition.argument);
        let Some(argument) = context.arguments.get(index) else {
            return false;
        };
        let expression = context
            .argument_expression(index)
            .map(|node| normalize_contract_expression(context.text(&node)))
            .unwrap_or_default();
        if !condition
            .values
            .iter()
            .any(|expected| call_value_matches(expected, &argument.ty, &expression))
        {
            return false;
        }
    }
    true
}

/// Evaluates one return contract with argument source text filled in from
/// the call node.
#[allow(dead_code)]
pub(crate) fn evaluate_call_return_contract(
    context: &CallContext<'_>,
    contract: &CallReturnContract,
) -> Option<Type> {
    let snapshot = context.snapshot?;
    let mut arguments: Vec<CallArgument> = context.arguments.to_vec();
    if context.node.is_some() {
        let index = usize::from(contract.argument);
        if let Some(argument) = arguments.get_mut(index)
            && argument.expression.is_empty()
            && let Some(node) = context.argument_expression(index)
        {
            argument.expression = context.text(&node).to_owned();
        }
    }
    effective_call_return_type(
        snapshot.relations(),
        &arguments,
        std::slice::from_ref(contract),
    )
}

fn call_value_matches(value: &CallValue, argument: &Type, expression: &str) -> bool {
    match value.kind {
        CallValueKind::String => {
            argument.kind() == Kind::LiteralString && argument.name() == &*value.value
        }
        CallValueKind::Number => {
            argument.to_string() == normalize_contract_expression(&value.expression)
        }
        _ => eq_ignore_case(
            &normalize_contract_expression(&value.expression),
            expression,
        ),
    }
}

/// Removes all Unicode white space.
pub(crate) fn normalize_contract_expression(value: &str) -> String {
    value.split_whitespace().collect()
}
