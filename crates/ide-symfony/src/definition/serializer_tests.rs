use std::sync::Arc;

use swlsp_uriutil::file_uri;

use super::*;
use crate::definition::php_common::test_support::*;

#[test]
fn serializer_definition_navigates_string_target() {
    let root = tempfile::tempdir().unwrap();
    let model_path = root.path().join("src/Model.php");
    let handler_path = root.path().join("src/Handler.php");
    let model_source = "<?php\nnamespace App;\nclass Model {}\n";
    let handler_source = r"<?php
namespace App;
function load($serializer): void {
    $serializer->deserialize($data, 'App\Model[]', 'json');
}
";
    write(&model_path, model_source);
    write(&handler_path, handler_source);
    let php = php_index(&root.path().join("php"));
    let serializer =
        Arc::new(swlsp_serializer::Index::new(root.path().join("serializer"), None).unwrap());
    index(&php, &p(&model_path), model_source);
    index(serializer.as_ref(), &p(&handler_path), handler_source);

    let document = document(&file_uri(&p(&handler_path)), handler_source);
    let offset = index_of(handler_source, "App\\Model") + 2;
    let locations = definitions(
        &SerializerDefinitionProvider::new(Some(serializer), Some(php)),
        &request_at(&document, offset),
    );
    assert_eq!(locations.len(), 1);
    assert_eq!(locations[0].uri, file_uri(&p(&model_path)));
}
