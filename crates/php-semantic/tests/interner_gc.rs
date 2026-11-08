//! Interner generation collection. A separate test binary: collections swap
//! the process-wide current generation, which the crate's unit tests
//! assume stays put. Tests here are serialized for the same reason.

use std::sync::{Arc, Mutex, MutexGuard};

use swlsp_php_semantic::{
    Document, NameKind, Parameter, Reference, Snapshot, Store, Str, Symbol, SymbolKind,
    atom_generation_stats, new_symbol_id,
};
use swlsp_syntax::TextRange;

fn serial() -> MutexGuard<'static, ()> {
    static LOCK: Mutex<()> = Mutex::new(());
    LOCK.lock().unwrap_or_else(|poison| poison.into_inner())
}

fn range(start: u32, end: u32) -> TextRange {
    TextRange::new(start.into(), end.into())
}

/// A class `<namespace>\Service` extending `<namespace>\Base` with a method
/// `run(string $value)`, plus a reference from a second file.
fn documents(namespace: &str) -> Vec<Document> {
    let directory = namespace.replace('\\', "/");
    let service_path = format!("/{directory}/service.php");
    let caller_path = format!("/{directory}/caller.php");
    let class_fqn = format!("{namespace}\\Service");
    let class_id = new_symbol_id(SymbolKind::Class, &class_fqn, &service_path, 0);
    let method_fqn = format!("{class_fqn}::run");
    let method_id = new_symbol_id(SymbolKind::Method, &method_fqn, &service_path, 20);
    let mut class = Symbol {
        id: class_id.clone(),
        kind: SymbolKind::Class,
        name: Str::from("Service"),
        fully_qualified: Str::from(class_fqn.as_str()),
        path: Str::from(service_path.as_str()),
        range: range(0, 100),
        selection_range: range(6, 13),
        ..Symbol::default()
    };
    class.set_hierarchy(
        vec![Str::from(format!("{namespace}\\Base"))],
        Vec::new(),
        Vec::new(),
        Vec::new(),
        Vec::new(),
        Vec::new(),
        Vec::new(),
    );
    let method = Symbol {
        id: method_id.clone(),
        kind: SymbolKind::Method,
        name: Str::from("run"),
        fully_qualified: Str::from(method_fqn.as_str()),
        container: class_id.clone(),
        path: Str::from(service_path.as_str()),
        range: range(20, 60),
        selection_range: range(36, 39),
        parameters: vec![Parameter {
            name: Str::from("value"),
            range: range(40, 53),
            selection_range: range(47, 53),
            ..Parameter::default()
        }],
        ..Symbol::default()
    };
    let service = Document {
        path: Str::from(service_path.as_str()),
        namespace: Str::from(namespace),
        symbols: vec![class, method],
        ..Document::default()
    };
    let caller = Document {
        path: Str::from(caller_path.as_str()),
        references: vec![Reference {
            name: Str::from("Service"),
            resolved: class_id,
            range: range(10, 17),
            kind: NameKind::Class,
            target_kind: SymbolKind::Class,
            ..Reference::default()
        }],
        ..Document::default()
    };
    vec![service, caller]
}

/// Everything the tests query, as plain strings.
fn observe(snapshot: &Snapshot, namespace: &str) -> Vec<String> {
    let class_fqn = format!("{namespace}\\Service");
    let classes = snapshot.classes(&class_fqn);
    assert_eq!(classes.len(), 1, "class {class_fqn}");
    let class = &classes[0];
    let methods = snapshot.members(class.id.as_str(), "run");
    assert_eq!(methods.len(), 1);
    let method = &methods[0];
    let mut observed = vec![
        class.id.as_str().to_string(),
        class.name.to_string(),
        class.fully_qualified.to_string(),
        method.id.as_str().to_string(),
        method.fully_qualified.to_string(),
        method.container.as_str().to_string(),
        method.parameters[0].name.to_string(),
        class.hierarchy.get().unwrap().extends[0].to_string(),
    ];
    let symbol = snapshot.symbol(method.id.as_str()).expect("method by id");
    observed.push(symbol.name.to_string());
    for location in snapshot.references_to(class.id.as_str()) {
        observed.push(format!(
            "{}@{}..{}",
            location.path, location.range_start, location.range_end
        ));
    }
    observed
}

#[test]
fn collection_preserves_queries_and_frees_the_old_generation() {
    let _serial = serial();
    let store = Store::new();
    store.replace_many(&documents("App\\Gc"));
    let before_snapshot = store.snapshot();
    let before = observe(&before_snapshot, "App\\Gc");
    // A name materialized from the old generation outlives it.
    let held_name: Str = before_snapshot.classes("App\\Gc\\Service")[0].name.clone();

    let stats = atom_generation_stats();
    assert!(store.collect_atoms(), "collection should start");
    let after = atom_generation_stats();
    assert_eq!(after.collections, stats.collections + 1);
    assert_eq!(after.live, 2, "old generation is held by the old snapshot");

    let collected = store.snapshot();
    assert_eq!(observe(&collected, "App\\Gc"), before);
    // The old snapshot keeps answering from its own generation.
    assert_eq!(observe(&before_snapshot, "App\\Gc"), before);

    drop(before_snapshot);
    assert_eq!(atom_generation_stats().live, 1, "old generation freed");
    assert_eq!(&*held_name, "Service");
    assert_eq!(observe(&collected, "App\\Gc"), before);

    // Publishing after a collection keeps working.
    store.replace_many(&documents("App\\GcNext"));
    let next = store.snapshot();
    observe(&next, "App\\GcNext");
    assert_eq!(observe(&next, "App\\Gc"), before);
}

#[test]
fn documents_packed_before_a_collection_are_rehomed_on_publish() {
    let _serial = serial();
    let other = Store::new();
    let collecting = Store::new();
    collecting.replace_many(&documents("App\\Collecting"));
    other.replace_many(&documents("App\\Other"));
    let expected = observe(&other.snapshot(), "App\\Other");

    assert!(collecting.collect_atoms());
    // `other` still holds the old generation until it publishes again.
    assert_eq!(atom_generation_stats().live, 2);
    other.replace_many(&documents("App\\OtherNext"));
    let snapshot = other.snapshot();
    assert_eq!(observe(&snapshot, "App\\Other"), expected);
    observe(&snapshot, "App\\OtherNext");
    assert_eq!(atom_generation_stats().live, 1, "nothing holds the old one");
}

#[test]
fn repeated_collections_free_every_generation() {
    let _serial = serial();
    let store = Store::new();
    store.replace_many(&documents("App\\Repeat"));
    let expected = observe(&store.snapshot(), "App\\Repeat");
    for _ in 0..20 {
        assert!(store.collect_atoms());
        assert_eq!(observe(&store.snapshot(), "App\\Repeat"), expected);
        assert_eq!(atom_generation_stats().live, 1);
    }
}

#[test]
fn collection_is_safe_under_concurrent_readers() {
    let _serial = serial();
    let store = Arc::new(Store::new());
    store.replace_many(&documents("App\\Concurrent"));
    let expected = observe(&store.snapshot(), "App\\Concurrent");
    let stop = Arc::new(std::sync::atomic::AtomicBool::new(false));
    let readers: Vec<_> = (0..4)
        .map(|_| {
            let store = store.clone();
            let stop = stop.clone();
            let expected = expected.clone();
            std::thread::spawn(move || {
                let mut reads = 0;
                while !stop.load(std::sync::atomic::Ordering::Relaxed) || reads == 0 {
                    let snapshot = store.snapshot();
                    assert_eq!(observe(&snapshot, "App\\Concurrent"), expected);
                    reads += 1;
                }
                reads
            })
        })
        .collect();
    let mut collected = 0;
    for _ in 0..50 {
        // Slots may all be held by readers' snapshots for a moment.
        if store.collect_atoms() {
            collected += 1;
        }
        store.replace_many(&documents("App\\ConcurrentWrite"));
    }
    stop.store(true, std::sync::atomic::Ordering::Relaxed);
    for reader in readers {
        assert!(reader.join().unwrap() > 0);
    }
    assert!(collected > 0);
    assert_eq!(observe(&store.snapshot(), "App\\Concurrent"), expected);
    drop(store);
    assert!(atom_generation_stats().live <= 1);
}

#[test]
fn encoded_graphs_decode_into_the_current_generation_after_collection() {
    let _serial = serial();
    let store = Store::new();
    let documents = documents("App\\Codec");
    let graphs: Vec<Vec<u8>> = documents
        .iter()
        .map(|document| swlsp_php_semantic::WorkspaceGraph::project(document).encode())
        .collect();
    store.replace_many(&documents);
    let expected = observe(&store.snapshot(), "App\\Codec");
    assert!(store.collect_atoms());
    let decoded: Vec<_> = graphs
        .iter()
        .map(|bytes| swlsp_php_semantic::WorkspaceGraph::decode(bytes).unwrap())
        .collect();
    let restored = Store::new();
    restored.replace_workspace_graphs_owned(decoded);
    assert_eq!(observe(&restored.snapshot(), "App\\Codec"), expected);
}
