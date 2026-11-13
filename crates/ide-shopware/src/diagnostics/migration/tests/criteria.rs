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
    assert_eq!(payload["argument"], "[$id]");
}

#[test]
fn shopware_criteria_analyzer_does_not_offer_ambiguous_rewrite() {
    let problems = criteria_problems(
        r#"<?php
function load(string $id): void {
    $first = new Criteria();
    $second = new Criteria();
    $first->addFilter(new EqualsFilter('{|shopware.criteria.id-filter:id|}', $id));
    $second->addFilter(new EqualsFilter('{|shopware.criteria.id-filter:id|}', $id), new EqualsFilter('active', true));
    $third = new Criteria();
    if ($id !== '') {
        $third->addFilter(new EqualsAnyFilter('{|shopware.criteria.id-filter:id|}', [$id]));
    }
}
function reassigned(string $id): void {
    $criteria = new Criteria();
    $criteria = new Criteria();
    $criteria->addFilter(new EqualsFilter('{|shopware.criteria.id-filter:id|}', $id));
}
"#,
    );
    // The first is alone in its statement and block: it can move. A
    // statement adding more, a conditional filter and an ambiguous Criteria
    // cannot.
    let safe: Vec<bool> = problems
        .iter()
        .map(|problem| problem.payload.as_ref().unwrap()["safe"] == true)
        .collect();
    assert_eq!(safe, [true, false, false, false]);
    assert_eq!(problems[2].payload.as_ref().unwrap().get("argument"), None);
}

#[test]
fn shopware_criteria_analyzer_leaves_filters_that_do_not_restrict_to_the_ids() {
    criteria_problems(
        r#"<?php
function load(string $id, array $ids, ?string $except): void {
    $criteria = new Criteria();
    $criteria->addFilter(new NotFilter(NotFilter::CONNECTION_AND, [new EqualsFilter('id', $except)]));
    $criteria->addFilter(new OrFilter([new EqualsFilter('parentId', $id), new EqualsFilter('id', $id)]));
    $criteria->addPostFilter(new MultiFilter(MultiFilter::CONNECTION_OR, [new EqualsAnyFilter('id', $ids)]));
    $criteria->addPostFilter(new EqualsFilter('id', $id));
    consume(new EqualsFilter('id', $id));
}
"#,
    );
}
