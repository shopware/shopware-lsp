use super::{analyze, index_file, open_php, suggestions};
use crate::symfony::{MISSING_CONSTRAINT_OPTION_CODE, ValidationAnalyzer};
use crate::test_support::diagnostics_document;

#[test]
fn constraint_option_diagnostics_report_unknown_property() {
    let (_dir, php) = open_php();
    index_file(
        &php,
        "/project/vendor/Constraint.php",
        r"<?php
namespace Symfony\Component\Validator;
class Constraint { public string $message; public array $groups; }
",
    );
    let source = r"<?php
class UniqueName extends \Symfony\Component\Validator\Constraint {}
new UniqueName(['messag' => 'key', 'groups' => ['Default']]);
";
    let path = "/project/src/Validation.php";
    index_file(&php, path, source);
    let result = analyze(
        &ValidationAnalyzer::new(Some(php.clone())),
        &diagnostics_document(&format!("file://{path}"), source),
    );
    assert_eq!(result.len(), 1);
    assert_eq!(result[0].id.0, MISSING_CONSTRAINT_OPTION_CODE);
    assert!(suggestions(&result[0]).contains(&"message".to_owned()));
}
