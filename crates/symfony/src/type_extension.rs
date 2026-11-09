//! Symfony call return types: container `get()` services and `HeaderBag`
//! defaults.

use std::borrow::Cow;
use std::collections::HashSet;
use std::sync::Arc;

use swlsp_php_inference::{CallContext, Extension};
use swlsp_php_semantic::{Confidence, TypeFact, TypeSource};
use swlsp_php_types::{Kind, Type, contains_uncertain, equal_fold};
use swlsp_syntax::TextRange;

use crate::model::Service;
use crate::service_index::ServiceIndex;

const CONTAINER_TYPES: [&str; 3] = [
    "Psr\\Container\\ContainerInterface",
    "Symfony\\Component\\DependencyInjection\\ContainerInterface",
    "Symfony\\Contracts\\Service\\ServiceProviderInterface",
];

fn named(name: &str) -> Type {
    Type::named(name, Vec::new())
}

fn framework_fact(ty: Type, reason: &'static str) -> TypeFact {
    TypeFact {
        ty,
        confidence: Confidence::Declared,
        source: TypeSource::Framework,
        origin: TextRange::default(),
        reason: Cow::Borrowed(reason),
    }
}

/// Symfony framework call inference (Go `symfony.PHPTypeExtension`).
#[derive(Clone, Default)]
pub struct PhpTypeExtension {
    services: Option<Arc<ServiceIndex>>,
}

impl PhpTypeExtension {
    pub fn new(services: Option<Arc<ServiceIndex>>) -> Self {
        Self { services }
    }

    fn resolve_service(
        &self,
        services: &ServiceIndex,
        id: &str,
        visited: &mut HashSet<String>,
    ) -> Option<Service> {
        if !visited.insert(id.to_owned()) {
            return None;
        }
        let service = services.get_service_by_id(id).ok()??;
        if service.alias_target.is_empty() {
            return Some(service);
        }
        self.resolve_service(services, &service.alias_target, visited)
    }
}

impl Extension for PhpTypeExtension {
    /// Container lookups answer from the service index.
    fn revision(&self) -> u64 {
        self.services
            .as_ref()
            .map_or(0, |services| services.revision())
    }

    fn infer_call(&self, context: &CallContext<'_>) -> Option<TypeFact> {
        if let Some(fact) = infer_header_bag_get(context) {
            return Some(fact);
        }
        let services = self.services.as_deref()?;
        if !equal_fold(context.name, "get")
            || context
                .arguments
                .first()
                .is_none_or(|argument| argument.ty.kind() != Kind::LiteralString)
            || !is_container_type(context)
        {
            return None;
        }
        let service_id = context.arguments[0].ty.name();
        let service = self.resolve_service(services, service_id, &mut HashSet::new())?;
        let mut class_name = service.class.as_str();
        if class_name.is_empty() && service.id.contains('\\') {
            class_name = &service.id;
        }
        if class_name.is_empty() {
            return None;
        }
        Some(framework_fact(
            named(class_name),
            "Symfony service container",
        ))
    }
}

fn infer_header_bag_get(context: &CallContext<'_>) -> Option<TypeFact> {
    let snapshot = context.snapshot?;
    if context.is_static || !equal_fold(context.name, "get") || context.arguments.len() < 2 {
        return None;
    }
    let relations = snapshot.relations();
    if !relations.is_subtype(
        &context.receiver,
        &named("Symfony\\Component\\HttpFoundation\\HeaderBag"),
    ) {
        return None;
    }
    let default_type = &context.arguments[1].ty;
    if contains_uncertain(default_type)
        || !relations.is_assignable_to(default_type, &Type::nullable(Type::string()))
    {
        return None;
    }
    Some(framework_fact(
        relations.join(&Type::string(), default_type),
        "Symfony HeaderBag default",
    ))
}

fn is_container_type(context: &CallContext<'_>) -> bool {
    if context.receiver.is_unknown() {
        return false;
    }
    let Some(snapshot) = context.snapshot else {
        return false;
    };
    let relations = snapshot.relations();
    CONTAINER_TYPES
        .iter()
        .any(|candidate| relations.is_subtype(&context.receiver, &named(candidate)))
}

#[cfg(test)]
#[path = "tests_refs/type_extension.rs"]
mod tests;
