use std::sync::atomic::{AtomicUsize, Ordering};
use std::sync::{Arc, Mutex};

use swlsp_php_types::must_parse;

use super::*;

fn rich_symbol() -> Symbol {
    let mut symbol = Symbol {
        id: "method".into(),
        kind: SymbolKind::Method,
        name: "find".into(),
        fully_qualified: "App\\Repository::find".into(),
        container: "repository".into(),
        path: "/repository.php".into(),
        range: range(10, 80),
        selection_range: range(20, 24),
        body_range: range(30, 79),
        visibility: Visibility::Protected,
        write_visibility: Visibility::Private,
        has_write_visibility: true,
        flags: Flags::STATIC | Flags::FINAL | Flags::SOFT_FINAL,
        ty: must_parse("callable(int): Product"),
        native_type: must_parse("Product"),
        doc_type: must_parse("Product|null"),
        return_type: must_parse("Product"),
        parameters: vec![Parameter {
            id: "parameter".into(),
            name: "$id".into(),
            ty: Type::int(),
            native_type: Type::int(),
            doc_type: must_parse("positive-int"),
            assistant_tags: names(&["Autowire"]),
            attributes: vec![Attribute {
                name: "SensitiveParameter".into(),
                arguments: vec![AttributeArgument {
                    name: "flag".into(),
                    value: AttributeValue {
                        kind: AttributeValueKind::Array,
                        expression: "['a' => 1]".into(),
                        items: vec![AttributeArrayItem {
                            key: AttributeValue {
                                kind: AttributeValueKind::String,
                                value: "a".into(),
                                ..AttributeValue::default()
                            },
                            has_key: true,
                            value: AttributeValue {
                                kind: AttributeValueKind::Integer,
                                value: "1".into(),
                                ..AttributeValue::default()
                            },
                        }],
                        ..AttributeValue::default()
                    },
                    range: range(3, 4),
                }],
                range: range(1, 5),
            }],
            default_value: Some(Box::new(AttributeValue {
                kind: AttributeValueKind::Null,
                value: "null".into(),
                expression: "null".into(),
                ..AttributeValue::default()
            })),
            range: range(25, 29),
            selection_range: range(26, 28),
            default_range: range(28, 29),
            flags: Flags::BY_REFERENCE,
            optional: true,
        }],
        ..Symbol::default()
    };
    symbol.set_signature_extras(
        vec![TemplateParameter {
            name: "T".into(),
            bound: named("Entity"),
            default: named("Product"),
            covariant: true,
            contravariant: false,
        }],
        vec![named("RuntimeException")],
        vec![TypeAssertion {
            target: "$this".into(),
            ty: named("App\\ProductRepository"),
            when_true: true,
            conditional: true,
            negated: true,
        }],
        vec![LiteralReturn {
            value: "null".into(),
            range: range(60, 64),
            ty: Type::null(),
        }],
        vec![ConstantReturn {
            receiver: "Product".into(),
            name: "DEFAULT".into(),
            range: range(65, 75),
        }],
    );
    symbol.set_hierarchy(
        names(&["App\\BaseRepository"]),
        names(&["App\\RepositoryInterface"]),
        names(&["App\\RepositoryTrait"]),
        vec![must_parse("App\\BaseRepository<Product>")],
        vec![must_parse("App\\RepositoryInterface<Product>")],
        vec![named("App\\RepositoryTrait")],
        vec![TraitAlias {
            trait_name: "App\\RepositoryTrait".into(),
            method: "find".into(),
            alias: "aliasedFind".into(),
            visibility: Visibility::Private,
            has_visibility: true,
        }],
    );
    symbol.set_metadata(
        vec![Attribute {
            name: "Deprecated".into(),
            range: range(1, 9),
            ..Attribute::default()
        }],
        vec![ConstantArrayItem {
            key: "key".into(),
            key_range: range(31, 34),
            value: "value".into(),
            value_range: range(36, 41),
            ty: Type::string(),
        }],
        "Finds a product.",
    );
    symbol
}

fn rich_document() -> Document {
    let mut reference = Reference {
        name: "Product".into(),
        kind: NameKind::Class,
        receiver: named("App\\Repository"),
        target_kind: SymbolKind::Class,
        is_static: true,
        write: true,
        range: range(50, 57),
        scope: 2,
        resolved: "product".into(),
        ..Reference::default()
    };
    reference.set_qualified_names(names(&["App\\Product"]));
    reference.set_candidate_ids(vec!["product".into(), "legacy-product".into()]);
    let contract = CallContract {
        target: CallTarget::method("App\\Repository", "find"),
        return_contract: CallReturnContract {
            kind: CallReturnKind::ArgumentMap,
            argument: 1,
            map: vec![CallMapEntry {
                key: CallValue {
                    kind: CallValueKind::String,
                    value: "a".into(),
                    expression: "'a'".into(),
                },
                result: CallValue {
                    kind: CallValueKind::ClassConstant,
                    value: "A::class".into(),
                    expression: "\\A::class".into(),
                },
            }],
        },
        expected_arguments: vec![ExpectedArgumentContract {
            argument: 2,
            values: vec![CallValue {
                kind: CallValueKind::Number,
                value: "1".into(),
                expression: "1".into(),
            }],
        }],
        expected_return_values: vec![CallValue {
            kind: CallValueKind::Constant,
            value: "X".into(),
            expression: "X".into(),
        }],
        exit_point: true,
        exit_arguments: vec![ExpectedArgumentContract {
            argument: 0,
            values: Vec::new(),
        }],
    };
    Document {
        path: "/repository.php".into(),
        version: 7,
        workspace_revision: 11,
        namespace: "App".into(),
        symbols: vec![rich_symbol()],
        references: vec![reference],
        call_contracts: vec![contract],
        ..Document::default()
    }
}

fn expected_round_trip(mut document: Document) -> Document {
    document.scopes.clear();
    for reference in &mut document.references {
        reference.scope = 0;
    }
    document
}

#[test]
fn workspace_document_round_trip_preserves_every_retained_symbol_field() {
    let document = rich_document();
    let expected = expected_round_trip(document.clone());
    let graph = WorkspaceGraph::pack_owned(document.clone());
    assert_eq!(graph.document(), expected);
    let encoded = graph.encode();
    let decoded = WorkspaceGraph::decode(&encoded).unwrap();
    assert_eq!(decoded.document(), expected);
    assert_eq!(decoded.encode(), encoded);
    // Canonicalized graphs are value-equal.
    let mut canonical = WorkspaceGraph::pack_owned(document);
    WorkspaceGraphDetacher::new().detach_owned(&mut canonical);
    assert_eq!(canonical.document(), expected);
}

#[test]
fn workspace_symbol_ranges_and_types_use_sparse_exact_fallbacks() {
    let symbols = vec![
        Symbol {
            range: range((1 << 20) + 10, (1 << 20) + 100),
            body_range: range((1 << 20) + 20, (1 << 20) + 99),
            flags: Flags::INTERNAL,
            ..symbol("compact", SymbolKind::Class, "", "", "/ranges.php")
        },
        Symbol {
            range: range(10, 200_000),
            selection_range: range(20, 25),
            body_range: range(30, 199_999),
            ..symbol("large", SymbolKind::Class, "", "", "/ranges.php")
        },
        Symbol {
            range: range(50, 60),
            selection_range: range(40, 45),
            ..symbol("before", SymbolKind::Class, "", "", "/ranges.php")
        },
        Symbol {
            ty: Type::int(),
            native_type: Type::string(),
            doc_type: Type::bool(),
            return_type: Type::float(),
            ..symbol("types", SymbolKind::Method, "", "", "/ranges.php")
        },
        Symbol {
            ty: Type::int(),
            native_type: Type::int(),
            doc_type: Type::string(),
            return_type: Type::int(),
            ..symbol("two-types", SymbolKind::Method, "", "", "/ranges.php")
        },
    ];
    let document = document("/ranges.php", symbols);
    let graph = WorkspaceGraph::pack_owned(document.clone());
    let packed = graph.packed();
    assert_eq!(packed.range_extras.len(), 2);
    assert_eq!(packed.type_extras.len(), 1);
    assert_eq!(graph.document().symbols, document.symbols);
    let decoded = WorkspaceGraph::decode(&graph.encode()).unwrap();
    assert_eq!(decoded.document().symbols, document.symbols);
    let snapshot = Snapshot::new(1, &[document]);
    let stats = snapshot.workspace_storage_stats();
    assert_eq!(stats.symbol_range_extras, 2);
    assert_eq!(stats.symbol_type_extras, 1);
}

#[test]
fn parameter_ids_derive_from_owner_with_exact_fallback() {
    let owner = new_symbol_id(SymbolKind::Method, "App\\Service::run", "/service.php", 10);
    let derived = new_symbol_id(
        SymbolKind::Parameter,
        &format!("{owner}:$id"),
        "/service.php",
        20,
    );
    let document = document(
        "/service.php",
        vec![Symbol {
            parameters: vec![
                Parameter {
                    id: derived.clone(),
                    name: "$id".into(),
                    ty: Type::int(),
                    native_type: Type::int(),
                    ..Parameter::default()
                },
                Parameter {
                    id: "legacy-parameter-id".into(),
                    name: "$legacy".into(),
                    ty: Type::string(),
                    native_type: Type::string(),
                    ..Parameter::default()
                },
                Parameter {
                    name: "$noId".into(),
                    ..Parameter::default()
                },
            ],
            ..symbol(
                &owner,
                SymbolKind::Method,
                "run",
                "App\\Service::run",
                "/service.php",
            )
        }],
    );
    let graph = WorkspaceGraph::pack_owned(document.clone());
    let detail = graph.packed().detail(0).unwrap();
    assert!(detail.parameters[0].extras.is_none());
    assert_eq!(
        detail.parameters[1].extras.as_ref().unwrap().id,
        Some("legacy-parameter-id".into())
    );
    assert!(detail.parameters[2].extras.is_none());
    // Go semantics: an empty parameter ID materializes as the derived ID.
    let mut expected = document;
    expected.symbols[0].parameters[2].id = format!("9:{owner}:$noid").into();
    assert_eq!(graph.document(), expected);
    assert_eq!(
        WorkspaceGraph::decode(&graph.encode()).unwrap().document(),
        expected
    );
}

struct CountingLoader {
    graph: Mutex<Option<Arc<WorkspaceGraph>>>,
    calls: AtomicUsize,
}

impl WorkspaceGraphLoader for CountingLoader {
    fn load_workspace_graph(&self, path: &str) -> Result<Arc<WorkspaceGraph>, LoadError> {
        self.calls.fetch_add(1, Ordering::SeqCst);
        let graph = self.graph.lock().unwrap().clone();
        graph
            .filter(|graph| graph.path() == path)
            .ok_or_else(|| LoadError::new(format!("missing {path}")))
    }
}

fn summary_fixture() -> (Document, Vec<u8>) {
    let mut symbol = Symbol {
        parameters: vec![Parameter {
            id: "9:app\\service::run:$value".into(),
            name: "$value".into(),
            ..Parameter::default()
        }],
        ..member(
            "App\\Service::run",
            SymbolKind::Method,
            "run",
            "App\\Service",
            "/service.php",
        )
    };
    symbol.fully_qualified = "App\\Service::run".into();
    symbol.set_extends(names(&["App\\Base"]));
    symbol.set_doc_summary("Runs the service.");
    let document = Document {
        references: vec![Reference {
            name: "Service".into(),
            resolved: symbol.id.clone(),
            ..Reference::default()
        }],
        ..document("/service.php", vec![symbol])
    };
    let encoded = WorkspaceGraph::pack_owned(document.clone()).encode();
    (document, encoded)
}

#[test]
fn summary_loads_symbol_details_on_demand() {
    let (document, encoded) = summary_fixture();
    let loader = Arc::new(CountingLoader {
        graph: Mutex::new(Some(Arc::new(WorkspaceGraph::decode(&encoded).unwrap()))),
        calls: AtomicUsize::new(0),
    });
    let mut decoder = WorkspaceGraphDecoder::new();
    let summary = decoder.decode_summary(&encoded, loader.clone()).unwrap();
    assert!(summary.is_summary());
    assert!(summary.packed().symbols[0].detail.is_none());
    assert!(summary.packed().symbols[0].hierarchy.is_some());
    assert_eq!(summary.reference_count(), 1);
    assert!(summary.visit_symbol_views(|view| {
        assert_eq!(view.id(), "App\\Service::run");
        assert_eq!(view.name(), "run");
        assert_eq!(view.kind(), SymbolKind::Method);
        assert_eq!(view.hierarchy_names().1, &names(&["App\\Base"])[..]);
        true
    }));
    let snapshot = Snapshot::from_packed(1, vec![Arc::new(summary.document)]);
    let _ = snapshot.workspace_storage_stats();
    assert_eq!(loader.calls.load(Ordering::SeqCst), 0);

    std::thread::scope(|scope| {
        for _ in 0..8 {
            scope.spawn(|| {
                let materialized = snapshot.symbol("App\\Service::run").unwrap();
                assert_eq!(materialized.parameters.len(), 1);
                assert_eq!(&*materialized.parameters[0].name, "$value");
                assert_eq!(materialized.doc_summary(), "Runs the service.");
                assert_eq!(materialized.extends(), &names(&["App\\Base"])[..]);
            });
        }
    });
    assert_eq!(loader.calls.load(Ordering::SeqCst), 8);
    assert_eq!(
        snapshot.symbol("App\\Service::run").unwrap(),
        document.symbols[0]
    );

    // Pinning keeps serving the old payload after the repository changes.
    let store_document = snapshot.packed().document(0);
    store_document.pin_details().unwrap();
    *loader.graph.lock().unwrap() = None;
    let calls = loader.calls.load(Ordering::SeqCst);
    assert_eq!(
        snapshot.symbol("App\\Service::run").unwrap().doc_summary(),
        "Runs the service."
    );
    assert_eq!(loader.calls.load(Ordering::SeqCst), calls);
}

#[test]
fn summary_degrades_when_loader_fails() {
    let (_, encoded) = summary_fixture();
    let loader = Arc::new(CountingLoader {
        graph: Mutex::new(None),
        calls: AtomicUsize::new(0),
    });
    let summary = WorkspaceGraphDecoder::new()
        .decode_summary(&encoded, loader)
        .unwrap();
    let materialized = summary.document().symbols.remove(0);
    assert!(materialized.parameters.is_empty());
    assert_eq!(materialized.extends(), &names(&["App\\Base"])[..]);
}

#[test]
fn summary_keeps_hierarchy_without_lazy_payload() {
    let mut symbol = symbol(
        "App\\Service",
        SymbolKind::Class,
        "Service",
        "App\\Service",
        "/service.php",
    );
    symbol.set_implements(names(&["App\\Contract"]));
    let encoded = WorkspaceGraph::pack_owned(document("/service.php", vec![symbol])).encode();
    let loader = Arc::new(CountingLoader {
        graph: Mutex::new(None),
        calls: AtomicUsize::new(0),
    });
    let summary = WorkspaceGraphDecoder::new()
        .decode_summary(&encoded, loader.clone())
        .unwrap();
    assert!(!summary.is_summary());
    assert_eq!(
        summary.document().symbols[0].implements(),
        &names(&["App\\Contract"])[..]
    );
    assert_eq!(loader.calls.load(Ordering::SeqCst), 0);
}

#[test]
fn store_ensures_document_details_before_replacement() {
    let (_, encoded) = summary_fixture();
    let loader = Arc::new(CountingLoader {
        graph: Mutex::new(Some(Arc::new(WorkspaceGraph::decode(&encoded).unwrap()))),
        calls: AtomicUsize::new(0),
    });
    let store = Store::new();
    let old = store
        .restore_workspace_graphs_decoded(WorkspaceRestoreCapacity::default(), |decoder, accept| {
            accept(decoder.decode_summary(&encoded, loader.clone())?);
            Ok::<(), DecodeError>(())
        })
        .unwrap();
    store.ensure_document_details("/service.php").unwrap();
    store.ensure_document_details("/missing.php").unwrap();
    *loader.graph.lock().unwrap() = None;
    store.replace(&document("/service.php", Vec::new()));
    assert_eq!(old.symbol("App\\Service::run").unwrap().parameters.len(), 1);
}

#[test]
fn decoder_rejects_invalid_input() {
    let encoded = WorkspaceGraph::pack_owned(rich_document()).encode();
    for length in 0..encoded.len() {
        assert!(
            WorkspaceGraph::decode(&encoded[..length]).is_err(),
            "prefix {length}"
        );
    }
    let mut trailing = encoded.clone();
    trailing.push(0);
    assert!(WorkspaceGraph::decode(&trailing).is_err());
    let mut version = encoded.clone();
    version[0] = 99;
    assert!(
        WorkspaceGraph::decode(&version)
            .unwrap_err()
            .to_string()
            .contains("version")
    );
    assert!(
        WorkspaceGraph::decode(&[
            1, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0x01
        ])
        .is_err()
    );
}

#[test]
fn decoder_shares_types_across_documents() {
    let first = WorkspaceGraph::pack_owned(Document {
        symbols: vec![Symbol {
            ty: must_parse("array<string, App\\Product>"),
            ..symbol("a", SymbolKind::Property, "$a", "", "/a.php")
        }],
        ..document("/a.php", Vec::new())
    })
    .encode();
    let second = WorkspaceGraph::pack_owned(Document {
        symbols: vec![Symbol {
            ty: must_parse("array<string, App\\Product>"),
            ..symbol("b", SymbolKind::Property, "$b", "", "/b.php")
        }],
        ..document("/b.php", Vec::new())
    })
    .encode();
    let mut decoder = WorkspaceGraphDecoder::new();
    let left = decoder.decode(&first).unwrap();
    let right = decoder.decode(&second).unwrap();
    let left_type = left.packed().types(0)[0].clone();
    let right_type = right.packed().types(0)[0].clone();
    assert!(left_type.ptr_eq(&right_type));
    decoder.clear();
}

#[test]
fn detacher_shares_values_across_batch() {
    let make = |path: &str| Document {
        symbols: vec![Symbol {
            ty: must_parse("list<App\\Item>"),
            ..symbol(
                &format!("{path}#p"),
                SymbolKind::Property,
                "$items",
                "",
                path,
            )
        }],
        ..document(path, Vec::new())
    };
    let mut detacher = WorkspaceGraphDetacher::with_capacity(4);
    let mut first = WorkspaceGraph::project_borrowed(&make("/one.php"));
    let mut second = WorkspaceGraph::project_borrowed(&make("/two.php"));
    assert!(!first.packed().types(0)[0].ptr_eq(&second.packed().types(0)[0]));
    detacher.detach_owned(&mut first);
    detacher.detach_owned(&mut second);
    assert!(first.packed().types(0)[0].ptr_eq(&second.packed().types(0)[0]));
}

#[test]
fn project_matches_two_stage_projection() {
    let mut full = rich_document();
    full.symbols.push(symbol(
        "local",
        SymbolKind::Local,
        "$x",
        "",
        "/repository.php",
    ));
    full.references.push(Reference {
        name: "$x".into(),
        kind: NameKind::Variable,
        ..Reference::default()
    });
    let direct = WorkspaceGraph::project(&full).document();
    let staged = WorkspaceGraph::pack_owned(full.workspace_graph()).document();
    assert_eq!(direct, staged);
    // The analysis generation is not part of a projection.
    assert_eq!(full.workspace_revision, 11);
    assert_eq!(direct.workspace_revision, 0);
    assert_eq!(direct.symbols.len(), 1);
    assert_eq!(direct.references.len(), 1);
}

#[test]
fn storage_stats_count_retained_tables() {
    let document = Document {
        references: vec![reference_with_targets(
            Reference {
                name: "run".into(),
                resolved: "service::run".into(),
                receiver: named("App\\Service"),
                kind: NameKind::Member,
                ..Reference::default()
            },
            &["App\\Service"],
            &["service::run"],
        )],
        ..document(
            "/service.php",
            vec![symbol(
                "service",
                SymbolKind::Class,
                "Service",
                "App\\Service",
                "/service.php",
            )],
        )
    };
    let snapshot = Snapshot::new(1, &[document]);
    let stats = snapshot.workspace_storage_stats();
    assert_eq!(stats.documents, 1);
    assert_eq!(stats.symbols, 1);
    assert_eq!(stats.references, 1);
    assert_eq!(stats.symbol_index_entries, 1);
    assert_eq!(stats.path_index_entries, 1);
    assert_eq!((stats.class_names, stats.class_ids), (1, 1));
    assert_eq!((stats.function_names, stats.constant_names), (0, 0));
    assert_eq!(
        (
            stats.member_containers,
            stats.member_names,
            stats.member_ids
        ),
        (0, 0, 0)
    );
    assert_eq!(stats.global_ids, 1);
    assert_eq!(stats.reference_strings, 3);
    assert_eq!(stats.reference_types, 1);
    assert_eq!(stats.reference_values, 2);
    assert!(stats.estimated_graph_bytes > 0);
    assert!(stats.atoms > 0);
}

#[test]
fn interner_grows_only_for_published_graphs() {
    // Unique names: the interner is process-wide and tests run in parallel.
    let fqn = "App\\InternerBoundOverlayOnly";
    let published_fqn = "App\\InternerBoundPublished";
    let base = Snapshot::new(
        1,
        &[document(
            "/published.php",
            vec![symbol(
                "interner-bound-published",
                SymbolKind::Class,
                "InternerBoundPublished",
                published_fqn,
                "/published.php",
            )],
        )],
    );
    assert!(crate::atom::is_interned(published_fqn));

    let open = document(
        "/open.php",
        vec![symbol(
            "interner-bound-overlay-only",
            SymbolKind::Class,
            "InternerBoundOverlayOnly",
            fqn,
            "/open.php",
        )],
    );
    let overlay = base.with_document(&open);
    assert_eq!(overlay.classes(fqn).len(), 1);
    let declarations = base.with_declarations(&open);
    assert_eq!(declarations.classes(fqn).len(), 1);
    assert!(
        !crate::atom::is_interned(fqn),
        "open-document overlays must not intern"
    );
    assert!(!crate::atom::is_interned("interner-bound-overlay-only"));
}
