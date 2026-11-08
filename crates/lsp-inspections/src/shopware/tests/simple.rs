use std::collections::HashSet;

use swlsp_language::LanguageId;
use swlsp_php::PhpIndex;

use crate::shopware::*;
use crate::test_support::{document, inspect};

#[test]
fn shopware_php_local_inspection_declares_and_reports_rules() {
    let inspection = new_shopware_php_local();
    let definition = inspection.definition();
    assert_eq!(definition.id, "shopware.php.local");
    assert_eq!(definition.languages, vec![LanguageId::Php]);
    assert_eq!(definition.problems.len(), 9);
    let document = document("file:///LocalRules.php", "<?php $_GET['value'];");
    let problems = inspect(inspection.as_ref(), &document);
    assert_eq!(problems.len(), 1);
    assert_eq!(
        problems[0].id.as_str(),
        swlsp_lsp_diagnostics::SHOPWARE_PHP_SUPERGLOBAL_CODE
    );
}

#[test]
fn shopware_php_semantic_inspection_declares_independent_rules() {
    let dir = tempfile::tempdir().unwrap();
    let index = PhpIndex::open(dir.path(), None).unwrap();
    let definition = new_shopware_php_semantic(Some(index)).definition();
    assert_eq!(definition.id, "shopware.php.semantic");
    assert_eq!(definition.languages, vec![LanguageId::Php]);
    assert_eq!(definition.problems.len(), 10);
    let mut seen = HashSet::new();
    for problem in &definition.problems {
        assert!(seen.insert(problem.id.clone()), "{}", problem.id);
    }
}
