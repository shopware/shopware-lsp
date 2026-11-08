use super::super::ShopwareCriteriaAnalyzer;
use super::*;

fn criteria_problems(source: &str) -> Vec<Problem> {
    ShopwareCriteriaAnalyzer::new()
        .analyze(
            &Context::background(),
            &document("file:///Criteria.php", source),
        )
        .expect("analyze")
}

#[test]
fn shopware_criteria_analyzer_only_offers_safe_local_rewrite() {
    let problems = criteria_problems(
        r#"<?php
function load(string $id): void {
    $criteria = new Criteria();
    $criteria->addFilter(new EqualsFilter('id', $id));
}
"#,
    );
    assert_eq!(problems.len(), 1);
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
    consume(new EqualsFilter('id', $id));
}
"#,
    );
    assert_eq!(problems.len(), 1);
    assert_eq!(problems[0].payload.as_ref().unwrap()["safe"], false);
}
