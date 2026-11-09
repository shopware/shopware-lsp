//! Snapshot-level checks walk
//! members directly; the `MemberResolver`/`ResolveSignature` assertions
//! live in the `resolver` module.

use std::collections::HashSet;
use std::sync::Arc;

use swlsp_php_semantic::{
    AttributeValueKind, CallReturnKind, CallTarget, Snapshot, SymbolKind, attribute_named,
    deprecation_of,
};
use swlsp_php_types::Kind;

use super::*;

fn version(major: i64, minor: i64) -> Version {
    Version::new(major, minor, 0)
}

fn snapshot(document: Document) -> Arc<Snapshot> {
    Snapshot::new(1, &[document])
}

/// Members named `name` declared by `class` or, when absent, by its nearest
/// supertypes (a breadth-first stand-in for `MemberResolver`).
fn inherited_members(snapshot: &Snapshot, class: &str, name: &str) -> Vec<Symbol> {
    let mut queue: Vec<Symbol> = snapshot.classes(class);
    let mut seen = HashSet::new();
    while !queue.is_empty() {
        let mut found = Vec::new();
        let mut next = Vec::new();
        for class in queue {
            if !seen.insert(class.id.clone()) {
                continue;
            }
            found.extend(snapshot.members(&class.id, name));
            next.extend(snapshot.direct_supertypes(&class.id));
        }
        if !found.is_empty() {
            return found;
        }
        queue = next;
    }
    Vec::new()
}

fn member_type(symbol: &Symbol) -> String {
    match symbol.kind {
        SymbolKind::Method | SymbolKind::Function => symbol.return_type.text().into_owned(),
        _ => symbol.ty.text().into_owned(),
    }
}

#[test]
fn versioned_stub_document() {
    let snapshot = snapshot(document(version(8, 3)));
    assert_eq!(snapshot.functions("json_validate").len(), 1);
    assert!(
        self::snapshot(document(version(8, 2)))
            .functions("json_validate")
            .is_empty()
    );

    let format = inherited_members(&snapshot, "DateTimeImmutable", "format");
    assert_eq!(format.len(), 1);
    assert_eq!(member_type(&format[0]), "string");
    for class in [
        "DateTime",
        "DateTimeZone",
        "DateInterval",
        "ArrayIterator",
        "SplFileInfo",
        "SplFileObject",
        "ReflectionNamedType",
        "ReflectionParameter",
        "Pdo\\Mysql",
        "DOMElement",
        "DOMDocument",
        "GdImage",
        "Redis",
        "RedisException",
        "Imagick",
        "ImagickPixel",
        "NumberFormatter",
        "XMLReader",
        "ZipArchive",
        "ReflectionClass",
        "JsonException",
    ] {
        assert_eq!(snapshot.classes(class).len(), 1, "{class}");
    }
    let trace = inherited_members(&snapshot, "Throwable", "getTrace");
    assert_eq!(trace.len(), 1);
    assert_eq!(
        member_type(&trace[0]),
        "list<array{args?:list,class?:class-string,file?:string,function:string,line?:int,object?:object,type?:string,...}>"
    );
    for (class, method) in [
        ("DateTimeImmutable", "__construct"),
        ("DateTimeImmutable", "add"),
        ("DateTime", "setTime"),
        ("DateTime", "createFromImmutable"),
        ("DateTimeImmutable", "createFromInterface"),
        ("DateTimeImmutable", "setTimezone"),
        ("Closure", "bind"),
        ("ReflectionProperty", "getType"),
        ("ReflectionMethod", "getDeclaringClass"),
        ("ReflectionMethod", "getAttributes"),
        ("ReflectionEnum", "getBackingType"),
        ("ReflectionFunction", "getClosureThis"),
        ("Throwable", "getPrevious"),
        ("SplFileObject", "seek"),
        ("ReflectionClass", "getAttributes"),
        ("ReflectionAttribute", "newInstance"),
    ] {
        assert_eq!(
            inherited_members(&snapshot, class, method).len(),
            1,
            "{class}::{method}"
        );
    }
    let get_attributes = inherited_members(&snapshot, "ReflectionClass", "getAttributes");
    assert_eq!(
        member_type(&get_attributes[0]),
        "list<ReflectionAttribute<TAttribute>>"
    );
    assert_eq!(get_attributes[0].templates()[0].name, "TAttribute");
    assert_eq!(
        member_type(&inherited_members(&snapshot, "ReflectionAttribute", "newInstance")[0]),
        "TAttribute"
    );
    let bind = &inherited_members(&snapshot, "Closure", "bind")[0];
    assert!(bind.flags.has(Flags::STATIC));
    assert_eq!(bind.templates()[0].name, "TClosure");

    let message = inherited_members(&snapshot, "RuntimeException", "message");
    assert_eq!(message.len(), 1);
    assert_eq!(message[0].visibility, Visibility::Protected);
    for (class, property, expected) in [
        ("DOMElement", "localName", "null|string"),
        ("DOMDocument", "encoding", "null|string"),
        ("ZipArchive", "filename", "string"),
        ("ReflectionObject", "name", "string"),
    ] {
        let properties = inherited_members(&snapshot, class, property);
        assert_eq!(properties.len(), 1, "{class}::${property}");
        assert_eq!(
            member_type(&properties[0]),
            expected,
            "{class}::${property}"
        );
    }
    for (class, constant) in [
        ("DateTimeInterface", "ATOM"),
        ("PDO", "ATTR_PERSISTENT"),
        ("Pdo\\Mysql", "ATTR_COMPRESS"),
    ] {
        assert_eq!(
            inherited_members(&snapshot, class, constant).len(),
            1,
            "{class}::{constant}"
        );
    }
    let array_iterator = snapshot.classes("ArrayIterator");
    let templates: Vec<&str> = array_iterator[0]
        .templates()
        .iter()
        .map(|template| template.name.as_str())
        .collect();
    assert_eq!(templates, ["TKey", "TValue"]);

    assert_eq!(snapshot.functions("json_decode")[0].parameters.len(), 4);
    assert_eq!(snapshot.functions("json_encode")[0].parameters.len(), 3);
    assert_eq!(snapshot.functions("array_filter")[0].parameters.len(), 3);
    assert_eq!(snapshot.functions("substr")[0].return_type.text(), "string");
    assert_eq!(
        snapshot.functions("strpos")[0].return_type.text(),
        "false|int"
    );
    assert_eq!(
        snapshot.functions("mb_substr")[0].return_type.text(),
        "string"
    );
    assert_eq!(
        snapshot.functions("mb_strpos")[0].return_type.text(),
        "false|int"
    );
    let parse_string = snapshot.functions("parse_str");
    assert_eq!(parse_string.len(), 1);
    assert!(parse_string[0].parameters[1].flags.has(Flags::BY_REFERENCE));
    let preg_match_all = snapshot.functions("preg_match_all");
    assert_eq!(preg_match_all.len(), 1);
    assert!(
        preg_match_all[0].parameters[2]
            .flags
            .has(Flags::BY_REFERENCE)
    );
    assert!(snapshot.is_subtype_of("ArrayObject", "Countable"));
}

#[test]
fn project_stub_document_loads_selected_extension_bundles() {
    let version = version(8, 3);
    let none: [&str; 0] = [];
    let core = snapshot(document_for_extensions(version, &none, &[]));
    assert_eq!(core.functions("strlen").len(), 1);
    assert_eq!(core.functions("json_encode").len(), 1);
    assert!(core.functions("curl_init").is_empty());
    assert!(core.classes("Redis").is_empty());
    assert!(core.classes("Imagick").is_empty());

    let selected = snapshot(document_for_extensions(version, &["curl", "redis"], &[]));
    assert_eq!(selected.functions("curl_init").len(), 1);
    assert_eq!(selected.classes("Redis").len(), 1);
    assert!(selected.classes("Imagick").is_empty());

    assert_eq!(extension_for_symbol("DOMDocument"), Some("dom"));
    assert_eq!(extension_for_symbol("curl_init"), Some("curl"));
    assert_eq!(extension_for_symbol("\\DOMNode::appendChild"), Some("dom"));
    assert_eq!(extension_for_symbol("not_a_runtime_symbol"), None);

    let selected_document = document_for_extensions(version, &["curl", "redis"], &[]);
    assert!(selected_document.symbols.len() < document(version).symbols.len());
    let owners: HashSet<&SymbolId> = selected_document
        .symbols
        .iter()
        .filter(|symbol| symbol.is_class_like())
        .map(|symbol| &symbol.id)
        .collect();
    for symbol in &selected_document.symbols {
        if !symbol.container.is_empty() {
            assert!(
                owners.contains(&symbol.container),
                "orphan selected-extension member {}",
                symbol.fully_qualified
            );
        }
    }
}

#[test]
fn selected_extensions_honor_disabled_dependencies() {
    let selected =
        selected_extensions(&["dom", "xsl", "pdo_sqlite", "curl"], &[&["libxml", "pdo"]]);
    assert!(selected.iter().any(|extension| extension == "curl"));
    for missing in ["libxml", "dom", "xsl", "pdo", "pdo_sqlite"] {
        assert!(
            !selected.iter().any(|extension| extension == missing),
            "{missing}"
        );
    }
    let mut sorted = selected.clone();
    sorted.sort();
    assert_eq!(selected, sorted);
}

#[test]
fn selected_extensions_keep_always_loaded_bundles() {
    let selected = selected_extensions(&["Ext-PDO-SQLite"], &[&["core", "json"]]);
    for expected in ["core", "json", "pdo", "pdo_sqlite", "sqlite3", "spl"] {
        assert!(
            selected.iter().any(|extension| extension == expected),
            "{expected}"
        );
    }
}

#[test]
fn generated_phpstorm_stub_coverage() {
    let document = document(version(8, 3));
    let snapshot = snapshot(document.clone());
    for function in [
        "array_combine",
        "array_map",
        "curl_init",
        "random_int",
        "str_replace",
    ] {
        assert_eq!(snapshot.functions(function).len(), 1, "{function}");
    }
    assert!(
        snapshot.functions("array_map")[0]
            .flags
            .has(Flags::GENERATED_STUB)
    );
    assert!(
        !snapshot.functions("array_filter")[0]
            .flags
            .has(Flags::GENERATED_STUB)
    );
    for class in ["Phar", "Random\\Randomizer", "SoapClient", "mysqli"] {
        assert_eq!(snapshot.classes(class).len(), 1, "{class}");
    }

    let mut seen = HashSet::new();
    let mut class_ids = HashSet::new();
    for symbol in &document.symbols {
        assert!(
            seen.insert(&symbol.id),
            "duplicate generated/overlay symbol {}",
            symbol.fully_qualified
        );
        if symbol.is_class_like() {
            class_ids.insert(&symbol.id);
        }
    }
    for symbol in &document.symbols {
        if !symbol.container.is_empty() {
            assert!(
                class_ids.contains(&symbol.container),
                "orphan generated member {}",
                symbol.fully_qualified
            );
        }
    }
}

#[test]
fn generated_phpstorm_stub_version_availability() {
    let php81 = snapshot(document(version(8, 1)));
    let php82 = snapshot(document(version(8, 2)));
    assert!(php81.classes("Random\\Randomizer").is_empty());
    assert_eq!(php82.classes("Random\\Randomizer").len(), 1);
    assert!(php82.functions("array_last").is_empty());
    let php85 = snapshot(document(version(8, 5)));
    assert_eq!(php85.functions("array_last").len(), 1);
}

#[test]
fn overlay_declarations_follow_the_php_version() {
    let php80 = snapshot(document(version(8, 0)));
    let php83 = snapshot(document(version(8, 3)));
    assert!(
        php80
            .classes("Fiber")
            .iter()
            .all(|fiber| php80.members(&fiber.id, "start").is_empty())
    );
    assert_eq!(inherited_members(&php83, "Fiber", "start").len(), 1);
    assert_eq!(php83.classes("Override").len(), 1);
}

#[test]
fn generated_phpstorm_attribute_semantics() {
    let snapshot = snapshot(document(version(8, 3)));
    let pathinfo = snapshot.functions("pathinfo");
    assert!(!pathinfo.is_empty());
    let result = &pathinfo[0].return_type;
    assert_eq!(result.kind(), Kind::Union);
    assert!(
        (0..result.argument_count()).any(|index| result.argument(index).kind() == Kind::ArrayShape)
    );
    assert_eq!(pathinfo[0].parameters.len(), 2);
    let expected = attribute_named(&pathinfo[0].parameters[1].attributes, "ExpectedValues")
        .expect("ExpectedValues attribute");
    let flags = expected.argument("flags", 1).expect("flags argument");
    assert_eq!(flags.kind, AttributeValueKind::Array);
    assert!(!flags.items.is_empty());

    let gettimeofday = snapshot.functions("gettimeofday");
    assert!(!gettimeofday.is_empty());
    assert_eq!(gettimeofday[0].parameters.len(), 1);
}

#[test]
fn generated_phpstorm_stub_versioned_deprecations() {
    let php82 = snapshot(document(version(8, 2)));
    let php85 = snapshot(document(version(8, 5)));
    let php86 = snapshot(document(version(8, 6)));

    assert!(
        !php82.functions("curl_close")[0]
            .flags
            .has(Flags::DEPRECATED)
    );
    let curl_close = &php85.functions("curl_close")[0];
    assert!(curl_close.flags.has(Flags::DEPRECATED));
    let deprecation = deprecation_of(curl_close.attributes()).expect("deprecation");
    assert_eq!(deprecation.reason, "Deprecated: it has no effect");
    assert_eq!(deprecation.since, "8.5");
    assert!(
        !php85.functions("mb_eregi_replace")[0]
            .flags
            .has(Flags::DEPRECATED)
    );
    assert!(
        php86.functions("mb_eregi_replace")[0]
            .flags
            .has(Flags::DEPRECATED)
    );
}

#[test]
fn generated_phpstorm_stub_provenance() {
    let (repository, commit) = generated_source();
    assert_eq!(
        repository,
        "https://github.com/JetBrains/phpstorm-stubs.git"
    );
    assert_eq!(commit, "b872a0404e009a42cd4ff77a54505e01b2f126e3");
}

#[test]
fn generated_phpstorm_call_contracts() {
    let contracts = generated_contracts();
    assert!(!contracts.is_empty());

    let mut array_pop = false;
    let mut append_child = false;
    let mut extension_loaded = false;
    let mut dd_exit = false;
    let mut mockery_map = false;
    for contract in &contracts {
        if contract.target == CallTarget::function("array_pop") {
            array_pop = contract.return_contract.kind == CallReturnKind::ArgumentElementType
                && contract.return_contract.argument == 0;
        } else if contract.target == CallTarget::method("DOMNode", "appendChild") {
            append_child = contract.return_contract.kind == CallReturnKind::ArgumentType
                && contract.return_contract.argument == 0;
        } else if contract.target == CallTarget::function("extension_loaded") {
            extension_loaded |= contract
                .expected_arguments
                .iter()
                .any(|expected| expected.argument == 0 && expected.values.len() > 20);
        } else if contract.target == CallTarget::function("dd") {
            dd_exit = contract.exit_point;
        } else if contract.target == CallTarget::method("Mockery", "mock") {
            mockery_map = contract.return_contract.kind == CallReturnKind::ArgumentMap;
        }
    }
    assert!(array_pop);
    assert!(append_child);
    assert!(extension_loaded);
    assert!(dd_exit);
    assert!(mockery_map);
}

#[test]
fn stub_document_snapshot_accepts_document() {
    // The stub document must be valid input for workspace snapshots: every
    // path is the document path and IDs are unique.
    let document = document(version(8, 3));
    assert!(document.path.starts_with("phpstub://8.3.0/"));
    assert!(
        document
            .symbols
            .iter()
            .all(|symbol| symbol.path == document.path)
    );
}

mod resolver {
    //! `MemberResolver` and `resolve_signature` over stub declarations.

    use swlsp_php_resolver::{Argument, MemberResolver, resolve_signature};

    use super::*;

    fn named(name: &str, arguments: Vec<Type>) -> Type {
        Type::named(name, arguments)
    }

    #[test]
    fn member_resolver_sees_stub_members() {
        let snapshot = snapshot(document(version(8, 3)));
        let members = MemberResolver::new(&snapshot);

        let format = members.methods(&named("DateTimeImmutable", vec![]), "format");
        assert_eq!(format.len(), 1);
        assert_eq!(format[0].ty.text(), "string");
        let trace = members.methods(&named("Throwable", vec![]), "getTrace");
        assert_eq!(trace.len(), 1);
        assert_eq!(
            trace[0].ty.text(),
            "list<array{args?:list,class?:class-string,file?:string,function:string,line?:int,object?:object,type?:string,...}>"
        );
        for (class, method) in [
            ("DateTimeImmutable", "__construct"),
            ("DateTimeImmutable", "add"),
            ("DateTime", "setTime"),
            ("DateTime", "createFromImmutable"),
            ("DateTimeImmutable", "createFromInterface"),
            ("DateTimeImmutable", "setTimezone"),
            ("Closure", "bind"),
            ("ReflectionProperty", "getType"),
            ("ReflectionMethod", "getDeclaringClass"),
            ("ReflectionMethod", "getAttributes"),
            ("ReflectionEnum", "getBackingType"),
            ("ReflectionFunction", "getClosureThis"),
            ("Throwable", "getPrevious"),
            ("SplFileObject", "seek"),
        ] {
            assert_eq!(
                members.methods(&named(class, vec![]), method).len(),
                1,
                "{class}::{method}"
            );
        }

        let get_attributes = members.methods(
            &named("ReflectionClass", vec![Type::object()]),
            "getAttributes",
        );
        assert_eq!(get_attributes.len(), 1);
        let resolved = resolve_signature(
            snapshot.relations(),
            get_attributes[0].symbol.clone(),
            &[Argument::of(Type::class_string(named(
                "ProductAttribute",
                vec![],
            )))],
        );
        assert!(resolved.compatible);
        assert_eq!(
            resolved.return_type.text(),
            "list<ReflectionAttribute<ProductAttribute>>"
        );
        let instance = members.methods(
            &named(
                "ReflectionAttribute",
                vec![named("ProductAttribute", vec![])],
            ),
            "newInstance",
        );
        assert_eq!(instance.len(), 1);
        assert_eq!(instance[0].ty.text(), "ProductAttribute");

        let message = members.properties(&named("RuntimeException", vec![]), "message");
        assert_eq!(message.len(), 1);
        assert_eq!(message[0].symbol.visibility, Visibility::Protected);
        for (class, property, expected) in [
            ("DOMElement", "localName", "null|string"),
            ("DOMDocument", "encoding", "null|string"),
            ("ZipArchive", "filename", "string"),
            ("ReflectionObject", "name", "string"),
        ] {
            let properties = members.properties(&named(class, vec![]), property);
            assert_eq!(properties.len(), 1, "{class}::${property}");
            assert_eq!(properties[0].ty.text(), expected, "{class}::${property}");
        }
        for (class, constant) in [
            ("DateTimeInterface", "ATOM"),
            ("PDO", "ATTR_PERSISTENT"),
            ("Pdo\\Mysql", "ATTR_COMPRESS"),
        ] {
            assert_eq!(
                members
                    .constant_types(&named(class, vec![]), constant)
                    .len(),
                1,
                "{class}::{constant}"
            );
        }

        let constructor = members.methods(
            &named(
                "ArrayIterator",
                vec![Type::template("TKey"), Type::template("TValue")],
            ),
            "__construct",
        );
        assert_eq!(constructor.len(), 1);
        let resolved = resolve_signature(
            snapshot.relations(),
            constructor[0].symbol.clone(),
            &[Argument::of(Type::list(Type::string()))],
        );
        assert!(resolved.compatible);
        assert_eq!(resolved.templates["TValue"].text(), "string");
    }

    #[test]
    fn stub_function_signatures_resolve() {
        let snapshot = snapshot(document(version(8, 3)));
        let function = |name: &str| snapshot.functions(name).remove(0);
        assert!(
            resolve_signature(
                snapshot.relations(),
                function("class_exists"),
                &[Argument::of(Type::string())],
            )
            .compatible
        );
        assert!(
            resolve_signature(
                snapshot.relations(),
                function("is_a"),
                &[
                    Argument::of(Type::string()),
                    Argument::of(Type::string()),
                    Argument::of(Type::bool()),
                ],
            )
            .compatible
        );
        assert!(
            resolve_signature(
                snapshot.relations(),
                function("preg_match_all"),
                &[
                    Argument::of(Type::literal_string("/pattern/")),
                    Argument::of(Type::string()),
                    Argument::of(Type::unknown()),
                ],
            )
            .compatible
        );
    }

    #[test]
    fn gettimeofday_conditional_return() {
        let snapshot = snapshot(document(version(8, 3)));
        let gettimeofday = snapshot.functions("gettimeofday").remove(0);
        let true_result = resolve_signature(
            snapshot.relations(),
            gettimeofday.clone(),
            &[Argument::of(Type::true_type())],
        );
        assert_eq!(true_result.return_type.text(), "float");
        let false_result = resolve_signature(
            snapshot.relations(),
            gettimeofday.clone(),
            &[Argument::of(Type::false_type())],
        );
        assert_eq!(false_result.return_type.kind(), Kind::ArrayShape);
        let omitted = resolve_signature(snapshot.relations(), gettimeofday, &[]);
        assert_eq!(omitted.return_type.kind(), Kind::ArrayShape);
    }
}
