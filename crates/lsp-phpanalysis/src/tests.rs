use swlsp_indexer::{Indexer, ParsedFile};

use super::*;

fn open() -> (tempfile::TempDir, PhpIndex) {
    let dir = tempfile::tempdir().unwrap();
    let index = PhpIndex::open(dir.path(), None).unwrap();
    (dir, index)
}

#[test]
fn for_document_caches_per_workspace_revision() {
    let (_dir, index) = open();
    let document = TextDocument::new(
        "file:///project/src/Consumer.php",
        "<?php class Consumer {}",
        1,
    );

    let first = for_document(&index, &document).unwrap().unwrap();
    let second = for_document(&index, &document).unwrap().unwrap();
    assert!(Arc::ptr_eq(&first, &second));

    index
        .index(&ParsedFile::new(
            "/project/src/Dependency.php",
            b"<?php class Dependency {}".to_vec(),
        ))
        .unwrap();
    let third = for_document(&index, &document).unwrap().unwrap();
    assert!(!Arc::ptr_eq(&first, &third));
    assert!(third.snapshot.revision > first.snapshot.revision);
    index.close().unwrap();
}

/// A type extension whose data changes without a PHP index change, like
/// the Symfony extension answering from the service index.
struct VersionedExtension(std::sync::atomic::AtomicU64);

impl swlsp_php_inference::Extension for VersionedExtension {
    fn infer_call(
        &self,
        _: &swlsp_php_inference::CallContext<'_>,
    ) -> Option<swlsp_php_semantic::TypeFact> {
        None
    }

    fn revision(&self) -> u64 {
        self.0.load(std::sync::atomic::Ordering::SeqCst)
    }
}

#[test]
fn for_document_follows_type_extension_revisions() {
    let (_dir, index) = open();
    let extension = Arc::new(VersionedExtension(Default::default()));
    index.register_type_extension(extension.clone());
    let document = TextDocument::new("file:///project/src/Uses.php", "<?php $x = 1;", 1);
    let first = for_document(&index, &document).unwrap().unwrap();
    assert!(Arc::ptr_eq(
        &first,
        &for_document(&index, &document).unwrap().unwrap()
    ));
    extension.0.store(1, std::sync::atomic::Ordering::SeqCst);
    let second = for_document(&index, &document).unwrap().unwrap();
    assert!(!Arc::ptr_eq(&first, &second), "stale extension data");
    index.close().unwrap();
}

#[test]
fn for_document_shares_concurrent_analysis() {
    let (_dir, index) = open();
    let document = TextDocument::new(
        "file:///project/src/Concurrent.php",
        "<?php class Concurrent {}",
        1,
    );
    let states: Vec<Arc<State>> = std::thread::scope(|scope| {
        let handles: Vec<_> = (0..8)
            .map(|_| scope.spawn(|| for_document(&index, &document).unwrap().unwrap()))
            .collect();
        handles
            .into_iter()
            .map(|handle| handle.join().unwrap())
            .collect()
    });
    for state in &states {
        assert!(Arc::ptr_eq(&states[0], state));
    }
    index.close().unwrap();
}

#[test]
fn imports_mark_suppressed_imports_used() {
    let (_dir, index) = open();
    let source = "<?php\nnamespace App;\nuse Foo\\Unused;\n// @phpstan-ignore-next-line\nuse Foo\\Suppressed;\nuse Foo\\Used;\nnew Used();\n";
    let document = TextDocument::new("file:///project/src/Imports.php", source, 1);
    let analysis = imports(&Context::background(), &index, &document).unwrap();
    let used: Vec<(String, bool)> = analysis
        .scopes
        .iter()
        .flat_map(|scope| &scope.declarations)
        .flat_map(|declaration| &declaration.items)
        .map(|item| (source[item.name_range].to_owned(), item.used))
        .collect();
    assert_eq!(used.len(), 3, "{used:?}");
    assert!(
        used.iter()
            .any(|(name, used)| name.ends_with("Unused") && !used)
    );
    assert!(
        used.iter()
            .any(|(name, used)| name.ends_with("Suppressed") && *used)
    );
    assert!(
        used.iter()
            .any(|(name, used)| name.ends_with("\\Used") && *used)
    );

    let twig = TextDocument::new("file:///project/a.twig", "{{ a }}", 1);
    assert!(
        imports(&Context::background(), &index, &twig)
            .unwrap()
            .scopes
            .is_empty()
    );
    index.close().unwrap();
}

#[test]
fn call_returns_value_follows_linked_targets() {
    let (_dir, index) = open();
    let source = "<?php\nfunction value(): int { return 1; }\nfunction &reference(): array { static $a = []; return $a; }\nvalue();\nreference();\nunknown();\n";
    let document = TextDocument::new("file:///project/src/Calls.php", source, 1);
    let state = for_document(&index, &document).unwrap().unwrap();
    let root = document.root().unwrap();
    let call = |name: &str| {
        swlsp_parser_php::query::calls(&root, &[])
            .into_iter()
            .find(|call| swlsp_parser_php::query::call_name(call) == name)
            .unwrap()
    };
    assert!(state.call_returns_value(&call("value")));
    assert!(!state.call_returns_value(&call("reference")));
    assert!(!state.call_returns_value(&call("unknown")));
    index.close().unwrap();
}

#[test]
fn request_context_reuses_memoized_analysis() {
    let (_dir, index) = open();
    let document = Arc::new(TextDocument::new(
        "file:///project/src/Request.php",
        "<?php class Request { public function run() {} }",
        1,
    ));
    let root = document.root().unwrap();
    let node = root.descendants().last();
    let syntax = SyntaxContext {
        document: Some(document.clone()),
        language: Some(LanguageId::Php),
        root: Some(root.clone()),
        token: None,
        node: node.clone(),
    };
    let first = request_context(&index, &syntax).unwrap();
    let second = request_context(&index, &syntax).unwrap();
    assert!(Arc::ptr_eq(&first.document, &second.document));
    assert!(Arc::ptr_eq(&first.snapshot, &second.snapshot));
    assert_eq!(first.node, node);
    assert_eq!(
        first.inside_class.as_ref().map(|class| class.name.as_str()),
        Some("Request")
    );

    let twig = SyntaxContext {
        language: Some(LanguageId::Twig),
        ..syntax
    };
    assert!(request_context(&index, &twig).is_none());
    index.close().unwrap();
}

#[test]
fn suppression_reader_matches_framework_codes_by_identifier_and_wildcard() {
    let source = "<?php\n\
        // @phpstan-ignore symfony.service.missing\n\
        $a = 1;\n\
        // @phpstan-ignore-next-line\n\
        $b = 2;\n\
        $c = 3;\n";
    let document = TextDocument::new("file:///project/src/Consumer.php", source, 1);
    let suppressions = suppression_reader()(&document);
    let at = |text: &str| TextSize::from(source.find(text).unwrap() as u32);
    let missing = DiagnosticId::from("symfony.service.missing");
    let other = DiagnosticId::from("symfony.class.missing");
    assert!(suppressions.suppresses(at("$a"), &missing));
    assert!(!suppressions.suppresses(at("$a"), &other));
    assert!(suppressions.suppresses(at("$b"), &other));
    assert!(!suppressions.suppresses(at("$c"), &missing));
}
