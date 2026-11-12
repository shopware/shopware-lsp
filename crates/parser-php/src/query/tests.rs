#[test]
fn clone_call_form_takes_arguments() {
    use crate::syntax::{PHP_ARGUMENT_LIST, PHP_CLONE_EXPRESSION, PHP_MEMBER_CALL};
    use swlsp_syntax::SyntaxNode;
    let source = "<?php\n$a = clone($this, ['name' => 1]);\n$b = clone($x)->copy();\n$c = clone $y->copy();\n$d = clone(\n";
    let parse = crate::parse(source);
    assert_eq!(parse.root().text().to_string(), source);
    let clones: Vec<SyntaxNode> = parse
        .root()
        .descendants()
        .filter(|node| node.kind() == PHP_CLONE_EXPRESSION)
        .collect();
    assert_eq!(clones.len(), 4);
    // The call form has an argument list; the member access after it
    // applies to the clone, not to its operand.
    let arguments = clones[0].first_child().unwrap();
    assert_eq!(arguments.kind(), PHP_ARGUMENT_LIST);
    assert_eq!(arguments.children().count(), 2);
    assert_eq!(
        clones[1].parent().map(|parent| parent.kind()),
        Some(PHP_MEMBER_CALL)
    );
    assert_eq!(clones[2].text().to_string().trim(), "clone $y->copy()");
    // An incomplete call keeps a usable tree.
    assert!(
        clones[3]
            .first_child()
            .is_some_and(|node| node.kind() == PHP_ARGUMENT_LIST)
    );
}

#[test]
fn suppression_operator_after_not() {
    let source = "<?php\nif (!@mkdir($d, 0o700)) { x(); }\n$a = @f() + -@g();\n";
    let parse = crate::parse(source);
    assert_eq!(parse.root().text().to_string(), source);
    assert!(parse.errors.is_empty(), "{:?}", parse.errors);
}
