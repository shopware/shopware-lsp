use std::borrow::Cow;

use crate::syntax::normalize_flow_expression;

#[test]
fn normalize_flow_expression_removes_whitespace_and_nullsafe() {
    assert_eq!(
        normalize_flow_expression(" \n$customer ?-> getId()\t"),
        "$customer->getId()"
    );
    assert_eq!(normalize_flow_expression("$items['key']"), "$items['key']");
}

#[test]
fn normalize_compact_flow_expression_borrows() {
    assert!(matches!(
        normalize_flow_expression(" \n\t$customer->getId()\r\n"),
        Cow::Borrowed("$customer->getId()")
    ));
}
