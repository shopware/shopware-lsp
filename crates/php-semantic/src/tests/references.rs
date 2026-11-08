use std::sync::Arc;

use super::*;
use crate::resolve::reference_may_target;

#[test]
fn resolves_cross_file_references_from_semantic_names() {
    let class = symbol(
        "class",
        SymbolKind::Class,
        "Service",
        "App\\Service",
        "/service.php",
    );
    let method = member("method", SymbolKind::Method, "run", "class", "/service.php");
    let consumer = Document {
        path: "/consumer.php".into(),
        references: vec![
            reference_with_targets(
                Reference {
                    name: "Service".into(),
                    kind: NameKind::Class,
                    range: range(1, 8),
                    ..Reference::default()
                },
                &["App\\Service"],
                &[],
            ),
            Reference {
                name: "run".into(),
                kind: NameKind::Member,
                receiver: named("App\\Service"),
                target_kind: SymbolKind::Method,
                range: range(20, 23),
                ..Reference::default()
            },
        ],
        ..Document::default()
    };
    let snapshot = Snapshot::new(
        1,
        &[consumer, document("/service.php", vec![class, method])],
    );
    assert!(snapshot.reverse_reference_stats().is_none());
    assert_eq!(
        snapshot.references_to("class"),
        vec![ReferenceLocation {
            path: "/consumer.php".into(),
            range_start: 1,
            range_end: 8
        }]
    );
    assert_eq!(snapshot.references_to("method").len(), 1);
    assert_eq!(snapshot.reverse_reference_stats(), Some((2, 2)));
    assert!(snapshot.references_to("missing").is_empty());
}

#[test]
fn builds_reverse_references_once_concurrently() {
    let target = symbol(
        "target",
        SymbolKind::Class,
        "Target",
        "App\\Target",
        "/target.php",
    );
    let documents: Vec<Document> = std::iter::once(document("/target.php", vec![target]))
        .chain((0..20).map(|index| Document {
            path: format!("/consumer{index:02}.php").into(),
            references: vec![Reference {
                resolved: "target".into(),
                range: range(1, 5),
                ..Reference::default()
            }],
            ..Document::default()
        }))
        .collect();
    let snapshot = Snapshot::new(1, &documents);
    let threads: Vec<_> = (0..8)
        .map(|_| {
            let snapshot = snapshot.clone();
            std::thread::spawn(move || snapshot.references_to("target"))
        })
        .collect();
    for thread in threads {
        let locations = thread.join().unwrap();
        assert_eq!(locations.len(), 20);
        assert_eq!(&*locations[0].path, "/consumer00.php");
        assert_eq!(&*locations[19].path, "/consumer19.php");
    }
}

#[test]
fn member_references_follow_inheritance_traits_and_unions() {
    let parent = symbol(
        "parent",
        SymbolKind::Class,
        "ParentService",
        "App\\ParentService",
        "/parent.php",
    );
    let method = member(
        "execute",
        SymbolKind::Method,
        "execute",
        "parent",
        "/parent.php",
    );
    let mut child = symbol(
        "child",
        SymbolKind::Class,
        "ChildService",
        "App\\ChildService",
        "/child.php",
    );
    child.set_extends(names(&["App\\ParentService"]));
    let enumeration = symbol(
        "enum",
        SymbolKind::Enum,
        "Status",
        "App\\Status",
        "/status.php",
    );
    let case = member(
        "case",
        SymbolKind::EnumCase,
        "Active",
        "enum",
        "/status.php",
    );
    let consumer = Document {
        path: "/consumer.php".into(),
        references: vec![
            Reference {
                name: "EXECUTE".into(),
                kind: NameKind::Member,
                receiver: Type::union([named("App\\ChildService"), Type::null()]),
                target_kind: SymbolKind::Method,
                range: range(10, 17),
                ..Reference::default()
            },
            Reference {
                name: "Active".into(),
                kind: NameKind::Member,
                receiver: named("App\\Status"),
                target_kind: SymbolKind::ClassConstant,
                range: range(30, 36),
                ..Reference::default()
            },
        ],
        ..Document::default()
    };
    let snapshot = Snapshot::new(
        1,
        &[
            document("/parent.php", vec![parent, method]),
            document("/child.php", vec![child]),
            document("/status.php", vec![enumeration, case]),
            consumer,
        ],
    );
    assert_eq!(snapshot.references_to("execute").len(), 1);
    assert_eq!(snapshot.references_to("case").len(), 1);
}

#[test]
fn overlay_resolves_references_from_other_documents() {
    let consumer = Document {
        path: "/consumer.php".into(),
        references: vec![reference_with_targets(
            Reference {
                name: "Service".into(),
                kind: NameKind::Class,
                range: range(10, 17),
                ..Reference::default()
            },
            &["App\\Service"],
            &[],
        )],
        ..Document::default()
    };
    let snapshot = Snapshot::new(1, &[consumer]);
    let overlay = snapshot.with_document(&document(
        "/service.php",
        vec![symbol(
            "service",
            SymbolKind::Class,
            "Service",
            "App\\Service",
            "/service.php",
        )],
    ));
    assert_eq!(
        overlay.references_to("service"),
        vec![ReferenceLocation {
            path: "/consumer.php".into(),
            range_start: 10,
            range_end: 17
        }]
    );
    assert_eq!(overlay.layer.reference_queries.as_ref().unwrap().len(), 1);
    assert_eq!(overlay.references_to("service").len(), 1);
    assert_eq!(overlay.layer.reference_queries.as_ref().unwrap().len(), 1);
}

#[test]
fn document_overlay_replaces_reverse_references() {
    let id = new_symbol_id(SymbolKind::Class, "App\\Target", "/target.php", 0);
    let target = symbol(
        &id,
        SymbolKind::Class,
        "Target",
        "App\\Target",
        "/target.php",
    );
    let persisted = Document {
        path: "/consumer.php".into(),
        references: vec![Reference {
            resolved: id.clone(),
            range: range(2, 8),
            ..Reference::default()
        }],
        ..Document::default()
    };
    let snapshot = Snapshot::new(1, &[document("/target.php", vec![target]), persisted]);
    assert_eq!(snapshot.references_to(&id).len(), 1);
    let overlay = snapshot.with_document(&Document {
        path: "/consumer.php".into(),
        references: vec![Reference {
            resolved: id.clone(),
            range: range(20, 26),
            ..Reference::default()
        }],
        ..Document::default()
    });
    let mut references = overlay.references_to(&id);
    assert_eq!(references.len(), 1);
    assert_eq!(references[0].range_start, 20);
    references[0].range_start = 99;
    assert_eq!(overlay.references_to(&id)[0].range_start, 20);
}

#[test]
fn overlay_references_prefilter_unrelated_names() {
    let parent = symbol(
        "parent",
        SymbolKind::Class,
        "ParentService",
        "App\\ParentService",
        "/parent.php",
    );
    let method = member(
        "execute",
        SymbolKind::Method,
        "execute",
        "parent",
        "/parent.php",
    );
    let mut child = symbol(
        "child",
        SymbolKind::Class,
        "ChildService",
        "App\\ChildService",
        "/child.php",
    );
    child.set_extends(names(&["App\\ParentService"]));
    let target = symbol(
        "target",
        SymbolKind::Class,
        "Target",
        "App\\Target",
        "/target.php",
    );
    let consumer = Document {
        path: "/consumer.php".into(),
        references: vec![
            Reference {
                name: "execute".into(),
                kind: NameKind::Member,
                receiver: named("App\\ChildService"),
                target_kind: SymbolKind::Method,
                range: range(10, 17),
                ..Reference::default()
            },
            Reference {
                name: "unrelated".into(),
                kind: NameKind::Member,
                receiver: named("App\\ChildService"),
                target_kind: SymbolKind::Method,
                range: range(30, 39),
                ..Reference::default()
            },
            reference_with_targets(
                Reference {
                    name: "Unrelated".into(),
                    kind: NameKind::Class,
                    range: range(40, 49),
                    ..Reference::default()
                },
                &["App\\Unrelated"],
                &[],
            ),
            reference_with_targets(
                Reference {
                    name: "Target".into(),
                    kind: NameKind::Class,
                    range: range(50, 56),
                    ..Reference::default()
                },
                &["APP\\TARGET"],
                &[],
            ),
        ],
        ..Document::default()
    };
    let snapshot = Snapshot::new(
        1,
        &[
            document("/parent.php", vec![parent, method]),
            document("/child.php", vec![child]),
            document("/target.php", vec![target]),
            consumer,
        ],
    )
    .with_document(&document("/open.php", Vec::new()));
    let packed = snapshot
        .base()
        .unwrap()
        .packed()
        .path_document("/consumer.php")
        .unwrap()
        .clone();
    let references = &packed.references;
    let method_view = snapshot.symbol_view("execute").unwrap();
    let target_view = snapshot.symbol_view("target").unwrap();
    assert!(reference_may_target(
        references,
        &references.record(0),
        &method_view
    ));
    assert!(!reference_may_target(
        references,
        &references.record(1),
        &method_view
    ));
    assert!(!reference_may_target(
        references,
        &references.record(2),
        &target_view
    ));
    assert!(reference_may_target(
        references,
        &references.record(3),
        &target_view
    ));
    assert_eq!(
        snapshot.references_to("execute"),
        vec![ReferenceLocation {
            path: "/consumer.php".into(),
            range_start: 10,
            range_end: 17
        }]
    );
    assert_eq!(
        snapshot.references_to("target"),
        vec![ReferenceLocation {
            path: "/consumer.php".into(),
            range_start: 50,
            range_end: 56
        }]
    );
    let _keep: Arc<_> = packed;
}

#[test]
fn function_references_fall_back_through_namespaces() {
    let global = symbol(
        "strlen",
        SymbolKind::Function,
        "strlen",
        "strlen",
        "/stubs.php",
    );
    let consumer = Document {
        path: "/consumer.php".into(),
        references: vec![reference_with_targets(
            Reference {
                name: "strlen".into(),
                kind: NameKind::Function,
                range: range(5, 11),
                ..Reference::default()
            },
            &["App\\strlen", "strlen"],
            &[],
        )],
        ..Document::default()
    };
    let snapshot = Snapshot::new(1, &[document("/stubs.php", vec![global]), consumer]);
    assert_eq!(snapshot.references_to("strlen").len(), 1);
}

#[test]
fn bloom_hash_normalizes_prefixes_and_case() {
    use crate::references::reference_bloom_hash;
    assert_eq!(
        reference_bloom_hash("\\App\\Service"),
        reference_bloom_hash("app\\service")
    );
    assert_eq!(
        reference_bloom_hash("$Value"),
        reference_bloom_hash("value")
    );
    assert_eq!(reference_bloom_hash(""), 0);
    assert_ne!(reference_bloom_hash("a"), reference_bloom_hash("b"));
}

#[test]
fn packed_references_round_trip_with_fallbacks() {
    let mut references = Vec::new();
    for index in 0..40u32 {
        let mut reference = Reference {
            name: format!("name{}", index % 7).into(),
            resolved: if index % 3 == 0 {
                format!("id{index}").into()
            } else {
                SymbolId::default()
            },
            receiver: if index % 2 == 0 {
                named("App\\Receiver")
            } else {
                Type::unknown()
            },
            range: if index == 5 {
                range(100, 100 + 5000)
            } else {
                range(index * 10, index * 10 + 4)
            },
            kind: NameKind::from_u8((index % 5) as u8).unwrap(),
            target_kind: SymbolKind::from_u8((index % 16) as u8).unwrap(),
            is_static: index % 4 == 0,
            write: index % 6 == 0,
            ..Reference::default()
        };
        let qualified: Vec<String> = (0..(if index == 7 { 20 } else { index % 3 }))
            .map(|value| format!("Q{value}"))
            .collect();
        reference.set_qualified_names(
            qualified
                .iter()
                .map(|value| Str::from(value.as_str()))
                .collect(),
        );
        if index == 9 {
            reference.set_candidate_ids(vec!["a".into(), "b".into()]);
        }
        references.push(reference);
    }
    let packed = crate::references::PackedReferences::pack(&references);
    assert!(packed.validate());
    assert_eq!(packed.full_count(), 2);
    assert_eq!(packed.materialize(), references);
    assert_ne!(packed.bloom, [0, 0]);
}
