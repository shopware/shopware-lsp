use std::sync::Arc;

use swlsp_uriutil::file_uri;

use super::*;
use crate::php_common::test_support::*;

#[test]
fn doctrine_definition_navigates_type_registration_class() {
    let root = tempfile::tempdir().unwrap();
    let doctrine =
        Arc::new(swlsp_doctrine::Index::new(root.path().join("doctrine"), None).unwrap());
    let php = php_index(&root.path().join("php"));
    let type_path = "/project/src/MoneyType.php";
    for (path, source) in [
        (
            "/project/vendor/Type.php",
            r"<?php
namespace Doctrine\DBAL\Types;
abstract class Type {}",
        ),
        (
            type_path,
            r"<?php
namespace App\Doctrine;
class MoneyType extends \Doctrine\DBAL\Types\Type {}",
        ),
    ] {
        index(&php, path, source);
    }
    let source = r"<?php
use App\Doctrine\MoneyType;
use Doctrine\DBAL\Types\Type;

Type::addType('money', MoneyType::class);";
    let document = document("file:///project/bootstrap.php", source);
    let provider = DoctrineDefinitionProvider::new(Some(doctrine), Some(php));
    for needle in ["money", "MoneyType::class"] {
        let offset = index_of(source, needle) + 1;
        let locations = definitions(&provider, &request_at(&document, offset));
        assert_eq!(locations.len(), 1, "{needle}");
        assert_eq!(locations[0].uri, file_uri(type_path));
    }
}
