//! Port of Go `internal/app/providers_context_test.go`.

use std::sync::Arc;

use swlsp_indexer::{Indexer, ParsedFile};
use swlsp_lsp::{Context, SyntaxContext, TextDocument};
use swlsp_lsp_phpanalysis::State;
use swlsp_php::PhpIndex;
use swlsp_syntax::{SyntaxNodeExt, TextSize};

use crate::providers::enrich_php_context;

#[test]
fn enrich_php_context_reuses_document_analysis() {
    let cache = tempfile::tempdir().unwrap();
    let index = PhpIndex::open(cache.path(), None).unwrap();

    let source = "<?php
class Consumer {
    public function run(): void { $this->execute(); }
}";
    let document = Arc::new(TextDocument::new("file:///project/Consumer.php", source, 1));
    let offset = TextSize::from(source.find("execute").unwrap() as u32);
    let root = document.root().unwrap();
    let syntax = SyntaxContext {
        document: Some(document.clone()),
        node: root.node_at(offset),
        root: Some(root),
        ..SyntaxContext::default()
    };

    let state = |ctx: Context| ctx.value::<Arc<State>>().cloned();
    let first = state(enrich_php_context(&Context::background(), &index, &syntax)).unwrap();
    let second = state(enrich_php_context(&Context::background(), &index, &syntax)).unwrap();
    assert!(Arc::ptr_eq(&first.document, &second.document));
    assert!(Arc::ptr_eq(&first.snapshot, &second.snapshot));

    index
        .index(&ParsedFile::new(
            "/project/Dependency.php",
            b"<?php class Dependency {}".to_vec(),
        ))
        .unwrap();
    let third = state(enrich_php_context(&Context::background(), &index, &syntax)).unwrap();
    assert!(!Arc::ptr_eq(&first.document, &third.document));
    assert!(!Arc::ptr_eq(&first.snapshot, &third.snapshot));
    index.close().unwrap();
}
