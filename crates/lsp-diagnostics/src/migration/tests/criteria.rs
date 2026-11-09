use super::super::ShopwareCriteriaAnalyzer;
use super::*;

fn criteria_problems(fixture: &str) -> Vec<Problem> {
    check_problems(
        &ShopwareCriteriaAnalyzer::new(),
        "file:///Criteria.php",
        fixture,
    )
    .1
}

#[test]
fn shopware_criteria_analyzer_only_offers_safe_local_rewrite() {
    let problems = criteria_problems(
        r#"<?php
function load(string $id): void {
    $criteria = new Criteria();
    $criteria->addFilter(new EqualsFilter('{|shopware.criteria.id-filter:id|}', $id));
}
"#,
    );
    let payload = problems[0].payload.as_ref().unwrap();
    assert_eq!(payload["safe"], true);
    assert_eq!(payload["argument"], "$id");
}

#[test]
fn shopware_criteria_analyzer_does_not_offer_ambiguous_rewrite() {
    let problems = criteria_problems(
        r#"<?php
function load(string $id): void {
    $first = new Criteria();
    $second = new Criteria();
    consume(new EqualsFilter('{|shopware.criteria.id-filter:id|}', $id));
}
"#,
    );
    assert_eq!(problems[0].payload.as_ref().unwrap()["safe"], false);
}
