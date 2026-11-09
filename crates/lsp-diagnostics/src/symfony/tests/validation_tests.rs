use super::{index_file, open_php, suggestions};
use crate::symfony::ValidationAnalyzer;
use crate::test_support::check_problems;

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
    let fixture = r"<?php
class UniqueName extends \Symfony\Component\Validator\Constraint {}
new UniqueName(['{|symfony.validation.option.missing:messag|}' => 'key', 'groups' => ['Default']]);
";
    let path = "/project/src/Validation.php";
    let (source, _) = swlsp_lsp::fixture::parse(fixture);
    index_file(&php, path, &source);
    let (_, result) = check_problems(
        &ValidationAnalyzer::new(Some(php.clone())),
        &format!("file://{path}"),
        fixture,
    );
    assert!(suggestions(&result[0]).contains(&"message".to_owned()));
}
