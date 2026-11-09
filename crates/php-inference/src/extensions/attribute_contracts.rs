//! Call-site-sensitive JetBrains attributes.

use std::borrow::Cow;

use swlsp_php_resolver::MemberResolver;
use swlsp_php_semantic::{
    Attribute, AttributeValue, AttributeValueKind, Confidence, SymbolView, TypeFact, TypeSource,
    attribute_named,
};
use swlsp_php_types::{Kind, Type};
use swlsp_syntax::TextRange;
use swlsp_textutil::{eq_ignore_case, lowercase};

use super::{CallContext, Extension, normalize_contract_expression};
use crate::syntax::trim_backslash;

/// Evaluates `#[NoReturn]`, including its argument-conditional form.
#[derive(Clone, Copy, Debug, Default)]
pub struct AttributeContracts;

impl Extension for AttributeContracts {
    fn infer_call(&self, context: &CallContext<'_>) -> Option<TypeFact> {
        let snapshot = context.snapshot?;
        let mut matched = false;
        let mut visit = |symbol: SymbolView<'_>| {
            let attributes = symbol.attributes();
            if let Some(attribute) = attribute_named(&attributes, "NoReturn")
                && no_return_attribute_matches(context, attribute)
            {
                matched = true;
            }
            !matched
        };
        if context.receiver.is_unknown() {
            let name_context = context.name_context();
            name_context.visit_function_names(context.name, |candidate| {
                snapshot.visit_function_views(candidate, &mut visit)
            });
        } else {
            MemberResolver::new(snapshot).visit_method_ids(
                &context.receiver,
                context.name,
                &mut visit,
            );
        }
        if !matched {
            return None;
        }
        Some(TypeFact {
            ty: Type::NEVER,
            confidence: Confidence::Declared,
            source: TypeSource::Signature,
            origin: TextRange::default(),
            reason: Cow::Borrowed("JetBrains NoReturn attribute"),
        })
    }
}

fn no_return_attribute_matches(context: &CallContext<'_>, attribute: &Attribute) -> bool {
    if attribute.arguments.is_empty() {
        return true;
    }
    if context.arguments.len() < attribute.arguments.len() {
        return false;
    }
    for (index, argument) in attribute.arguments.iter().enumerate() {
        let expected = &argument.value;
        if no_return_any_argument(expected) {
            continue;
        }
        let actual = &context.arguments[index].ty;
        let expression = context
            .argument_expression(index)
            .map(|node| normalize_contract_expression(context.text(&node)))
            .unwrap_or_default();
        if !attribute_value_matches(expected, actual, &expression) {
            return false;
        }
    }
    true
}

fn no_return_any_argument(value: &AttributeValue) -> bool {
    if value.kind != AttributeValueKind::ClassConstant {
        return false;
    }
    let identity = lowercase(trim_backslash(&value.value));
    identity.ends_with("\\noreturn::any_argument") || identity == "noreturn::any_argument"
}

fn attribute_value_matches(expected: &AttributeValue, actual: &Type, expression: &str) -> bool {
    match expected.kind {
        AttributeValueKind::String => {
            actual.kind() == Kind::LiteralString && actual.name() == &*expected.value
        }
        AttributeValueKind::Integer | AttributeValueKind::Float => {
            actual.to_string() == normalize_contract_expression(&expected.expression)
        }
        AttributeValueKind::Bool => {
            &*expected.value == "true" && actual.kind() == Kind::True
                || &*expected.value == "false" && actual.kind() == Kind::False
        }
        AttributeValueKind::Null => actual.kind() == Kind::Null,
        _ => eq_ignore_case(
            &normalize_contract_expression(&expected.expression),
            expression,
        ),
    }
}
