use std::collections::HashMap;

use swlsp_php_semantic::{
    Flags, Snapshot, SymbolId, SymbolKind, TraitAlias, TypeAssertion, Visibility,
};
use swlsp_php_types::{CallableParameter, Kind, Relations, Type};

use super::*;
use crate::{Argument, MemberResolver, ResolvedMember, resolve_signature};

type IdVisitor<'v> = dyn Fn(&mut dyn FnMut(SymbolId)) -> bool + 'v;

fn ids(values: &[&str]) -> Vec<SymbolId> {
    values.iter().map(|value| SymbolId::from(*value)).collect()
}

fn first_method(symbol_id: &str, return_type: Type) -> Symbol {
    let mut symbol = Symbol {
        return_type,
        ..member(
            symbol_id,
            SymbolKind::Method,
            "first",
            "Collection::first",
            "base",
        )
    };
    symbol.set_assertions(vec![TypeAssertion {
        target: "$value".into(),
        ty: Type::template("T"),
        when_true: true,
        ..TypeAssertion::default()
    }]);
    symbol
}

#[test]
fn member_resolution_across_inheritance_and_templates() {
    let snapshot = Snapshot::new(
        1,
        &[document(
            "/types.php",
            vec![
                with_templates(
                    sym("base", SymbolKind::Class, "Collection", "Collection"),
                    vec![template("T")],
                ),
                first_method("first", Type::template("T")),
                Symbol {
                    ty: Type::template("T"),
                    ..member(
                        "current",
                        SymbolKind::Property,
                        "current",
                        "Collection::$current",
                        "base",
                    )
                },
                with_hierarchy(
                    sym("child", SymbolKind::Class, "Products", "Products"),
                    &["Collection"],
                    &[],
                    &[],
                    Vec::new(),
                    Vec::new(),
                ),
            ],
        )],
    );
    let resolver = MemberResolver::new(&snapshot);
    let resolved = resolver.methods(&generic("Collection", vec![named("Product")]), "first");
    assert_eq!(resolved.len(), 1);
    assert_eq!(resolved[0].ty.text(), "Product");
    assert_eq!(resolved[0].symbol.assertions().len(), 1);
    assert_eq!(resolved[0].symbol.assertions()[0].ty.text(), "Product");

    let method_ids = resolver.method_ids(
        &Type::union([named("Products"), named("Collection")]),
        "first",
    );
    assert_eq!(method_ids, ids(&["first"]));

    let property_types =
        resolver.property_types(&generic("Collection", vec![named("Product")]), "current");
    assert_eq!(property_types.len(), 1);
    assert_eq!(property_types[0].text(), "Product");

    let union_property_types = resolver.property_types(
        &Type::union([
            generic("Collection", vec![named("Product")]),
            generic("Collection", vec![named("Category")]),
        ]),
        "current",
    );
    assert_eq!(union_property_types.len(), 1);
}

#[test]
fn member_resolution_supports_trait_aliases() {
    let snapshot = Snapshot::new(
        1,
        &[
            document(
                "/trait.php",
                vec![
                    sym("trait", SymbolKind::Trait, "Reusable", "Reusable"),
                    Symbol {
                        parameters: vec![param("$input", Type::string())],
                        return_type: Type::int(),
                        ..member(
                            "value",
                            SymbolKind::Method,
                            "value",
                            "Reusable::value",
                            "trait",
                        )
                    },
                ],
            ),
            document(
                "/consumer.php",
                vec![with_hierarchy(
                    sym("consumer", SymbolKind::Class, "Consumer", "Consumer"),
                    &[],
                    &[],
                    &["Reusable"],
                    Vec::new(),
                    vec![TraitAlias {
                        trait_name: "Reusable".into(),
                        method: "value".into(),
                        alias: "aliasedValue".into(),
                        visibility: Visibility::Private,
                        has_visibility: true,
                    }],
                )],
            ),
        ],
    );
    let resolver = MemberResolver::new(&snapshot);
    let methods = resolver.methods(&named("Consumer"), "aliasedValue");
    assert_eq!(methods.len(), 1);
    assert_eq!(&*methods[0].symbol.name, "aliasedValue");
    assert_eq!(methods[0].symbol.container, "consumer");
    assert_eq!(
        &*methods[0].symbol.fully_qualified,
        "Consumer::aliasedValue"
    );
    assert_eq!(methods[0].symbol.visibility, Visibility::Private);
    assert_eq!(methods[0].symbol.parameters.len(), 1);
    assert_eq!(methods[0].ty.text(), "int");
    assert_eq!(
        resolver.method_ids(&named("Consumer"), "aliasedValue"),
        ids(&["value"])
    );

    let mut names: Vec<String> = resolver
        .all(&named("Consumer"))
        .into_iter()
        .filter(|member| member.symbol.kind == SymbolKind::Method)
        .map(|member| member.symbol.name.to_string())
        .collect();
    names.sort();
    assert_eq!(names, ["aliasedValue", "value"]);
}

#[test]
fn member_id_visitors_match_slice_apis_and_stop_early() {
    let snapshot = Snapshot::new(
        1,
        &[document(
            "/service.php",
            vec![
                sym("service", SymbolKind::Class, "Service", "Service"),
                member("run", SymbolKind::Method, "run", "", "service"),
                member("name", SymbolKind::Property, "name", "", "service"),
                member("kind", SymbolKind::ClassConstant, "KIND", "", "service"),
                member("active", SymbolKind::EnumCase, "ACTIVE", "", "service"),
            ],
        )],
    );
    let resolver = MemberResolver::new(&snapshot);
    let receiver = named("Service");
    let collect = |visit: &IdVisitor<'_>| {
        let mut result = Vec::new();
        assert!(visit(&mut |id| result.push(id)));
        result
    };
    assert_eq!(
        resolver.method_ids(&receiver, "run"),
        collect(&|push| resolver.visit_method_ids(&receiver, "run", |view| {
            push(view.symbol_id());
            true
        }))
    );
    assert_eq!(
        resolver.all_method_ids(&receiver),
        collect(&|push| resolver.visit_all_method_ids(&receiver, |view| {
            push(view.symbol_id());
            true
        }))
    );
    assert_eq!(
        resolver.property_ids(&receiver, "name"),
        collect(
            &|push| resolver.visit_property_ids(&receiver, "name", |view| {
                push(view.symbol_id());
                true
            })
        )
    );
    for name in ["KIND", "ACTIVE"] {
        let expected = resolver.constant_ids(&receiver, name);
        assert_eq!(expected.len(), 1);
        assert_eq!(
            expected,
            collect(
                &|push| resolver.visit_constant_ids(&receiver, name, |view| {
                    push(view.symbol_id());
                    true
                })
            )
        );
    }
    let mut visited = 0;
    assert!(!resolver.visit_method_ids(&receiver, "run", |_| {
        visited += 1;
        false
    }));
    assert_eq!(visited, 1);
}

#[test]
fn resolved_method_visitor_matches_union_and_stops_early() {
    let documents: Vec<_> = ["First", "Second"]
        .iter()
        .map(|class_name| {
            let class_id = class_name.to_lowercase();
            document(
                &format!("/{class_name}.php"),
                vec![
                    sym(&class_id, SymbolKind::Class, class_name, class_name),
                    Symbol {
                        return_type: named(class_name),
                        ..member(
                            &format!("{class_name}::run"),
                            SymbolKind::Method,
                            "run",
                            &format!("{class_name}::run"),
                            &class_id,
                        )
                    },
                ],
            )
        })
        .collect();
    let snapshot = Snapshot::new(1, &documents);
    let resolver = MemberResolver::new(&snapshot);
    let receiver = Type::union([named("First"), named("Second")]);
    let expected = resolver.methods(&receiver, "run");
    assert_eq!(expected.len(), 2);
    let mut actual: Vec<ResolvedMember> = Vec::new();
    assert!(resolver.visit_methods(&receiver, "run", |member| {
        actual.push(member);
        true
    }));
    assert_eq!(expected, actual);

    let mut visited = 0;
    assert!(!resolver.visit_methods(&receiver, "run", |_| {
        visited += 1;
        false
    }));
    assert_eq!(visited, 1);
}

#[test]
fn all_method_ids_returns_only_effective_declarations() {
    let snapshot = Snapshot::new(
        1,
        &[document(
            "/methods.php",
            vec![
                sym("contract", SymbolKind::Interface, "Contract", "Contract"),
                member(
                    "required",
                    SymbolKind::Method,
                    "run",
                    "Contract::run",
                    "contract",
                ),
                with_hierarchy(
                    sym("missing", SymbolKind::Class, "Missing", "Missing"),
                    &[],
                    &["Contract"],
                    &[],
                    Vec::new(),
                    Vec::new(),
                ),
                with_hierarchy(
                    sym("concrete", SymbolKind::Class, "Concrete", "Concrete"),
                    &[],
                    &["Contract"],
                    &[],
                    Vec::new(),
                    Vec::new(),
                ),
                member(
                    "implemented",
                    SymbolKind::Method,
                    "run",
                    "Concrete::run",
                    "concrete",
                ),
            ],
        )],
    );
    let resolver = MemberResolver::new(&snapshot);
    assert_eq!(
        resolver.all_method_ids(&named("Missing")),
        ids(&["required"])
    );
    assert_eq!(
        resolver.all_method_ids(&named("Concrete")),
        ids(&["implemented"])
    );
}

#[test]
fn resolve_generic_signature() {
    let symbol = Symbol {
        kind: SymbolKind::Function,
        name: "identity".into(),
        return_type: Type::template("T"),
        parameters: vec![param("$value", Type::template("T"))],
        ..Symbol::default()
    };
    let resolved = resolve_signature(
        Relations::default(),
        symbol.clone(),
        &[Argument::of(named("Product"))],
    );
    assert!(resolved.compatible);
    assert_eq!(resolved.return_type.text(), "Product");

    let mut array_identity = symbol;
    array_identity.parameters[0].ty = Type::array(Type::array_key(), Type::template("T"));
    let resolved = resolve_signature(
        Relations::default(),
        array_identity,
        &[Argument::of(Type::union([
            Type::array(Type::array_key(), named("Product")),
            Type::array(Type::array_key(), Type::string()),
        ]))],
    );
    assert!(resolved.compatible);
    assert_eq!(resolved.return_type.text(), "Product|string");
}

#[test]
fn resolve_generic_signature_projects_subclass_to_generic_parameter() {
    let snapshot = Snapshot::new(
        1,
        &[document(
            "/extensions.php",
            vec![
                with_templates(
                    sym("extension", SymbolKind::Class, "Extension", "Extension"),
                    vec![template("T")],
                ),
                with_hierarchy(
                    sym(
                        "child",
                        SymbolKind::Class,
                        "ProductExtension",
                        "ProductExtension",
                    ),
                    &["Extension"],
                    &[],
                    &[],
                    vec![generic("Extension", vec![named("Product")])],
                    Vec::new(),
                ),
            ],
        )],
    );
    let symbol = with_templates(
        Symbol {
            kind: SymbolKind::Method,
            name: "publish".into(),
            return_type: Type::template("T"),
            parameters: vec![param(
                "$extension",
                generic("Extension", vec![Type::template("T")]),
            )],
            ..Symbol::default()
        },
        vec![template("T")],
    );
    let resolved = resolve_signature(
        snapshot.relations(),
        symbol,
        &[Argument::of(named("ProductExtension"))],
    );
    assert!(resolved.compatible);
    assert_eq!(resolved.return_type.text(), "Product");
}

#[test]
fn member_resolution_specializes_complete_signature() {
    let snapshot = Snapshot::new(
        1,
        &[document(
            "/box.php",
            vec![
                with_templates(
                    sym("box", SymbolKind::Class, "Box", "Box"),
                    vec![template("T")],
                ),
                Symbol {
                    return_type: Type::template("T"),
                    parameters: vec![param("$value", Type::template("T"))],
                    ..member(
                        "replace",
                        SymbolKind::Method,
                        "replace",
                        "Box::replace",
                        "box",
                    )
                },
            ],
        )],
    );
    let resolved =
        MemberResolver::new(&snapshot).methods(&generic("Box", vec![Type::string()]), "replace");
    assert_eq!(resolved.len(), 1);
    assert_eq!(resolved[0].symbol.parameters[0].ty.text(), "string");
    assert_eq!(resolved[0].symbol.return_type.text(), "string");
    let retained = snapshot.symbol("replace").unwrap();
    assert_eq!(retained.parameters[0].ty.text(), "T");
    assert_eq!(retained.return_type.text(), "T");
    assert!(
        resolve_signature(
            snapshot.relations(),
            resolved[0].symbol.clone(),
            &[Argument::of(Type::string())],
        )
        .compatible
    );
    assert!(
        !resolve_signature(
            snapshot.relations(),
            resolved[0].symbol.clone(),
            &[Argument::of(Type::int())],
        )
        .compatible
    );
}

#[test]
fn bound_class_template_does_not_fall_back_to_default() {
    let snapshot = Snapshot::new(
        1,
        &[document(
            "/generic-box.php",
            vec![
                with_templates(
                    sym("generic-box", SymbolKind::Class, "GenericBox", "GenericBox"),
                    vec![template_with("T", Type::UNKNOWN, Type::string())],
                ),
                Symbol {
                    return_type: Type::template("T"),
                    ..member(
                        "generic-box-value",
                        SymbolKind::Method,
                        "value",
                        "GenericBox::value",
                        "generic-box",
                    )
                },
            ],
        )],
    );
    let methods = MemberResolver::new(&snapshot)
        .methods(&generic("GenericBox", vec![Type::template("T")]), "value");
    assert_eq!(methods.len(), 1);
    assert_eq!(methods[0].symbol.templates().len(), 1);
    assert!(methods[0].symbol.templates()[0].default.is_unknown());
    assert_eq!(
        resolve_signature(snapshot.relations(), methods[0].symbol.clone(), &[])
            .return_type
            .text(),
        "T"
    );
}

#[test]
fn resolve_signature_honors_bounds_defaults_and_variadics() {
    let mut symbol = Symbol {
        kind: SymbolKind::Function,
        name: "collect".into(),
        return_type: Type::template("T"),
        parameters: vec![Parameter {
            flags: Flags::VARIADIC,
            ..param("$values", Type::template("T"))
        }],
        ..Symbol::default()
    };
    symbol.set_templates(vec![template_with(
        "T",
        named("Entity"),
        named("DefaultEntity"),
    )]);
    let hierarchy = TestHierarchy(HashMap::from([
        ("Product", "Entity"),
        ("DefaultEntity", "Entity"),
    ]));
    let relations = Relations::new(&hierarchy);
    let resolved = resolve_signature(
        relations,
        symbol.clone(),
        &[
            Argument::of(named("Product")),
            Argument::of(named("Product")),
        ],
    );
    assert!(resolved.compatible);
    assert_eq!(resolved.return_type.text(), "Product");

    let invalid = resolve_signature(relations, symbol.clone(), &[Argument::of(Type::string())]);
    assert!(!invalid.compatible);

    let mut with_default = symbol;
    with_default.parameters[0].optional = true;
    let resolved = resolve_signature(relations, with_default, &[]);
    assert!(resolved.compatible);
    assert_eq!(resolved.return_type.text(), "DefaultEntity");
}

#[test]
fn resolve_signature_treats_mixed_argument_as_not_provably_invalid() {
    let resolved = resolve_signature(
        Relations::default(),
        Symbol {
            parameters: vec![param("$value", Type::string())],
            return_type: Type::string(),
            ..Symbol::default()
        },
        &[Argument::of(Type::mixed())],
    );
    assert!(resolved.compatible);
}

#[test]
fn resolve_signature_accepts_already_bound_class_template() {
    let template_type = Type::template("TElement");
    let resolved = resolve_signature(
        Relations::default(),
        with_templates(
            Symbol {
                kind: SymbolKind::Method,
                name: "add".into(),
                return_type: Type::void(),
                parameters: vec![param("$entity", template_type.clone())],
                ..Symbol::default()
            },
            vec![template_with("TElement", named("Entity"), Type::UNKNOWN)],
        ),
        &[Argument::of(template_type.clone())],
    );
    assert!(resolved.compatible);
    assert_eq!(resolved.templates.get("TElement"), Some(&template_type));
}

#[test]
fn resolve_signature_infers_templates_from_callable_and_list() {
    let entity_callable =
        |result: Type| Type::callable(vec![CallableParameter::of(named("Entity"))], result);
    let callable = with_templates(
        Symbol {
            parameters: vec![param("$callback", entity_callable(Type::template("T")))],
            return_type: Type::array(Type::string(), Type::template("T")),
            ..Symbol::default()
        },
        vec![template_with("T", Type::mixed(), Type::UNKNOWN)],
    );
    let resolved = resolve_signature(
        Relations::default(),
        callable,
        &[Argument::of(entity_callable(Type::string()))],
    );
    assert!(resolved.compatible);
    assert_eq!(resolved.return_type.text(), "array<string,string>");

    let array_filter = with_templates(
        Symbol {
            parameters: vec![param(
                "$array",
                Type::array(Type::template("TKey"), Type::template("TValue")),
            )],
            return_type: Type::array(Type::template("TKey"), Type::template("TValue")),
            ..Symbol::default()
        },
        vec![
            template_with("TKey", Type::array_key(), Type::UNKNOWN),
            template_with("TValue", Type::mixed(), Type::UNKNOWN),
        ],
    );
    let resolved = resolve_signature(
        Relations::default(),
        array_filter,
        &[Argument::of(Type::list(Type::string()))],
    );
    assert!(resolved.compatible);
    assert_eq!(resolved.return_type.text(), "array<int,string>");

    let fmap = with_templates(
        Symbol {
            parameters: vec![param(
                "$callback",
                entity_callable(Type::union([
                    Type::template("T"),
                    Type::false_type(),
                    Type::null(),
                ])),
            )],
            return_type: Type::array(Type::string(), Type::template("T")),
            ..Symbol::default()
        },
        vec![template_with("T", Type::mixed(), Type::UNKNOWN)],
    );
    let resolved = resolve_signature(
        Relations::default(),
        fmap,
        &[Argument::of(entity_callable(
            Type::nullable(Type::string()),
        ))],
    );
    assert!(resolved.compatible);
    assert_eq!(resolved.return_type.text(), "array<string,string>");

    let uncertain_array = with_templates(
        Symbol {
            parameters: vec![param(
                "$values",
                Type::array(Type::array_key(), Type::template("T")),
            )],
            return_type: Type::void(),
            ..Symbol::default()
        },
        vec![template_with("T", Type::string(), Type::UNKNOWN)],
    );
    let resolved = resolve_signature(
        Relations::default(),
        uncertain_array,
        &[Argument::of(Type::array(Type::int(), Type::mixed()))],
    );
    assert!(resolved.compatible);
    assert!(!resolved.templates.contains_key("T"));
}

#[test]
fn resolve_signature_maps_named_arguments() {
    let symbol = Symbol {
        kind: SymbolKind::Function,
        name: "format".into(),
        return_type: Type::string(),
        parameters: vec![
            param("$first", Type::string()),
            Parameter {
                optional: true,
                ..param("$second", Type::int())
            },
            Parameter {
                flags: Flags::VARIADIC,
                ..param("$rest", Type::string())
            },
        ],
        ..Symbol::default()
    };
    let relations = Relations::default();
    let resolved = resolve_signature(
        relations,
        symbol.clone(),
        &[
            Argument::named("second", Type::int()),
            Argument::named("$first", Type::string()),
            Argument::named("unknown", Type::string()),
        ],
    );
    assert!(resolved.compatible);
    assert!(resolved.templates.is_empty());

    let first_twice = resolve_signature(
        relations,
        symbol.clone(),
        &[
            Argument::named("first", Type::string()),
            Argument::named("$first", Type::string()),
        ],
    );
    assert!(!first_twice.compatible);
    assert_eq!(
        first_twice.mismatch,
        "parameter #1 ($first) is passed more than once"
    );
    let positional_after_named = resolve_signature(
        relations,
        symbol.clone(),
        &[
            Argument::named("first", Type::string()),
            Argument::of(Type::int()),
        ],
    );
    assert_eq!(
        positional_after_named.mismatch,
        "positional argument after named argument"
    );
    let missing = resolve_signature(
        relations,
        symbol.clone(),
        &[Argument::named("second", Type::int())],
    );
    assert_eq!(
        missing.mismatch,
        "missing argument for parameter #1 ($first)"
    );

    let mut without_variadic = symbol;
    without_variadic.parameters.truncate(2);
    let unknown = resolve_signature(
        relations,
        without_variadic,
        &[Argument::named("unknown", Type::string())],
    );
    assert!(!unknown.compatible);
    assert_eq!(unknown.mismatch, "unknown named argument $unknown");
}

#[test]
fn resolve_signature_tracks_more_than_sixty_four_parameters() {
    let parameters = (0..65)
        .map(|index| Parameter {
            optional: true,
            ..param(&format!("$p{index}"), Type::int())
        })
        .collect();
    let resolved = resolve_signature(
        Relations::default(),
        Symbol {
            kind: SymbolKind::Function,
            name: "large".into(),
            return_type: Type::void(),
            parameters,
            ..Symbol::default()
        },
        &[Argument::named("p64", Type::int())],
    );
    assert!(resolved.compatible);
}

#[test]
fn partially_defaulted_class_templates_remain_inferable() {
    let snapshot = Snapshot::new(
        1,
        &[document(
            "/pair.php",
            vec![
                with_templates(
                    sym("pair", SymbolKind::Class, "Pair", "Pair"),
                    vec![
                        template_with("T", Type::object(), Type::UNKNOWN),
                        template_with("TLabel", Type::UNKNOWN, Type::string()),
                    ],
                ),
                Symbol {
                    parameters: vec![
                        param("$value", Type::template("T")),
                        Parameter {
                            optional: true,
                            ..param("$label", Type::template("TLabel"))
                        },
                    ],
                    ..member(
                        "construct",
                        SymbolKind::Method,
                        "__construct",
                        "Pair::__construct",
                        "pair",
                    )
                },
            ],
        )],
    );
    let constructor = MemberResolver::new(&snapshot).methods(&named("Pair"), "__construct");
    assert_eq!(constructor.len(), 1);
    assert_eq!(constructor[0].symbol.templates().len(), 2);
    let resolved = resolve_signature(
        snapshot.relations(),
        constructor[0].symbol.clone(),
        &[Argument::of(named("Product"))],
    );
    assert!(resolved.compatible);
    assert_eq!(resolved.templates["T"].text(), "Product");
    assert_eq!(resolved.templates["TLabel"].text(), "string");
}

#[test]
fn method_template_shadows_class_template() {
    let snapshot = Snapshot::new(
        1,
        &[document(
            "/container.php",
            vec![
                with_templates(
                    sym("container", SymbolKind::Class, "Container", "Container"),
                    vec![template("T")],
                ),
                with_templates(
                    Symbol {
                        return_type: Type::template("T"),
                        parameters: vec![param("$value", Type::template("T"))],
                        ..member(
                            "convert",
                            SymbolKind::Method,
                            "convert",
                            "Container::convert",
                            "container",
                        )
                    },
                    vec![template("T")],
                ),
            ],
        )],
    );
    let method = MemberResolver::new(&snapshot)
        .methods(&generic("Container", vec![named("Product")]), "convert");
    assert_eq!(method.len(), 1);
    let resolved = resolve_signature(
        snapshot.relations(),
        method[0].symbol.clone(),
        &[Argument::of(Type::string())],
    );
    assert!(resolved.compatible);
    assert_eq!(resolved.return_type.text(), "string");
}

#[test]
fn colliding_method_template_is_renamed() {
    let snapshot = Snapshot::new(
        1,
        &[document(
            "/wrapper.php",
            vec![
                with_templates(
                    sym("wrapper", SymbolKind::Class, "Wrapper", "Wrapper"),
                    vec![template("TValue")],
                ),
                with_templates(
                    Symbol {
                        return_type: generic(
                            "Pair",
                            vec![Type::template("TValue"), Type::template("T")],
                        ),
                        parameters: vec![param("$value", Type::template("T"))],
                        ..member(
                            "with",
                            SymbolKind::Method,
                            "with",
                            "Wrapper::with",
                            "wrapper",
                        )
                    },
                    vec![template("T")],
                ),
            ],
        )],
    );
    // The receiver binds the class template to the caller's own template T,
    // which collides with the method template T.
    let methods = MemberResolver::new(&snapshot)
        .methods(&generic("Wrapper", vec![Type::template("T")]), "with");
    assert_eq!(methods.len(), 1);
    let symbol = &methods[0].symbol;
    assert_eq!(symbol.return_type.text(), "Pair<T,TMethod>");
    assert_eq!(symbol.parameters[0].ty.text(), "TMethod");
    let names: Vec<&str> = symbol.templates().iter().map(|t| &*t.name).collect();
    assert_eq!(names, ["TValue", "TMethod"]);
    let resolved = resolve_signature(
        snapshot.relations(),
        symbol.clone(),
        &[Argument::of(Type::int())],
    );
    assert_eq!(resolved.return_type.text(), "Pair<T,int>");
}

#[test]
fn member_resolution_specializes_method_template_bound() {
    let snapshot = Snapshot::new(
        1,
        &[document(
            "/collection.php",
            vec![
                with_templates(
                    sym("collection", SymbolKind::Class, "Collection", "Collection"),
                    vec![template("TElement")],
                ),
                with_templates(
                    Symbol {
                        return_type: Type::nullable(Type::template("T")),
                        parameters: vec![param(
                            "$predicate",
                            Type::callable(
                                vec![CallableParameter::of(Type::template("T"))],
                                Type::bool(),
                            ),
                        )],
                        ..member(
                            "first-where",
                            SymbolKind::Method,
                            "firstWhere",
                            "Collection::firstWhere",
                            "collection",
                        )
                    },
                    vec![template_with(
                        "T",
                        Type::template("TElement"),
                        Type::UNKNOWN,
                    )],
                ),
            ],
        )],
    );
    let methods = MemberResolver::new(&snapshot).methods(
        &generic("Collection", vec![named("Delivery")]),
        "firstWhere",
    );
    assert_eq!(methods.len(), 1);
    assert_eq!(methods[0].symbol.templates().len(), 2);
    assert_eq!(methods[0].symbol.templates()[1].bound.text(), "Delivery");
    let resolved = resolve_signature(
        snapshot.relations(),
        methods[0].symbol.clone(),
        &[Argument::of(Type::callable(
            vec![CallableParameter::of(named("Delivery"))],
            Type::bool(),
        ))],
    );
    assert!(resolved.compatible);
    assert_eq!(resolved.return_type.text(), "Delivery|null");
}

#[test]
fn constant_types_and_all_members_skip_type_aliases() {
    let snapshot = Snapshot::new(
        1,
        &[document(
            "/status.php",
            vec![
                sym("status", SymbolKind::Enum, "Status", "Status"),
                Symbol {
                    ty: named("Status"),
                    ..member(
                        "open",
                        SymbolKind::EnumCase,
                        "Open",
                        "Status::Open",
                        "status",
                    )
                },
                Symbol {
                    ty: Type::literal_string("x"),
                    ..member(
                        "prefix",
                        SymbolKind::ClassConstant,
                        "PREFIX",
                        "Status::PREFIX",
                        "status",
                    )
                },
                member(
                    "alias",
                    SymbolKind::TypeAlias,
                    "Row",
                    "Status::Row",
                    "status",
                ),
            ],
        )],
    );
    let resolver = MemberResolver::new(&snapshot);
    let receiver = named("Status");
    assert_eq!(
        resolver.constant_types(&receiver, "Open")[0].text(),
        "Status"
    );
    assert_eq!(
        resolver.constant_types(&receiver, "PREFIX")[0].kind(),
        Kind::LiteralString
    );
    let kinds: Vec<Kind> = resolver
        .all(&receiver)
        .iter()
        .map(|member| member.ty.kind())
        .collect();
    assert_eq!(kinds.len(), 2);
    assert!(MemberResolver::default().methods(&receiver, "x").is_empty());
}
