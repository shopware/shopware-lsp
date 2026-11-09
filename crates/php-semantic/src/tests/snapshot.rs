use super::*;

fn view_ids(views: &[SymbolView<'_>]) -> Vec<String> {
    views.iter().map(|view| view.id().to_owned()).collect()
}

#[test]
fn document_overlay_replaces_persisted_symbols() {
    let persisted = document(
        "/file.php",
        vec![symbol(
            "old",
            SymbolKind::Class,
            "Old",
            "App\\Old",
            "/file.php",
        )],
    );
    let overlay = document(
        "/file.php",
        vec![symbol(
            "new",
            SymbolKind::Class,
            "New",
            "App\\New",
            "/file.php",
        )],
    );
    let snapshot = Snapshot::new(1, &[persisted]).with_document(&overlay);
    assert!(snapshot.classes("App\\Old").is_empty());
    assert_eq!(snapshot.classes("App\\New").len(), 1);
    assert!(snapshot.symbol("old").is_none());
    assert!(snapshot.has_document("/file.php"));
}

#[test]
fn call_contracts_respect_multiple_document_overlays() {
    let contract = |argument: u16| CallContract {
        target: CallTarget::function("make"),
        return_contract: CallReturnContract {
            kind: CallReturnKind::ArgumentType,
            argument,
            ..Default::default()
        },
        ..CallContract::default()
    };
    let snapshot = Snapshot::new(
        1,
        &[
            Document {
                path: "/one.php".into(),
                call_contracts: vec![contract(1)],
                ..Document::default()
            },
            Document {
                path: "/two.php".into(),
                call_contracts: vec![contract(2)],
                ..Document::default()
            },
        ],
    );
    let all = |snapshot: &Snapshot| {
        let mut arguments = Vec::new();
        snapshot.visit_function_call_contracts("\\MAKE", |current| {
            arguments.push(current.return_contract.argument);
            true
        });
        arguments
    };
    assert_eq!(all(&snapshot), [1, 2]);
    let snapshot = snapshot.with_document(&document("/one.php", Vec::new()));
    let snapshot = snapshot.with_document(&Document {
        path: "/two.php".into(),
        call_contracts: vec![contract(3)],
        ..Document::default()
    });
    assert_eq!(all(&snapshot), [3]);
}

#[test]
fn method_call_contracts_apply_to_subtypes() {
    let mut child = symbol("child", SymbolKind::Class, "Child", "App\\Child", "/c.php");
    child.set_extends(names(&["App\\Base"]));
    let contract = CallContract {
        target: CallTarget::method("App\\Base", "get"),
        return_contract: CallReturnContract {
            kind: CallReturnKind::ArgumentType,
            ..Default::default()
        },
        ..CallContract::default()
    };
    let snapshot = Snapshot::new(
        1,
        &[
            document(
                "/b.php",
                vec![symbol(
                    "base",
                    SymbolKind::Class,
                    "Base",
                    "App\\Base",
                    "/b.php",
                )],
            ),
            Document {
                call_contracts: vec![contract],
                ..document("/c.php", vec![child])
            },
        ],
    );
    let mut count = 0;
    snapshot.visit_method_call_contracts(&named("App\\Child"), "GET", |_| {
        count += 1;
        true
    });
    assert_eq!(count, 1);
    snapshot.visit_method_call_contracts(&named("App\\Other"), "get", |_| {
        count += 1;
        true
    });
    assert_eq!(count, 1);
}

#[test]
fn global_symbols_exclude_members_and_respect_overlays() {
    let old_class = symbol(
        "old-class",
        SymbolKind::Class,
        "Old",
        "App\\Old",
        "/class.php",
    );
    let old_method = member(
        "old-method",
        SymbolKind::Method,
        "run",
        "old-class",
        "/class.php",
    );
    let function = symbol(
        "function",
        SymbolKind::Function,
        "helper",
        "App\\helper",
        "/function.php",
    );
    let new_class = symbol(
        "new-class",
        SymbolKind::Class,
        "New",
        "App\\New",
        "/class.php",
    );
    let new_property = member(
        "new-property",
        SymbolKind::Property,
        "$value",
        "new-class",
        "/class.php",
    );
    let snapshot = Snapshot::new(
        1,
        &[
            document("/class.php", vec![old_class, old_method]),
            document("/function.php", vec![function.clone(), function]),
        ],
    )
    .with_document(&document("/class.php", vec![new_class, new_property]));
    let mut globals = ids(&snapshot.global_symbols());
    globals.sort();
    assert_eq!(globals, ["function", "new-class"]);
    let classes = snapshot.global_class_views();
    assert_eq!(view_ids(&classes), ["new-class"]);
}

#[test]
fn declaration_overlay_does_not_pack_references() {
    let target = symbol(
        "target",
        SymbolKind::Class,
        "Target",
        "App\\Target",
        "/target.php",
    );
    let overlay = Snapshot::new(1, &[]).with_declarations(&Document {
        references: vec![Reference {
            resolved: "target".into(),
            range: range(2, 8),
            ..Reference::default()
        }],
        ..document("/target.php", vec![target])
    });
    assert_eq!(overlay.classes("App\\Target").len(), 1);
    assert!(overlay.references_to("target").is_empty());
    assert!(overlay.has_document("/target.php"));
}

#[test]
fn snapshot_does_not_expose_locals_as_class_members() {
    let class = symbol("class", SymbolKind::Class, "C", "C", "/c.php");
    let method = member("method", SymbolKind::Method, "run", "class", "/c.php");
    let parameter = member(
        "parameter",
        SymbolKind::Parameter,
        "$input",
        "method",
        "/c.php",
    );
    let snapshot = Snapshot::new(1, &[document("/c.php", vec![class, method, parameter])]);
    assert_eq!(snapshot.members("class", "run").len(), 1);
    assert_eq!(snapshot.members("class", "RUN").len(), 1);
    assert!(snapshot.members("method", "$input").is_empty());
}

#[test]
fn snapshot_deduplicates_repeated_index_entries() {
    let class = symbol(
        "class",
        SymbolKind::Class,
        "Service",
        "App\\Service",
        "/service.php",
    );
    let method = member("method", SymbolKind::Method, "run", "class", "/service.php");
    let alternate = Symbol {
        id: "alternate-method".into(),
        ..method.clone()
    };
    let document = document("/service.php", vec![class, method, alternate]);
    let snapshot = Snapshot::new(1, &[document.clone(), document]);
    assert_eq!(snapshot.class_views("App\\Service").len(), 1);
    assert_eq!(
        view_ids(&snapshot.member_views("class", "run")),
        ["method", "alternate-method"]
    );
    assert_eq!(view_ids(&snapshot.global_symbol_views()), ["class"]);
    assert_eq!(snapshot.all_symbols().len(), 3);
}

#[test]
fn snapshot_indexes_only_winning_duplicate_declaration() {
    let original = symbol(
        "service",
        SymbolKind::Class,
        "OldService",
        "App\\OldService",
        "/old.php",
    );
    let replacement = symbol(
        "service",
        SymbolKind::Class,
        "NewService",
        "App\\NewService",
        "/new.php",
    );
    let snapshot = Snapshot::new(
        1,
        &[
            document("/old.php", vec![original]),
            document("/new.php", vec![replacement]),
        ],
    );
    assert!(snapshot.class_views("App\\OldService").is_empty());
    let classes = snapshot.class_views("App\\NewService");
    assert_eq!(classes.len(), 1);
    assert_eq!(classes[0].path(), "/new.php");
}

#[test]
fn shared_declaration_id_resolves_to_the_last_document() {
    // A runtime stub and a phar polyfill both declare `\Attribute` with the
    // same ID; name and global lookups resolve through the symbol view of that ID,
    // so the declaration of the later document wins everywhere.
    let declaration = |path: &str, flags: Flags| Symbol {
        flags,
        ..symbol(
            "1:attribute",
            SymbolKind::Class,
            "Attribute",
            "Attribute",
            path,
        )
    };
    let stub = document(
        "phpstub://8.2.0/selected",
        vec![declaration("phpstub://8.2.0/selected", Flags::INTERNAL)],
    );
    let polyfill = document(
        "/project/vendor/polyfill/Attribute.php",
        vec![declaration(
            "/project/vendor/polyfill/Attribute.php",
            Flags::empty(),
        )],
    );
    for (documents, winner) in [
        (
            [stub.clone(), polyfill.clone()],
            "/project/vendor/polyfill/Attribute.php",
        ),
        ([polyfill.clone(), stub.clone()], "phpstub://8.2.0/selected"),
    ] {
        let snapshot = Snapshot::new(1, &documents);
        let paths = |views: Vec<SymbolView<'_>>| -> Vec<String> {
            views.iter().map(|view| view.path().to_owned()).collect()
        };
        assert_eq!(paths(snapshot.class_views("Attribute")), [winner]);
        assert_eq!(paths(snapshot.global_class_views()), [winner]);
        assert_eq!(snapshot.classes("\\Attribute")[0].path, winner);
        assert_eq!(snapshot.symbol("1:attribute").unwrap().path, winner);
        let mut visited = Vec::new();
        snapshot.visit_class_views("Attribute", |view| {
            visited.push(view.path().to_owned());
            true
        });
        assert_eq!(visited, [winner]);
    }
}

#[test]
fn name_index_keeps_distinct_alternates() {
    let first = symbol(
        "first",
        SymbolKind::Class,
        "Service",
        "App\\Service",
        "/a.php",
    );
    let second = symbol(
        "second",
        SymbolKind::Class,
        "Service",
        "app\\SERVICE",
        "/b.php",
    );
    let snapshot = Snapshot::new(
        1,
        &[
            document("/a.php", vec![first]),
            document("/b.php", vec![second]),
        ],
    );
    assert_eq!(
        view_ids(&snapshot.class_views("\\APP\\service")),
        ["first", "second"]
    );
    let overlay = snapshot.with_document(&document(
        "/c.php",
        vec![symbol(
            "third",
            SymbolKind::Class,
            "S",
            "App\\Service",
            "/c.php",
        )],
    ));
    assert_eq!(
        view_ids(&overlay.class_views("App\\Service")),
        ["third", "first", "second"]
    );
}

#[test]
fn constants_are_case_sensitive() {
    let constant = symbol(
        "constant",
        SymbolKind::GlobalConstant,
        "VERSION",
        "App\\VERSION",
        "/c.php",
    );
    let snapshot = Snapshot::new(1, &[document("/c.php", vec![constant])]);
    assert_eq!(snapshot.constants("\\App\\VERSION").len(), 1);
    assert!(snapshot.constants("App\\version").is_empty());
}

#[test]
fn visitors_match_slice_lookups() {
    let class = symbol(
        "class",
        SymbolKind::Class,
        "Service",
        "App\\Service",
        "/service.php",
    );
    let method = member("method", SymbolKind::Method, "run", "class", "/service.php");
    let alternate = Symbol {
        id: "alternate-method".into(),
        ..method.clone()
    };
    let function = symbol(
        "function",
        SymbolKind::Function,
        "build",
        "App\\build",
        "/service.php",
    );
    let constant = symbol(
        "constant",
        SymbolKind::GlobalConstant,
        "VERSION",
        "App\\VERSION",
        "/service.php",
    );
    let document = document(
        "/service.php",
        vec![class, method, alternate, function, constant],
    );
    for snapshot in [
        Snapshot::new(1, std::slice::from_ref(&document)),
        Snapshot::new(1, std::slice::from_ref(&document)).with_declarations(&document),
    ] {
        let mut visited = Vec::new();
        assert!(snapshot.visit_class_views("App\\Service", |view| {
            visited.push(view.id().to_owned());
            true
        }));
        assert_eq!(visited, view_ids(&snapshot.class_views("App\\Service")));
        let mut visited = Vec::new();
        snapshot.visit_function_views("app\\BUILD", |view| {
            visited.push(view.id().to_owned());
            true
        });
        assert_eq!(visited, view_ids(&snapshot.function_views("App\\build")));
        assert_eq!(visited, ["function"]);
        let mut visited = Vec::new();
        snapshot.visit_constant_views("App\\VERSION", |view| {
            visited.push(view.id().to_owned());
            true
        });
        assert_eq!(visited, ["constant"]);
        let mut visited = Vec::new();
        snapshot.visit_member_views("class", "run", |view| {
            visited.push(view.id().to_owned());
            true
        });
        assert_eq!(visited, view_ids(&snapshot.member_views("class", "run")));
        assert_eq!(visited, ["method", "alternate-method"]);
        let mut count = 0;
        assert!(!snapshot.visit_member_views("class", "run", |_| {
            count += 1;
            false
        }));
        assert_eq!(count, 1);
    }
}

#[test]
fn member_views_of_are_sorted() {
    let class = symbol("class", SymbolKind::Class, "C", "C", "/c.php");
    let mut members = vec![
        Symbol {
            range: range(50, 60),
            ..member("zeta", SymbolKind::Method, "zeta", "class", "/c.php")
        },
        Symbol {
            range: range(10, 20),
            ..member("alpha", SymbolKind::Method, "Alpha", "class", "/c.php")
        },
        Symbol {
            range: range(5, 6),
            ..member("prop", SymbolKind::Property, "$beta", "class", "/c.php")
        },
        Symbol {
            range: range(1, 2),
            ..member("const", SymbolKind::ClassConstant, "Z", "class", "/c.php")
        },
    ];
    members.insert(0, class);
    let snapshot = Snapshot::new(1, &[document("/c.php", members.clone())]);
    assert_eq!(
        view_ids(&snapshot.member_views_of("class")),
        ["alpha", "zeta", "prop", "const"]
    );
    let overlay = snapshot.with_document(&document("/other.php", Vec::new()));
    assert_eq!(
        view_ids(&overlay.member_views_of("class")),
        ["alpha", "zeta", "prop", "const"]
    );
    assert_eq!(overlay.members_of("class").len(), 4);
    assert_eq!(snapshot.members("class", "$BETA").len(), 1);
}

#[test]
fn updated_symbol_overlay_is_immutable_and_reuses_indexes() {
    let mut method = symbol(
        "method",
        SymbolKind::Function,
        "value",
        "value",
        "/value.php",
    );
    let snapshot = Snapshot::new(1, &[document("/value.php", vec![method.clone()])]);
    method.return_type = Type::string();
    let updated = snapshot.with_updated_symbols(&document("/value.php", vec![method.clone()]));
    assert!(snapshot.symbol("method").unwrap().return_type.is_unknown());
    let resolved = updated.functions("value");
    assert_eq!(resolved.len(), 1);
    assert_eq!(resolved[0].return_type.to_string(), "string");
    assert!(std::sync::Arc::ptr_eq(&snapshot.layer, &updated.layer));
    let missing = updated.with_updated_function_returns(&document(
        "/value.php",
        vec![symbol(
            "absent",
            SymbolKind::Function,
            "absent",
            "absent",
            "/value.php",
        )],
    ));
    assert!(missing.symbol("absent").is_none());
}

#[test]
fn override_symbol_updates_unique_handles_in_place() {
    let mut method = symbol(
        "method",
        SymbolKind::Function,
        "value",
        "value",
        "/value.php",
    );
    let base = Snapshot::new(1, &[document("/value.php", vec![method.clone()])]);
    let mut shared = base.clone();
    method.return_type = Type::string();
    Snapshot::override_symbol(&mut shared, &method);
    assert!(!std::sync::Arc::ptr_eq(&base, &shared));
    assert!(base.symbol("method").unwrap().return_type.is_unknown());
    assert_eq!(
        shared.functions("value")[0].return_type.to_string(),
        "string"
    );
    assert!(std::sync::Arc::ptr_eq(&base.layer, &shared.layer));

    let unique = std::sync::Arc::as_ptr(&shared);
    method.return_type = Type::int();
    Snapshot::override_symbol(&mut shared, &method);
    assert_eq!(std::sync::Arc::as_ptr(&shared), unique);
    assert_eq!(
        shared.symbol("method").unwrap().return_type.to_string(),
        "int"
    );

    let absent = symbol(
        "absent",
        SymbolKind::Function,
        "absent",
        "absent",
        "/value.php",
    );
    Snapshot::override_symbol(&mut shared, &absent);
    assert!(shared.symbol("absent").is_none());
}

#[test]
fn symbols_in_path_respect_overlays() {
    let snapshot = Snapshot::new(
        1,
        &[document(
            "/a.php",
            vec![symbol("a", SymbolKind::Class, "A", "A", "/a.php")],
        )],
    );
    assert_eq!(ids(&snapshot.symbols_in("/a.php")), ["a"]);
    let overlay = snapshot.with_document(&document(
        "/a.php",
        vec![symbol("b", SymbolKind::Class, "B", "B", "/a.php")],
    ));
    assert_eq!(ids(&overlay.symbols_in("/a.php")), ["b"]);
    let other = overlay.with_document(&document("/z.php", Vec::new()));
    assert_eq!(ids(&other.symbols_in("/a.php")), ["b"]);
    assert!(other.symbols_in("/missing.php").is_empty());
    let cloned = other.clone_symbols();
    assert!(cloned.contains_key("b"));
    assert!(!cloned.contains_key("a"));
}

#[test]
fn symbol_view_attributes_preserve_snapshot_layers() {
    let mut base_symbol = symbol("target", SymbolKind::Function, "target", "", "/base.php");
    base_symbol.set_attributes(vec![Attribute {
        name: "BaseAttribute".into(),
        ..Attribute::default()
    }]);
    let base = Snapshot::new(1, &[document("/base.php", vec![base_symbol])]);
    assert_eq!(
        &*base.symbol_view("target").unwrap().attributes()[0].name,
        "BaseAttribute"
    );
    let mut open = symbol(
        "open-target",
        SymbolKind::Function,
        "openTarget",
        "",
        "/open.php",
    );
    open.set_attributes(vec![Attribute {
        name: "OpenAttribute".into(),
        ..Attribute::default()
    }]);
    let overlay = base.with_document(&document("/open.php", vec![open]));
    assert_eq!(
        &*overlay.symbol_view("open-target").unwrap().attributes()[0].name,
        "OpenAttribute"
    );
}

#[test]
fn relations_treat_class_names_case_insensitively() {
    let snapshot = Snapshot::new(
        1,
        &[document(
            "/product.php",
            vec![symbol(
                "product",
                SymbolKind::Class,
                "Product",
                "Shopware\\Core\\Product",
                "/product.php",
            )],
        )],
    );
    assert!(snapshot.relations().is_subtype(
        &named("SHOPWARE\\CORE\\PRODUCT"),
        &named("shopware\\core\\product")
    ));
}

#[test]
fn snapshot_projects_generic_supertypes() {
    let mut base = symbol("base", SymbolKind::Class, "Producer", "Producer", "");
    base.set_templates(vec![TemplateParameter {
        name: "T".into(),
        covariant: true,
        ..TemplateParameter::default()
    }]);
    let entity = symbol("entity", SymbolKind::Class, "Entity", "Entity", "");
    let mut product = symbol("product", SymbolKind::Class, "Product", "Product", "");
    product.set_extends(names(&["Entity"]));
    let mut child = symbol(
        "child",
        SymbolKind::Class,
        "ProductProducer",
        "ProductProducer",
        "",
    );
    child.set_hierarchy(
        names(&["Producer"]),
        Vec::new(),
        Vec::new(),
        vec![Type::named("Producer", vec![named("Product")])],
        Vec::new(),
        Vec::new(),
        Vec::new(),
    );
    let snapshot = Snapshot::new(
        1,
        &[document("/types.php", vec![base, entity, product, child])],
    );
    assert!(snapshot.relations().is_subtype(
        &named("ProductProducer"),
        &Type::named("Producer", vec![named("Entity")])
    ));
    let projected = snapshot
        .as_supertype(&named("ProductProducer"), "Producer")
        .unwrap();
    assert_eq!(projected.to_string(), "Producer<Product>");
    assert_eq!(
        snapshot.template_variance("Producer", 0),
        swlsp_php_types::Variance::Covariant
    );
    assert_eq!(
        snapshot.template_variance("Producer", 3),
        swlsp_php_types::Variance::Invariant
    );
}

#[test]
fn snapshot_projects_explicit_generic_through_concrete_collection_subclasses() {
    let mut iterator = symbol(
        "iterator",
        SymbolKind::Interface,
        "IteratorAggregate",
        "IteratorAggregate",
        "",
    );
    iterator.set_templates(vec![
        TemplateParameter {
            name: "TKey".into(),
            ..TemplateParameter::default()
        },
        TemplateParameter {
            name: "TValue".into(),
            ..TemplateParameter::default()
        },
    ]);
    let mut collection = symbol(
        "collection",
        SymbolKind::Class,
        "Collection",
        "Collection",
        "",
    );
    collection.set_templates(vec![TemplateParameter {
        name: "TElement".into(),
        ..TemplateParameter::default()
    }]);
    collection.set_hierarchy(
        Vec::new(),
        names(&["IteratorAggregate"]),
        Vec::new(),
        Vec::new(),
        vec![Type::named(
            "IteratorAggregate",
            vec![Type::array_key(), Type::template("TElement")],
        )],
        Vec::new(),
        Vec::new(),
    );
    let mut field_collection = symbol(
        "field-collection",
        SymbolKind::Class,
        "FieldCollection",
        "FieldCollection",
        "",
    );
    field_collection.set_hierarchy(
        names(&["Collection"]),
        Vec::new(),
        Vec::new(),
        vec![Type::named("Collection", vec![named("Field")])],
        Vec::new(),
        Vec::new(),
        Vec::new(),
    );
    let mut compiled = symbol(
        "compiled",
        SymbolKind::Class,
        "CompiledFieldCollection",
        "CompiledFieldCollection",
        "",
    );
    compiled.set_extends(names(&["FieldCollection"]));
    let snapshot = Snapshot::new(
        1,
        &[document(
            "/collections.php",
            vec![iterator, collection, field_collection, compiled],
        )],
    );
    let projected = snapshot
        .as_supertype(
            &Type::named(
                "CompiledFieldCollection",
                vec![named("ManyToManyAssociationField")],
            ),
            "IteratorAggregate",
        )
        .unwrap();
    assert_eq!(
        projected.to_string(),
        "IteratorAggregate<array-key,ManyToManyAssociationField>"
    );
}

#[test]
fn callable_signature_follows_invoke_and_parents() {
    let mut invokable = symbol(
        "invokable",
        SymbolKind::Class,
        "Handler",
        "App\\Handler",
        "/h.php",
    );
    invokable.set_templates(vec![TemplateParameter {
        name: "T".into(),
        ..TemplateParameter::default()
    }]);
    let mut invoke = member(
        "invoke",
        SymbolKind::Method,
        "__invoke",
        "invokable",
        "/h.php",
    );
    invoke.parameters = vec![Parameter {
        name: "$value".into(),
        ty: Type::template("T"),
        ..Parameter::default()
    }];
    invoke.return_type = Type::template("T");
    let mut child = symbol("child", SymbolKind::Class, "Child", "App\\Child", "/c.php");
    child.set_hierarchy(
        names(&["App\\Handler"]),
        Vec::new(),
        Vec::new(),
        vec![Type::named("App\\Handler", vec![Type::string()])],
        Vec::new(),
        Vec::new(),
        Vec::new(),
    );
    let snapshot = Snapshot::new(
        1,
        &[
            document("/h.php", vec![invokable, invoke]),
            document("/c.php", vec![child]),
        ],
    );
    let signature = snapshot.callable_signature(&named("App\\Child")).unwrap();
    assert_eq!(signature.result().to_string(), "string");
    assert_eq!(signature.parameters()[0].ty.to_string(), "string");
    assert!(
        snapshot
            .callable_signature(&named("App\\Missing"))
            .is_none()
    );
}

#[test]
fn resolve_type_alias_uses_synthetic_member() {
    let class = symbol(
        "class",
        SymbolKind::Class,
        "Shapes",
        "App\\Shapes",
        "/s.php",
    );
    let alias = Symbol {
        ty: Type::array(Type::string(), Type::int()),
        ..member("alias", SymbolKind::TypeAlias, "Map", "class", "/s.php")
    };
    let snapshot = Snapshot::new(1, &[document("/s.php", vec![class, alias])]);
    let resolved = snapshot
        .resolve_type_alias(&swlsp_php_types::phpdoc_alias("App\\Shapes", "Map"))
        .unwrap();
    assert_eq!(resolved, Type::array(Type::string(), Type::int()));
    assert!(snapshot.resolve_type_alias(&named("App\\Shapes")).is_none());
}

#[test]
fn snapshot_is_shareable_across_threads() {
    fn assert_send_sync<T: Send + Sync>() {}
    assert_send_sync::<Snapshot>();
    assert_send_sync::<Store>();
    assert_send_sync::<WorkspaceGraph>();
}
