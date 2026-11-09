//! EventDispatcher `dispatch()` return types.

use std::borrow::Cow;

use swlsp_php_inference::{CallContext, Extension};
use swlsp_php_semantic::{Confidence, TypeFact, TypeSource};
use swlsp_php_types::Kind;
use swlsp_syntax::TextRange;
use swlsp_textutil::eq_ignore_case;

use crate::parser::is_dispatcher_type;

/// Symfony's `EventDispatcherInterface::dispatch()` returns the dispatched
/// event object, also for the legacy `dispatch($name, $event)` order.
#[derive(Clone, Copy, Debug, Default)]
pub struct PhpTypeExtension;

impl PhpTypeExtension {
    pub fn new() -> Self {
        PhpTypeExtension
    }
}

impl Extension for PhpTypeExtension {
    fn infer_call(&self, context: &CallContext<'_>) -> Option<TypeFact> {
        if !eq_ignore_case(context.name, "dispatch")
            || context.arguments.is_empty()
            || !is_dispatcher_type(&context.receiver, context.snapshot)
        {
            return None;
        }
        let mut result = &context.arguments[0].ty;
        if result.kind() == Kind::LiteralString && context.arguments.len() > 1 {
            result = &context.arguments[1].ty;
        }
        if result.is_unknown() {
            return None;
        }
        Some(TypeFact {
            ty: result.clone(),
            confidence: Confidence::Declared,
            source: TypeSource::Framework,
            origin: TextRange::default(),
            reason: Cow::Borrowed("Symfony EventDispatcher returns the dispatched event"),
        })
    }
}
