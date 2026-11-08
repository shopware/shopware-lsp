use std::collections::HashMap;

use swlsp_php_semantic::{
    Attribute, AttributeArgument, AttributeValue, AttributeValueKind, CallMapEntry,
    CallReturnContract, CallReturnKind, CallValue, CallValueKind, Flags, Parameter, Symbol,
};
use swlsp_php_types::{Relations, Type};

use super::*;
use crate::{
    Argument, effective_call_return_type, iterable_types, resolve_signature,
    resolve_signature_with_contracts,
};

fn void_symbol(parameters: Vec<Parameter>) -> Symbol {
    Symbol {
        parameters,
        return_type: Type::void(),
        ..Symbol::default()
    }
}

#[test]
fn resolve_signature_accepts_matching_documented_alias() {
    let alias = named("App\\ImportedArrayAlias");
    let array = Type::array(Type::array_key(), Type::mixed());
    let symbol = void_symbol(vec![Parameter {
        native_type: array.clone(),
        doc_type: alias.clone(),
        ..param("$value", array)
    }]);
    assert!(
        resolve_signature(Relations::default(), symbol.clone(), &[Argument::of(alias)]).compatible
    );
    let rejected = resolve_signature(
        Relations::default(),
        symbol,
        &[Argument::of(named("App\\DifferentAlias"))],
    );
    assert!(!rejected.compatible);
    assert_eq!(
        rejected.mismatch,
        "parameter #1 ($value) expects array<array-key,mixed>, App\\DifferentAlias given"
    );
}

#[test]
fn resolve_signature_accepts_native_type_when_phpdoc_is_too_narrow() {
    let node = named("Twig\\Node\\Node");
    let symbol = void_symbol(vec![Parameter {
        native_type: Type::iterable(Type::array_key(), Type::mixed()),
        doc_type: Type::array(Type::array_key(), node.clone()),
        ..param("$nodes", Type::array(Type::array_key(), node.clone()))
    }]);
    assert!(
        resolve_signature(
            Relations::default(),
            symbol,
            &[Argument::of(Type::iterable(Type::int(), node))],
        )
        .compatible
    );
}

fn repository_contract() -> CallReturnContract {
    CallReturnContract {
        kind: CallReturnKind::ArgumentMap,
        argument: 0,
        map: vec![CallMapEntry {
            key: CallValue {
                kind: CallValueKind::String,
                value: "product.repository".into(),
                expression: "'product.repository'".into(),
            },
            result: CallValue {
                kind: CallValueKind::ClassConstant,
                expression: "\\App\\Repository\\ProductRepository::class".into(),
                ..CallValue::default()
            },
        }],
    }
}

#[test]
fn resolve_signature_surfaces_dynamic_contract_return_type() {
    let symbol = Symbol {
        parameters: vec![param("$id", Type::string())],
        return_type: Type::object(),
        ..Symbol::default()
    };
    let contract = repository_contract();
    let resolved = resolve_signature_with_contracts(
        Relations::default(),
        symbol.clone(),
        &[Argument {
            ty: Type::literal_string("product.repository"),
            expression: "'product.repository'".into(),
            ..Argument::default()
        }],
        std::slice::from_ref(&contract),
    );
    assert!(resolved.compatible);
    assert!(resolved.contract_applied);
    assert_eq!(
        resolved.return_type.text(),
        "App\\Repository\\ProductRepository"
    );

    let mut incompatible = symbol;
    incompatible.parameters[0].ty = Type::int();
    let resolved = resolve_signature_with_contracts(
        Relations::default(),
        incompatible,
        &[Argument::of(Type::literal_string("product.repository"))],
        &[contract],
    );
    assert!(!resolved.compatible);
}

#[test]
fn call_contracts_cover_element_types_and_map_patterns() {
    let relations = Relations::default();
    let element = CallReturnContract {
        kind: CallReturnKind::ArgumentElementType,
        argument: 0,
        map: Vec::new(),
    };
    let list = [Argument::of(Type::list(named("Product")))];
    assert_eq!(
        effective_call_return_type(relations, &list, std::slice::from_ref(&element)),
        Some(named("Product"))
    );
    assert_eq!(
        effective_call_return_type(relations, &[Argument::of(Type::string())], &[element]),
        None
    );

    let pattern = |key: &str, value: &str| CallMapEntry {
        key: CallValue {
            kind: CallValueKind::String,
            value: key.into(),
            expression: format!("'{key}'").into(),
        },
        result: CallValue {
            kind: CallValueKind::String,
            value: value.into(),
            ..CallValue::default()
        },
    };
    let map = CallReturnContract {
        kind: CallReturnKind::ArgumentMap,
        argument: 0,
        map: vec![pattern("", "@Proxy"), pattern("by-arg", "$1")],
    };
    let by_argument = [
        Argument::of(Type::literal_string("by-arg")),
        Argument::of(Type::class_string(named("App\\Entity"))),
    ];
    assert_eq!(
        effective_call_return_type(relations, &by_argument, std::slice::from_ref(&map)),
        Some(named("App\\Entity"))
    );
    let fallback = [Argument::of(Type::literal_string("\\App\\Thing"))];
    assert_eq!(
        effective_call_return_type(relations, &fallback, &[map]),
        Some(named("App\\ThingProxy"))
    );

    let shape = Type::array_shape(
        vec![
            swlsp_php_types::ShapeField::new("0", Type::int()),
            swlsp_php_types::ShapeField::new("name", Type::string()),
        ],
        false,
    );
    let (key, value) = iterable_types(&shape, relations);
    assert_eq!(key.text(), "\"name\"|0");
    assert_eq!(value.text(), "int|string");
    let (key, value) = iterable_types(&generic("Traversable", vec![named("Row")]), relations);
    assert_eq!((key, value), (Type::array_key(), named("Row")));
}

#[test]
fn resolve_signature_allows_extra_positional_arguments_for_user_code() {
    let mut symbol = Symbol {
        parameters: vec![param("$value", Type::string())],
        ..Symbol::default()
    };
    let arguments = [Argument::of(Type::string()), Argument::of(Type::bool())];
    assert!(resolve_signature(Relations::default(), symbol.clone(), &arguments).compatible);
    symbol.flags |= Flags::GENERATED_STUB;
    let rejected = resolve_signature(Relations::default(), symbol, &arguments);
    assert!(!rejected.compatible);
    assert_eq!(rejected.mismatch, "too many arguments");
}

#[test]
fn resolve_signature_rejects_unknown_named_arguments() {
    let symbol = Symbol {
        parameters: vec![param("$value", Type::string())],
        ..Symbol::default()
    };
    assert!(
        !resolve_signature(
            Relations::default(),
            symbol,
            &[Argument::named("$extra", Type::string())],
        )
        .compatible
    );
}

fn string_argument(name: &str, value: &str) -> AttributeArgument {
    AttributeArgument {
        name: name.into(),
        value: AttributeValue {
            kind: AttributeValueKind::String,
            value: value.into(),
            ..AttributeValue::default()
        },
        ..AttributeArgument::default()
    }
}

#[test]
fn resolve_signature_applies_return_type_contract() {
    let boolean_contract = Attribute {
        name: "JetBrains\\PhpStorm\\Internal\\ReturnTypeContract".into(),
        arguments: vec![
            string_argument("true", "float"),
            string_argument("false", "string"),
        ],
        ..Attribute::default()
    };
    let mut symbol = Symbol {
        return_type: Type::union([Type::string(), Type::float()]),
        parameters: vec![Parameter {
            optional: true,
            attributes: vec![boolean_contract],
            default_value: Some(Box::new(AttributeValue {
                kind: AttributeValueKind::Bool,
                value: "false".into(),
                ..AttributeValue::default()
            })),
            ..param("$asFloat", Type::bool())
        }],
        ..Symbol::default()
    };
    let relations = Relations::default();
    let returns = |symbol: &Symbol, arguments: &[Argument]| {
        resolve_signature(relations, symbol.clone(), arguments)
            .return_type
            .text()
            .into_owned()
    };
    assert_eq!(
        returns(&symbol, &[Argument::of(Type::true_type())]),
        "float"
    );
    assert_eq!(
        returns(&symbol, &[Argument::of(Type::false_type())]),
        "string"
    );
    assert_eq!(returns(&symbol, &[]), "string");

    let existence_contract = Attribute {
        name: "JetBrains\\PhpStorm\\Internal\\ReturnTypeContract".into(),
        arguments: vec![
            string_argument("exists", "int|null"),
            string_argument("notExists", "array|null"),
        ],
        ..Attribute::default()
    };
    symbol.parameters = vec![Parameter {
        flags: Flags::VARIADIC,
        attributes: vec![existence_contract],
        ..param("$vars", Type::mixed())
    }];
    assert_eq!(returns(&symbol, &[]), "array|null");
    assert_eq!(
        returns(&symbol, &[Argument::of(Type::string())]),
        "int|null"
    );
}

#[test]
fn resolve_signature_defers_bounds_for_caller_template() {
    let symbol = with_templates(
        void_symbol(vec![param(
            "$class",
            Type::class_string(Type::template("Expected")),
        )]),
        vec![template_with("Expected", Type::object(), Type::UNKNOWN)],
    );
    assert!(
        resolve_signature(
            Relations::default(),
            symbol.clone(),
            &[Argument::of(Type::class_string(Type::template("T")))],
        )
        .compatible
    );
    assert!(
        resolve_signature(
            Relations::default(),
            symbol,
            &[Argument::of(Type::class_string(Type::union([
                Type::template("T"),
                named("Product"),
            ])))],
        )
        .compatible
    );
}

#[test]
fn resolve_signature_accepts_nominal_subtype_for_generic_bound() {
    let collection = generic("Collection", vec![named("Contract")]);
    let symbol = with_templates(
        Symbol {
            parameters: vec![param("$values", Type::template("T"))],
            return_type: Type::template("T"),
            ..Symbol::default()
        },
        vec![template_with("T", collection, Type::UNKNOWN)],
    );
    let actual = named("ConcreteCollection");
    let hierarchy = TestHierarchy(HashMap::from([("ConcreteCollection", "Collection")]));
    let resolved = resolve_signature(
        Relations::new(&hierarchy),
        symbol,
        &[Argument::of(actual.clone())],
    );
    assert!(resolved.compatible);
    assert_eq!(resolved.return_type, actual);
}

#[test]
fn resolve_signature_prefers_structured_template_union_arm() {
    let symbol = with_templates(
        Symbol {
            parameters: vec![param(
                "$objectOrClass",
                Type::union([
                    Type::template("T"),
                    Type::class_string(Type::template("T")),
                    Type::string(),
                ]),
            )],
            return_type: Type::template("T"),
            ..Symbol::default()
        },
        vec![template_with("T", Type::object(), Type::object())],
    );
    let product = named("Product");
    let resolved = resolve_signature(
        Relations::default(),
        symbol,
        &[Argument::of(Type::class_string(product.clone()))],
    );
    assert!(resolved.compatible);
    assert_eq!(resolved.templates.get("T"), Some(&product));
    assert_eq!(resolved.return_type, product);
}
