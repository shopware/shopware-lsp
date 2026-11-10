use super::*;

fn class_document(path: &str, id: &str, name: &str) -> Document {
    document(
        path,
        vec![symbol(
            id,
            SymbolKind::Class,
            name.rsplit('\\').next().unwrap(),
            name,
            path,
        )],
    )
}

#[test]
fn store_publishes_many_documents_as_one_generation() {
    let store = Store::new();
    let snapshot = store.replace_many(&[
        class_document("/first.php", "first", "App\\First"),
        class_document("/second.php", "second", "App\\Second"),
    ]);
    assert_eq!(snapshot.revision, 1);
    assert_eq!(snapshot.classes("App\\First").len(), 1);
    assert_eq!(snapshot.classes("App\\Second").len(), 1);
    assert!(std::sync::Arc::ptr_eq(&snapshot, &store.snapshot()));
    let unchanged = store.replace_many(&[Document::default()]);
    assert_eq!(unchanged.revision, 1);
}

#[test]
fn store_replace_projects_workspace_graph() {
    let store = Store::new();
    let mut full = class_document("/a.php", "a", "App\\A");
    full.symbols
        .push(symbol("local", SymbolKind::Local, "$x", "", "/a.php"));
    full.references.push(Reference {
        name: "$x".into(),
        kind: NameKind::Variable,
        ..Reference::default()
    });
    store.replace(&full);
    let stored = store.document("/a.php").unwrap();
    assert_eq!(ids(&stored.symbols), ["a"]);
    assert!(stored.references.is_empty());
    store.replace_many_owned(vec![full]);
    assert_eq!(store.document("/a.php").unwrap().symbols.len(), 2);
}

#[test]
fn store_path_index_uses_copy_on_write_publication() {
    let store = Store::new();
    let first = store.replace(&class_document("/a.php", "a", "App\\A"));
    let second = store.replace(&class_document("/b.php", "b", "App\\B"));
    let third = store.remove(&["/a.php"]);
    assert!(first.has_document("/a.php") && !first.has_document("/b.php"));
    assert!(second.has_document("/a.php") && second.has_document("/b.php"));
    assert!(!third.has_document("/a.php") && third.has_document("/b.php"));
    assert_eq!(third.revision, 3);
    let cleared = store.clear();
    assert_eq!(cleared.revision, 4);
    assert!(cleared.global_symbols().is_empty());
    assert!(second.has_document("/a.php"));
}

#[test]
fn store_restores_owned_documents_as_one_generation() {
    let store = Store::new();
    let snapshot = store
        .restore_owned::<()>(|accept| {
            accept(class_document("/first.php", "first", "App\\First"));
            accept(class_document("/second.php", "second", "App\\Second"));
            Ok(())
        })
        .unwrap();
    assert_eq!(snapshot.revision, 1);
    assert_eq!(snapshot.classes("App\\First").len(), 1);
    assert_eq!(snapshot.classes("App\\Second").len(), 1);
}

#[test]
fn store_restore_failure_keeps_published_generation() {
    let store = Store::new();
    let before = store.replace(&class_document(
        "/existing.php",
        "existing",
        "App\\Existing",
    ));
    let result = store.restore_owned(|accept| {
        accept(class_document("/partial.php", "partial", "App\\Partial"));
        Err("decode failed")
    });
    assert_eq!(result.unwrap_err(), "decode failed");
    let after = store.snapshot();
    assert!(std::sync::Arc::ptr_eq(&before, &after));
    assert_eq!(after.classes("App\\Existing").len(), 1);
    assert!(after.classes("App\\Partial").is_empty());
}

#[test]
fn store_restores_workspace_graphs() {
    let store = Store::new();
    let encoded = WorkspaceGraph::pack_owned(Document {
        symbols: vec![Symbol {
            ty: named("App\\Service"),
            ..symbol(
                "service",
                SymbolKind::Class,
                "Service",
                "App\\Service",
                "/service.php",
            )
        }],
        ..document("/service.php", Vec::new())
    })
    .encode();
    let snapshot = store
        .restore_workspace_graphs_decoded(
            WorkspaceRestoreCapacity {
                documents: 1,
                strings: 8,
                types: 1,
            },
            |decoder, accept| {
                accept(decoder.decode(&encoded)?);
                Ok::<(), DecodeError>(())
            },
        )
        .unwrap();
    assert_eq!(snapshot.revision, 1);
    assert_eq!(snapshot.classes("App\\Service").len(), 1);
    let restored = store
        .restore_workspace_graphs_owned::<()>(WorkspaceRestoreCapacity::default(), |accept| {
            accept(WorkspaceGraph::project(&class_document(
                "/x.php", "x", "App\\X",
            )));
            Ok(())
        })
        .unwrap();
    assert_eq!(restored.revision, 2);
    assert!(restored.classes("App\\Service").is_empty());
    assert_eq!(restored.classes("App\\X").len(), 1);
    let empty = Store::new();
    let unchanged = empty.restore_owned::<()>(|_| Ok(())).unwrap();
    assert_eq!(unchanged.revision, 0);
}

#[test]
fn store_restores_workspace_graphs_on_several_threads() {
    let names: Vec<String> = (0..300)
        .map(|index| format!("App\\Service{index}"))
        .collect();
    let rows: Vec<Vec<u8>> = names
        .iter()
        .enumerate()
        .map(|(index, name)| {
            let path = format!("/service{index}.php");
            WorkspaceGraph::pack_owned(class_document(&path, &format!("service{index}"), name))
                .encode()
        })
        .collect();
    let restore = |threads: usize| {
        let store = Store::new();
        store
            .restore_workspace_graphs_parallel(
                WorkspaceRestoreCapacity::default(),
                &[class_document("/stub.php", "stub", "App\\Stub")],
                threads,
                |accept| {
                    rows.iter().cloned().for_each(accept);
                    Ok::<(), DecodeError>(())
                },
                |decoder, row| decoder.decode(row),
            )
            .unwrap()
    };
    for threads in [1, 4] {
        let snapshot = restore(threads);
        assert_eq!(snapshot.revision, 1);
        assert_eq!(snapshot.classes("App\\Stub").len(), 1);
        for name in &names {
            assert_eq!(
                snapshot.classes(name).len(),
                1,
                "{name} with {threads} threads"
            );
        }
    }
}

#[test]
fn a_parallel_restore_reports_a_failing_row_and_keeps_the_generation() {
    let store = Store::new();
    let before = store.replace(&class_document("/a.php", "a", "App\\A"));
    let valid = WorkspaceGraph::pack_owned(class_document("/b.php", "b", "App\\B")).encode();
    let result = store.restore_workspace_graphs_parallel(
        WorkspaceRestoreCapacity::default(),
        &[],
        4,
        |accept| {
            for _ in 0..200 {
                accept(valid.clone());
            }
            accept(vec![0xff]);
            Ok::<(), DecodeError>(())
        },
        |decoder, row| decoder.decode(row),
    );
    assert!(result.is_err());
    assert!(std::sync::Arc::ptr_eq(&before, &store.snapshot()));
}

#[test]
fn store_consumes_canonical_graphs_without_mutating_older_snapshots() {
    let store = Store::new();
    let first = store.replace(&class_document("/a.php", "a", "App\\A"));
    let mut detacher = WorkspaceGraphDetacher::new();
    let mut graph = WorkspaceGraph::project_borrowed(&class_document("/a.php", "a2", "App\\A2"));
    detacher.detach_owned(&mut graph);
    detacher.finish();
    let second = store.replace_canonical_workspace_graphs_owned(vec![graph]);
    assert_eq!(first.classes("App\\A").len(), 1);
    assert!(second.classes("App\\A").is_empty());
    assert_eq!(second.classes("App\\A2").len(), 1);
    let third = store.replace_workspace_graphs_owned(vec![WorkspaceGraph::project(
        &class_document("/b.php", "b", "App\\B"),
    )]);
    assert_eq!(third.global_symbols().len(), 2);
}

#[test]
fn stored_documents_are_defensive_copies() {
    let store = Store::new();
    let mut service = symbol(
        "service",
        SymbolKind::Class,
        "Service",
        "App\\Service",
        "/service.php",
    );
    service.parameters = vec![Parameter {
        name: "$value".into(),
        assistant_tags: names(&["Autowire"]),
        ..Parameter::default()
    }];
    service.set_extends(names(&["App\\Base"]));
    store.replace(&document("/service.php", vec![service]));
    let mut first = store.document("/service.php").unwrap();
    first.symbols[0].set_extends(names(&["App\\Mutated"]));
    first.symbols[0].parameters[0].assistant_tags[0] = "Mutated".into();
    let second = store.document("/service.php").unwrap();
    assert_eq!(second.symbols[0].extends(), &names(&["App\\Base"])[..]);
    assert_eq!(
        second.symbols[0].parameters[0].assistant_tags,
        names(&["Autowire"])
    );
}
