//! Shopware call return types (system config, DAL repositories and
//! collections, feature flags).

use std::borrow::Cow;

use swlsp_php_inference::{CallContext, Extension};
use swlsp_php_semantic::{Confidence, TypeFact, TypeSource};
use swlsp_php_types::{Joiner, Kind, Relations, Type};
use swlsp_syntax::TextRange;
use swlsp_textutil::eq_ignore_case;

const SYSTEM_CONFIG_SERVICE: &str = "Shopware\\Core\\System\\SystemConfig\\SystemConfigService";
const ENTITY_COLLECTION: &str = "Shopware\\Core\\Framework\\DataAbstractionLayer\\EntityCollection";
const ENTITY: &str = "Shopware\\Core\\Framework\\DataAbstractionLayer\\Entity";
const ENTITY_SEARCH_RESULT: &str =
    "Shopware\\Core\\Framework\\DataAbstractionLayer\\Search\\EntitySearchResult";
const ENTITY_REPOSITORY: &str = "Shopware\\Core\\Framework\\DataAbstractionLayer\\EntityRepository";
const FEATURE: &str = "Shopware\\Core\\Framework\\Feature";
const ID_SEARCH_RESULT: &str =
    "Shopware\\Core\\Framework\\DataAbstractionLayer\\Search\\IdSearchResult";
const AGGREGATION_RESULT_COLLECTION: &str = "Shopware\\Core\\Framework\\DataAbstractionLayer\\Search\\AggregationResult\\AggregationResultCollection";

fn named(name: &str) -> Type {
    Type::named(name, Vec::new())
}

/// Infers return types of Shopware framework calls (such as repository
/// searches, collections, system config and feature flags).
#[derive(Clone, Copy, Debug, Default)]
pub struct PhpTypeExtension;

impl PhpTypeExtension {
    pub fn new() -> Self {
        PhpTypeExtension
    }
}

impl Extension for PhpTypeExtension {
    fn infer_call(&self, context: &CallContext<'_>) -> Option<TypeFact> {
        let name = canonical_shopware_method(context.name)?;
        if is_system_config_method(name)
            && let Some(result) = system_config_type(context, name)
        {
            return Some(framework_fact(result, "Shopware system config"));
        }
        if is_collection_method(name)
            && let Some(result) = collection_type(context, name)
        {
            return Some(framework_fact(result, "Shopware DAL collection"));
        }
        if is_repository_method(name)
            && let Some(result) = repository_type(context, name)
        {
            return Some(framework_fact(result, "Shopware DAL repository"));
        }
        if name == "isactive" && is_receiver(context, &context.receiver, FEATURE) {
            return Some(framework_fact(Type::bool(), "Shopware feature flag"));
        }
        None
    }
}

/// The lower-cased method name when it is one Shopware infers. Does not
/// allocate.
fn canonical_shopware_method(name: &str) -> Option<&'static str> {
    const METHODS: [&str; 19] = [
        "get",
        "set",
        "last",
        "sort",
        "first",
        "slice",
        "count",
        "getint",
        "delete",
        "filter",
        "search",
        "getbool",
        "getfloat",
        "isactive",
        "getstring",
        "getdomain",
        "searchids",
        "aggregate",
        "getelements",
    ];
    METHODS
        .iter()
        .find(|method| method.len() == name.len() && eq_ignore_case(name, method))
        .copied()
}

fn is_system_config_method(name: &str) -> bool {
    matches!(
        name,
        "get" | "getint" | "getstring" | "getfloat" | "getbool" | "getdomain" | "set" | "delete"
    )
}

fn is_collection_method(name: &str) -> bool {
    matches!(
        name,
        "first" | "last" | "get" | "getelements" | "filter" | "sort" | "slice" | "count"
    )
}

fn is_repository_method(name: &str) -> bool {
    matches!(name, "search" | "searchids" | "aggregate")
}

fn system_config_type(context: &CallContext<'_>, name: &str) -> Option<Type> {
    if !is_receiver(context, &context.receiver, SYSTEM_CONFIG_SERVICE) {
        return None;
    }
    Some(match name {
        "get" => Type::mixed(),
        "getint" => Type::int(),
        "getstring" => Type::string(),
        "getfloat" => Type::float(),
        "getbool" => Type::bool(),
        "getdomain" => Type::array(Type::string(), Type::mixed()),
        "set" | "delete" => Type::void(),
        _ => return None,
    })
}

fn repository_type(context: &CallContext<'_>, name: &str) -> Option<Type> {
    if !is_receiver(context, &context.receiver, ENTITY_REPOSITORY) {
        return None;
    }
    let entity_collection = context
        .receiver
        .arguments()
        .first()
        .cloned()
        .unwrap_or_else(Type::object);
    Some(match name {
        "search" => Type::named(ENTITY_SEARCH_RESULT, vec![entity_collection]),
        "searchids" => named(ID_SEARCH_RESULT),
        "aggregate" => named(AGGREGATION_RESULT_COLLECTION),
        _ => return None,
    })
}

fn collection_type(context: &CallContext<'_>, name: &str) -> Option<Type> {
    if !is_receiver(context, &context.receiver, ENTITY_COLLECTION)
        && !is_receiver(context, &context.receiver, ENTITY_SEARCH_RESULT)
    {
        return None;
    }
    let element = collection_element_type(context, &context.receiver);
    Some(match name {
        "first" | "last" | "get" => Type::nullable(element),
        "getelements" => Type::array(Type::array_key(), element),
        "filter" | "sort" | "slice" => context.receiver.clone(),
        "count" => Type::int(),
        _ => return None,
    })
}

fn collection_element_type(context: &CallContext<'_>, receiver: &Type) -> Type {
    if receiver.kind() == Kind::Union {
        let relations = context
            .snapshot
            .map_or_else(Relations::default, |snapshot| snapshot.relations());
        let mut elements = Joiner::new(relations, Type::never());
        for index in 0..receiver.argument_count() {
            elements.add(collection_element_type(context, &receiver.argument(index)));
        }
        return elements.value();
    }
    let Some(snapshot) = context.snapshot else {
        return direct_collection_element_type(receiver);
    };
    let mut receiver = receiver.clone();
    if snapshot
        .relations()
        .is_subtype(&receiver, &named(ENTITY_SEARCH_RESULT))
        && receiver.argument_count() > 0
    {
        receiver = receiver.argument(0);
    }
    if let Some(projected) = snapshot.as_supertype(&receiver, ENTITY_COLLECTION) {
        if projected.argument_count() > 0 {
            return projected.argument(0);
        }
        // A raw EntityCollection still carries its declared template bound.
        // Returning object here loses the fact that every element is a
        // Struct and causes invalid addExtension diagnostics after dynamic
        // creation.
        return named(ENTITY);
    }
    direct_collection_element_type(&receiver)
}

fn direct_collection_element_type(receiver: &Type) -> Type {
    if receiver.argument_count() > 0 {
        return receiver.argument(0);
    }
    Type::object()
}

fn is_receiver(context: &CallContext<'_>, receiver: &Type, target: &str) -> bool {
    if receiver.kind() == Kind::Object && eq_ignore_case(receiver.name(), target) {
        return true;
    }
    context
        .snapshot
        .is_some_and(|snapshot| snapshot.relations().is_subtype(receiver, &named(target)))
}

fn framework_fact(value: Type, reason: &'static str) -> TypeFact {
    TypeFact {
        ty: value,
        confidence: Confidence::Declared,
        source: TypeSource::Framework,
        origin: TextRange::default(),
        reason: Cow::Borrowed(reason),
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use swlsp_php_semantic::{Document, Snapshot, Symbol, SymbolId, SymbolKind, TemplateParameter};

    fn context<'a>(snapshot: &'a Snapshot, name: &'a str, receiver: Type) -> CallContext<'a> {
        CallContext {
            snapshot: Some(snapshot),
            document: None,
            node: None,
            source: "",
            name,
            receiver,
            arguments: &[],
            current_class: Type::UNKNOWN,
            is_static: false,
        }
    }

    #[test]
    fn shopware_type_extension() {
        let extension = PhpTypeExtension::new();
        let mut entity_collection = Symbol {
            id: SymbolId::new("entity-collection"),
            kind: SymbolKind::Class,
            name: "EntityCollection".into(),
            fully_qualified: ENTITY_COLLECTION.into(),
            path: "/collections.php".into(),
            ..Symbol::default()
        };
        entity_collection.set_templates(vec![TemplateParameter {
            name: "TElement".into(),
            ..TemplateParameter::default()
        }]);
        let mut payment_collection = Symbol {
            id: SymbolId::new("payment-collection"),
            kind: SymbolKind::Class,
            name: "PaymentMethodCollection".into(),
            fully_qualified: "PaymentMethodCollection".into(),
            path: "/collections.php".into(),
            ..Symbol::default()
        };
        payment_collection.set_hierarchy(
            vec![ENTITY_COLLECTION.into()],
            Vec::new(),
            Vec::new(),
            vec![Type::named(
                ENTITY_COLLECTION,
                vec![named("PaymentMethodEntity")],
            )],
            Vec::new(),
            Vec::new(),
            Vec::new(),
        );
        let snapshot = Snapshot::new(
            1,
            &[Document {
                path: "/collections.php".into(),
                symbols: vec![entity_collection, payment_collection],
                ..Document::default()
            }],
        );

        let infer = |name: &str, receiver: Type| {
            extension
                .infer_call(&context(&snapshot, name, receiver))
                .map(|fact| fact.ty.to_string())
        };
        assert_eq!(
            infer("getString", named(SYSTEM_CONFIG_SERVICE)).as_deref(),
            Some("string")
        );
        assert_eq!(
            infer(
                "first",
                Type::named(ENTITY_COLLECTION, vec![named("ProductEntity")])
            )
            .as_deref(),
            Some("ProductEntity|null")
        );
        assert_eq!(
            infer("get", named(ENTITY_COLLECTION)),
            Some(format!("{ENTITY}|null"))
        );
        assert_eq!(
            infer("first", named("PaymentMethodCollection")).as_deref(),
            Some("PaymentMethodEntity|null")
        );
        assert_eq!(
            infer(
                "first",
                Type::named(ENTITY_SEARCH_RESULT, vec![named("PaymentMethodCollection")])
            )
            .as_deref(),
            Some("PaymentMethodEntity|null")
        );
        assert_eq!(
            infer(
                "get",
                Type::union([
                    named("PaymentMethodCollection"),
                    Type::named(ENTITY_COLLECTION, vec![named("ProductEntity")]),
                ])
            )
            .as_deref(),
            Some("PaymentMethodEntity|ProductEntity|null")
        );
        assert_eq!(
            infer(
                "search",
                Type::named(ENTITY_REPOSITORY, vec![named("ProductCollection")])
            )
            .as_deref(),
            Some(
                "Shopware\\Core\\Framework\\DataAbstractionLayer\\Search\\EntitySearchResult<ProductCollection>"
            )
        );
        assert_eq!(infer("isActive", named(FEATURE)).as_deref(), Some("bool"));
        assert_eq!(infer("unknown", named(FEATURE)), None);
        assert_eq!(infer("getInt", named("Other")), None);
    }

    #[test]
    fn method_classification_is_case_insensitive() {
        assert_eq!(
            canonical_shopware_method("GetElements"),
            Some("getelements")
        );
        assert_eq!(canonical_shopware_method("SEARCHIDS"), Some("searchids"));
        assert_eq!(canonical_shopware_method("getElement"), None);
        assert_eq!(canonical_shopware_method(""), None);
    }
}
