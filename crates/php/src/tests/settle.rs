use std::path::Path;

use swlsp_indexer::{BatchIndexer, CancelToken};

use super::{index, open};
use crate::PhpIndex;

/// `App\item()` is inferred within its file; `App\label()` needs it and a
/// declared method of another file; `App\outer()` needs `label()`, so it
/// only settles in a second pass.
const FILES: [(&str, &str); 4] = [
    (
        "Item.php",
        "<?php\nnamespace App;\nclass Item { public function label(): string { return ''; } }\n",
    ),
    (
        "item.php",
        "<?php\nnamespace App;\nfunction item() { return new Item(); }\n",
    ),
    (
        "label.php",
        "<?php\nnamespace App;\nfunction label() { return item()->label(); }\n",
    ),
    (
        "outer.php",
        "<?php\nnamespace App;\nfunction outer() { return label(); }\n",
    ),
];

fn write_files(root: &Path) -> Vec<String> {
    FILES
        .iter()
        .map(|(name, source)| {
            let path = root.join(name);
            std::fs::write(&path, source).unwrap();
            path.to_str().unwrap().to_owned()
        })
        .collect()
}

/// Indexes `paths` as one scanner run: published together, then settled.
fn index_batch(idx: &PhpIndex, paths: &[String]) {
    idx.begin_indexing_batch(paths);
    for path in paths {
        index(idx, path, &std::fs::read_to_string(path).unwrap());
    }
    idx.end_indexing_batch().unwrap();
    idx.settle_indexing_batch(&CancelToken::new(), 2).unwrap();
}

/// The return type persisted and published for `function`, without
/// on-demand inference.
fn return_type(idx: &PhpIndex, function: &str) -> String {
    let snapshot = idx.semantic_snapshot();
    let functions = snapshot.function_views(function);
    assert_eq!(functions.len(), 1, "{function}");
    functions[0].materialize_stored().return_type.to_string()
}

fn assert_settled(idx: &PhpIndex) {
    assert_eq!(return_type(idx, "App\\item"), "App\\Item");
    assert_eq!(return_type(idx, "App\\label"), "string");
    assert_eq!(return_type(idx, "App\\outer"), "string");
}

#[test]
fn settles_return_types_inferred_across_files_of_one_run() {
    let project = tempfile::tempdir().unwrap();
    let config = tempfile::tempdir().unwrap();
    let paths = write_files(project.path());
    let idx = open(config.path());
    index_batch(&idx, &paths);
    assert_settled(&idx);

    // A single-file run already sees every other file and agrees.
    index_batch(&idx, &paths[2..3]);
    assert_settled(&idx);
    idx.close().unwrap();

    // The settled graphs were persisted, not only published.
    let idx = open(config.path());
    assert_settled(&idx);
    idx.close().unwrap();
}

#[test]
fn leaves_files_changed_since_the_run_to_the_watcher() {
    let project = tempfile::tempdir().unwrap();
    let config = tempfile::tempdir().unwrap();
    let paths = write_files(project.path());
    let idx = open(config.path());
    idx.begin_indexing_batch(&paths);
    for path in &paths {
        index(&idx, path, &std::fs::read_to_string(path).unwrap());
    }
    idx.end_indexing_batch().unwrap();
    assert_eq!(return_type(&idx, "App\\label"), "unknown");
    std::fs::write(
        &paths[2],
        "<?php\nnamespace App;\nfunction label() { return 1; }\n",
    )
    .unwrap();
    idx.settle_indexing_batch(&CancelToken::new(), 2).unwrap();
    // The changed file keeps the graph of the content it was indexed from.
    assert_eq!(return_type(&idx, "App\\label"), "unknown");
    idx.close().unwrap();
}

#[test]
fn cancelled_settling_keeps_the_published_run() {
    let project = tempfile::tempdir().unwrap();
    let config = tempfile::tempdir().unwrap();
    let paths = write_files(project.path());
    let idx = open(config.path());
    idx.begin_indexing_batch(&paths);
    for path in &paths {
        index(&idx, path, &std::fs::read_to_string(path).unwrap());
    }
    idx.end_indexing_batch().unwrap();
    let cancel = CancelToken::new();
    cancel.cancel();
    assert!(idx.settle_indexing_batch(&cancel, 2).is_err());
    assert_eq!(return_type(&idx, "App\\item"), "App\\Item");
    assert_eq!(return_type(&idx, "App\\label"), "unknown");
    idx.close().unwrap();
}

/// The stored receiver type of the `label` call in `path`.
fn label_receiver(idx: &PhpIndex, path: &str) -> String {
    let document = idx.inner.semantic_store.document(path).expect("document");
    document
        .references
        .iter()
        .find(|reference| reference.name.as_str() == "label")
        .expect("label call")
        .receiver
        .to_string()
}

#[test]
fn settles_lookups_in_indexes_the_run_was_still_building() {
    use std::sync::Arc;
    use std::sync::atomic::{AtomicBool, Ordering};

    use swlsp_php_inference::CallContext;
    use swlsp_php_semantic::{Confidence, TypeFact, TypeSource};
    use swlsp_php_types::Type;

    let project = tempfile::tempdir().unwrap();
    let config = tempfile::tempdir().unwrap();
    let write = |name: &str, source: &str| {
        let path = project.path().join(name);
        std::fs::write(&path, source).unwrap();
        path.to_str().unwrap().to_owned()
    };
    let paths = [
        write("Item.php", FILES[0].1),
        // No inferred return type: only the lookup makes it a candidate.
        write(
            "run.php",
            "<?php\nnamespace App;\nfunction run(): void { service()->label(); }\n",
        ),
    ];
    let idx = open(config.path());
    // An index another indexer fills during the same run: `service()` is
    // known only once it is complete.
    let complete = Arc::new(AtomicBool::new(false));
    let index_state = complete.clone();
    idx.register_type_extension(Arc::new(move |context: &CallContext<'_>| {
        if context.name != "service" {
            return None;
        }
        context.consults_index();
        index_state.load(Ordering::SeqCst).then(|| TypeFact {
            ty: Type::named("App\\Item", Vec::new()),
            confidence: Confidence::Declared,
            source: TypeSource::Framework,
            origin: Default::default(),
            reason: "test index".into(),
        })
    }));

    idx.begin_indexing_batch(&paths);
    for path in &paths {
        index(&idx, path, &std::fs::read_to_string(path).unwrap());
    }
    complete.store(true, Ordering::SeqCst);
    idx.end_indexing_batch().unwrap();
    assert_eq!(label_receiver(&idx, &paths[1]), "unknown");

    idx.settle_indexing_batch(&CancelToken::new(), 2).unwrap();
    assert_eq!(label_receiver(&idx, &paths[1]), "App\\Item");
    idx.close().unwrap();

    // Persisted, not only published.
    let idx = open(config.path());
    assert_eq!(label_receiver(&idx, &paths[1]), "App\\Item");
    idx.close().unwrap();
}
