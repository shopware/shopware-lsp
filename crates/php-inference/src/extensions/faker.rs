//! Faker generator magic-method return types (port of `faker.go`).

use std::borrow::Cow;

use swlsp_php_semantic::{Confidence, TypeFact, TypeSource};
use swlsp_php_types::{Kind, Type, equal_fold};
use swlsp_syntax::TextRange;

use super::{CallContext, Extension};
use crate::types_util::iterable_types;

const FAKER_GENERATOR_CLASS: &str = "Faker\\Generator";

/// Models `Faker\Generator::randomElement()`, whose upstream declaration
/// returns mixed although it preserves the input element type.
#[derive(Clone, Copy, Debug, Default)]
pub struct FakerTypes;

impl Extension for FakerTypes {
    fn infer_call(&self, context: &CallContext<'_>) -> Option<TypeFact> {
        if context.is_static
            || !equal_fold(context.name, "randomElement")
            || !is_faker_generator(context)
        {
            return None;
        }
        let element = match context.arguments.len() {
            // Matches Faker's default ['a', 'b', 'c'] argument.
            0 => Type::STRING,
            1 => iterable_types(&context.arguments[0].ty, context.relations()).1,
            _ => return None,
        };
        if element.is_unknown() || element.kind() == Kind::Mixed {
            return None;
        }
        Some(TypeFact {
            ty: Type::nullable(element),
            confidence: Confidence::Declared,
            source: TypeSource::Signature,
            origin: TextRange::default(),
            reason: Cow::Borrowed("Faker randomElement preserves the input element type"),
        })
    }
}

fn is_faker_generator(context: &CallContext<'_>) -> bool {
    let target = Type::named(FAKER_GENERATOR_CLASS, Vec::new());
    if context.receiver.kind() == Kind::Object && equal_fold(context.receiver.name(), target.name())
    {
        return true;
    }
    context
        .snapshot
        .is_some_and(|snapshot| snapshot.relations().is_subtype(&context.receiver, &target))
}
