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

#[test]
fn references_variable_variables_and_attributed_constants_parse() {
    use crate::syntax::{ERROR, PHP_CONST_DECLARATION, PHP_UNARY_EXPRESSION, PHP_VARIABLE};
    let source = "<?php\n$$a = ${'b'} + $c->$$d + self::${$e}[1];\n$f = &$g;\nif (null !== $h = &self::$i[$j]) {}\n#[A]\nconst X = 1;\nfoo(&$k, [&$l]);\n$m = 'n'.++$o + .5 - 1e-3;\n";
    let parse = crate::parse(source);
    assert_eq!(parse.root().text().to_string(), source);
    assert!(parse.errors.is_empty(), "{:?}", parse.errors);
    let kinds = |kind| {
        parse
            .root()
            .descendants()
            .filter(|node| node.kind() == kind)
            .map(|node| node.text().to_string().trim().to_owned())
            .collect::<Vec<_>>()
    };
    assert!(kinds(ERROR).is_empty(), "{:?}", kinds(ERROR));
    let variables = kinds(PHP_VARIABLE);
    for name in ["$$a", "${'b'}", "$$d", "${$e}"] {
        assert!(
            variables.iter().any(|text| text == name),
            "{name}: {variables:?}"
        );
    }
    assert!(kinds(PHP_UNARY_EXPRESSION).contains(&"&$g".to_owned()));
    assert_eq!(kinds(PHP_CONST_DECLARATION), ["#[A]\nconst X = 1;"]);
}
