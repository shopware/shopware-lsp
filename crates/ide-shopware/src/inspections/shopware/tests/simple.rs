use std::collections::HashSet;

use swlsp_language::LanguageId;
use swlsp_php::PhpIndex;

use crate::diagnostics::{ShopwarePhpLocalAnalyzer, ShopwarePhpSemanticAnalyzer};

use crate::inspections::analyzer_inspection;
use crate::inspections::test_support::check_diagnostics;

#[test]
fn shopware_php_local_inspection_declares_and_reports_rules() {
    let inspection = analyzer_inspection(ShopwarePhpLocalAnalyzer::new());
    let definition = inspection.definition();
    assert_eq!(definition.id, "shopware.php.local");
    assert_eq!(definition.languages, vec![LanguageId::Php]);
    assert_eq!(definition.problems.len(), 9);
    check_diagnostics(
        inspection.as_ref(),
        "file:///LocalRules.php",
        "<?php {|shopware.php.superglobal:$_GET|}['value'];",
    );
}

#[test]
fn shopware_php_semantic_inspection_declares_independent_rules() {
    let dir = tempfile::tempdir().unwrap();
    let index = PhpIndex::open(dir.path(), None).unwrap();
    let definition =
        analyzer_inspection(ShopwarePhpSemanticAnalyzer::new(Some(index))).definition();
    assert_eq!(definition.id, "shopware.php.semantic");
    assert_eq!(definition.languages, vec![LanguageId::Php]);
    assert_eq!(definition.problems.len(), 16);
    let mut seen = HashSet::new();
    for problem in &definition.problems {
        assert!(seen.insert(problem.id.clone()), "{}", problem.id);
    }
}
