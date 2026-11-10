use swlsp_uriutil::file_uri;

use super::*;
use crate::definition::php_common::test_support::*;

#[test]
fn console_helper_definition_navigates_to_helper_class() {
    let root = tempfile::tempdir().unwrap();
    let helper_path = root.path().join("src/QuestionHelper.php");
    let helper_source = r"<?php
namespace App;
use Symfony\Component\Console\Helper\Helper;
class QuestionHelper extends Helper {
    public function getName(): string { return 'question'; }
}";
    write(&helper_path, helper_source);
    let php = php_index(&root.path().join("cache"));
    index(
        &php,
        &p(&root.path().join("vendor/Console.php")),
        r"<?php
namespace Symfony\Component\Console\Helper;
interface HelperInterface { public function getName(): string; }
abstract class Helper implements HelperInterface {}
class HelperSet { public function get(string $name): HelperInterface {} }
",
    );
    index(&php, &p(&helper_path), helper_source);

    let path = p(&root.path().join("src/Consumer.php"));
    let source = r"<?php
use Symfony\Component\Console\Helper\HelperSet;
function inspect(HelperSet $helpers): void {
    $helpers->get('question');
}";
    let document = document(&file_uri(&path), source);
    let offset = last_index_of(source, "question") + 1;
    let locations = definitions(
        &ConsoleHelperDefinitionProvider::new(Some(php)),
        &request_at(&document, offset),
    );
    assert_eq!(locations.len(), 1);
    assert_eq!(locations[0].uri, file_uri(&p(&helper_path)));
    assert_eq!(locations[0].range.start.line, 3);
    assert_eq!(locations[0].range.start.character, 6);
    assert_eq!(locations[0].range.end.character, 20);
}
