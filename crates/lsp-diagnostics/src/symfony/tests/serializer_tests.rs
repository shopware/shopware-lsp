use std::sync::Arc;

use super::{index_file, open_php, suggestions, tempdir};
use crate::symfony::SerializerAnalyzer;
use crate::test_support::check_problems;

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
    let (_, result) = check_problems(
        &SerializerAnalyzer::new(Some(serializer), Some(php.clone())),
        "file:///project/src/Handler.php",
        r"<?php
$serializer->deserialize($data, '{|symfony.serializer.class.missing:App\Modle[]|}', 'json');
$serializer->deserialize($data, 'App\Model', 'json');
",
    );
    assert!(result[0].message.contains("App\\Modle"));
    assert!(suggestions(&result[0]).contains(&"App\\Model".to_owned()));
}
