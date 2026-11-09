use std::path::Path;

use swlsp_indexer::{BatchIndexer, CancelToken};
use swlsp_parser_php::query;

use super::{index, open};
use crate::PhpIndex;

const ITEM: &str =
    "<?php\nnamespace App;\nclass Item { public function label(): string { return ''; } }\n";

/// `label()` and `outer()` infer their return types through other files.
const FILES: [(&str, &str); 4] = [
    ("Item.php", ITEM),
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

fn write(root: &Path, name: &str, source: &str) -> String {
    let path = root.join(name);
    std::fs::write(&path, source).unwrap();
    path.to_str().unwrap().to_owned()
}

fn index_run(idx: &PhpIndex, paths: &[String]) {
    idx.begin_indexing_batch(paths);
    for path in paths {
        index(idx, path, &std::fs::read_to_string(path).unwrap());
    }
    idx.end_indexing_batch().unwrap();
    idx.settle_indexing_batch(&CancelToken::new(), 2).unwrap();
}

fn current(idx: &PhpIndex, function: &str) -> String {
    let functions = idx.semantic_snapshot().functions(function);
    assert_eq!(functions.len(), 1, "{function}");
    functions[0].return_type.to_string()
}

fn stored(idx: &PhpIndex, function: &str) -> String {
    let snapshot = idx.semantic_snapshot();
    snapshot.function_views(function)[0]
        .materialize_stored()
        .return_type
        .to_string()
}

/// The type of the `name()` call in an unsaved document.
fn call_type(idx: &PhpIndex, name: &str) -> String {
    let source = format!("<?php\nnamespace App;\n$value = {name}();\n");
    let parsed = swlsp_parser_php::parse(&source);
    let document = idx.analyze_tree("/project/open.php", 1, &parsed.tree);
    let call = query::calls(&parsed.root(), &[])
        .into_iter()
        .find(|call| query::call_method_name(call) == name)
        .expect("call");
    document.type_of(&call).ty.to_string()
}

#[test]
fn inferred_returns_follow_files_changed_in_later_runs() {
    let project = tempfile::tempdir().unwrap();
    let config = tempfile::tempdir().unwrap();
    let paths: Vec<String> = FILES
        .iter()
        .map(|(name, source)| write(project.path(), name, source))
        .collect();
    let idx = open(config.path());
    index_run(&idx, &paths);
    assert_eq!(current(&idx, "App\\outer"), "string");

    // Only the declared method changes; the files inferring from it are not
    // reindexed, so their persisted types go stale.
    let item = write(project.path(), "Item.php", &ITEM.replace("string", "int"));
    index_run(&idx, std::slice::from_ref(&item));
    assert_eq!(stored(&idx, "App\\label"), "string");
    assert_eq!(stored(&idx, "App\\outer"), "string");

    // Queries recompute them against the current generation.
    assert_eq!(current(&idx, "App\\label"), "int");
    assert_eq!(current(&idx, "App\\outer"), "int");
    assert_eq!(call_type(&idx, "outer"), "int");
    assert_eq!(call_type(&idx, "item"), "App\\Item");

    // Indexing keeps analyzing against stored types, so preparing one file
    // never analyzes others.
    let consumer = write(
        project.path(),
        "consumer.php",
        "<?php\nnamespace App;\nfunction consumer() { return outer(); }\n",
    );
    let before = idx.semantic_snapshot().inferred_return_files();
    index_run(&idx, std::slice::from_ref(&consumer));
    assert_eq!(stored(&idx, "App\\consumer"), "string");
    assert_eq!(idx.semantic_snapshot().inferred_return_files(), 0);
    assert!(before > 0);
    idx.close().unwrap();
}

#[test]
fn inferred_returns_fall_back_to_stored_types_for_unreadable_files() {
    let project = tempfile::tempdir().unwrap();
    let config = tempfile::tempdir().unwrap();
    let paths: Vec<String> = FILES
        .iter()
        .map(|(name, source)| write(project.path(), name, source))
        .collect();
    let idx = open(config.path());
    index_run(&idx, &paths);
    std::fs::remove_file(&paths[2]).unwrap();
    // The watcher has not removed `label.php` from the index yet.
    assert_eq!(current(&idx, "App\\label"), "string");
    idx.close().unwrap();
}
