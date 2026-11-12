use super::{index_file, open_php, suggestions};
use crate::diagnostics::symfony::ValidationAnalyzer;
use crate::diagnostics::test_support::check_problems;

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

#[test]
fn constraint_options_are_not_the_fields_of_a_collection() {
    let (_dir, php) = open_php();
    index_file(
        &php,
        "/project/vendor/Constraint.php",
        r"<?php
namespace Symfony\Component\Validator;
class Constraint { public string $message; public array $groups; }
namespace Symfony\Component\Validator\Constraints;
class Collection extends \Symfony\Component\Validator\Constraint { public array $fields; }
",
    );
    let fixture = r"<?php
use Symfony\Component\Validator\Constraints\Collection;
new Collection(fields: ['id' => [], 'productNumber' => []]);
new Collection(options: ['{|symfony.validation.option.missing:feilds|}' => []]);
";
    let path = "/project/src/Validation.php";
    let (source, _) = swlsp_lsp::fixture::parse(fixture);
    index_file(&php, path, &source);
    check_problems(
        &ValidationAnalyzer::new(Some(php.clone())),
        &format!("file://{path}"),
        fixture,
    );
}
