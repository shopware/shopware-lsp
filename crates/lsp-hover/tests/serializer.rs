mod common;

use std::sync::Arc;

use common::*;
use swlsp_lsp::TextDocument;
use swlsp_lsp_hover::SerializerHoverProvider;
use swlsp_serializer::Index;

#[test]
fn serializer_hover_describes_target_usage() {
    let cache = tempfile::tempdir().unwrap();
    let index = Arc::new(Index::new(cache.path(), None).unwrap());
    let source = "<?php
$serializer->deserialize($data, 'App\\Model[]', 'json');
";
    let path = "/project/src/Handler.php";
    index_file(index.as_ref(), path, source);
    let document = TextDocument::new(format!("file://{path}"), source, 1);
    let request = request_at(document, source.find("App\\Model").unwrap() + 2);
    let result = hover(&SerializerHoverProvider::new(Some(index), None), &request).expect("hover");
    let value = &result.contents.value;
    assert!(value.contains("Symfony Serializer target"));
    assert!(value.contains("App\\Model"));
    assert!(value.contains("1 indexed deserialize use"));
}
