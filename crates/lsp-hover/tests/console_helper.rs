mod common;

use common::*;
use swlsp_lsp::TextDocument;
use swlsp_lsp_hover::ConsoleHelperHoverProvider;

#[test]
fn console_helper_hover_shows_resolved_class() {
    let root = tempfile::tempdir().unwrap();
    let cache = tempfile::tempdir().unwrap();
    let php = php_index(
        cache.path(),
        &[
            (
                &p(&root.path().join("vendor/Console.php")),
                r"<?php
namespace Symfony\Component\Console\Helper;
interface HelperInterface { public function getName(): string; }
abstract class Helper implements HelperInterface {}
class HelperSet { public function has(string $name): bool {} }
",
            ),
            (
                &p(&root.path().join("src/QuestionHelper.php")),
                r"<?php
namespace App;
use Symfony\Component\Console\Helper\Helper;
/** Helps ask interactive questions. */
class QuestionHelper extends Helper {
    public function getName(): string { return 'question'; }
}",
            ),
        ],
    );
    let source = r"<?php
use Symfony\Component\Console\Helper\HelperSet;
function inspect(HelperSet $helpers): void {
    $helpers->has('question');
}";
    let document = TextDocument::new(uri(&root.path().join("src/Consumer.php")), source, 1);
    let request = request_at(document, source.rfind("question").unwrap() + 1);
    let result = hover(&ConsoleHelperHoverProvider::new(Some(php)), &request).expect("hover");
    let value = &result.contents.value;
    assert!(value.contains("Symfony Console helper"));
    assert!(value.contains("`question`"));
    assert!(value.contains("`App\\QuestionHelper`"));
    assert!(value.contains("interactive questions"));
    let range = result.range.expect("range");
    assert_eq!(range.start.character, 19);
    assert_eq!(range.end.character, 27);
}
