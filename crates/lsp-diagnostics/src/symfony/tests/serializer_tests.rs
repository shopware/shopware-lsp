use std::sync::Arc;

use super::{analyze, index_file, open_php, suggestions, tempdir};
use crate::symfony::{MISSING_SERIALIZER_CLASS_CODE, SerializerAnalyzer};
use crate::test_support::diagnostics_document;

#[test]
fn serializer_diagnostics_report_missing_string_class() {
    let (_dir, php) = open_php();
    index_file(
        &php,
        "/project/src/Model.php",
        "<?php namespace App; class Model {}",
    );
    let serializer_dir = tempdir();
    let serializer = Arc::new(swlsp_serializer::Index::new(serializer_dir.path(), None).unwrap());
    let source = r"<?php
$serializer->deserialize($data, 'App\Modle[]', 'json');
$serializer->deserialize($data, 'App\Model', 'json');
";
    let result = analyze(
        &SerializerAnalyzer::new(Some(serializer), Some(php.clone())),
        &diagnostics_document("file:///project/src/Handler.php", source),
    );
    assert_eq!(result.len(), 1);
    assert_eq!(result[0].id.0, MISSING_SERIALIZER_CLASS_CODE);
    assert!(result[0].message.contains("App\\Modle"));
    assert!(suggestions(&result[0]).contains(&"App\\Model".to_owned()));
}
