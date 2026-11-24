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

#[test]
fn a_partial_application_has_placeholder_arguments() {
    use crate::query::{
        arguments, is_partial_application, is_placeholder_argument, is_variadic_placeholder,
    };
    use crate::syntax::{PHP_ARGUMENT_PLACEHOLDER, PHP_FUNCTION_CALL, PHP_MEMBER_CALL};
    let source = "<?php\n$a = f(?, 1, name: ?, ...);\n$b = $o?->m(\n    ?,\n);\n$c = f(...);\n$d = f($x ? 1 : 2, ...$rest);\n$e = g($x ?: 1, $y ?? 2);\n";
    let parse = crate::parse(source);
    assert_eq!(parse.root().text().to_string(), source);
    assert!(parse.errors.is_empty(), "{:?}", parse.errors);
    let calls: Vec<_> = parse
        .root()
        .descendants()
        .filter(|node| matches!(node.kind(), PHP_FUNCTION_CALL | PHP_MEMBER_CALL))
        .collect();
    let kinds = |index: usize| -> Vec<(bool, bool)> {
        arguments(&calls[index])
            .map(|argument| {
                (
                    is_placeholder_argument(&argument),
                    is_variadic_placeholder(&argument),
                )
            })
            .collect()
    };
    assert_eq!(
        kinds(0),
        [(true, false), (false, false), (true, false), (true, true)]
    );
    assert_eq!(kinds(1), [(true, false)]);
    assert_eq!(kinds(2), [(true, true)]);
    // A ternary and a spread are values.
    assert_eq!(kinds(3), [(false, false), (false, false)]);
    assert!(!is_partial_application(&calls[3]));
    assert!(!is_partial_application(&calls[4]));
    assert!(is_partial_application(&calls[0]) && is_partial_application(&calls[2]));
    let placeholders = parse
        .root()
        .descendants()
        .filter(|node| node.kind() == PHP_ARGUMENT_PLACEHOLDER)
        .count();
    assert_eq!(placeholders, 3);
    // A call that is still being written keeps a usable tree.
    for source in ["<?php\nf(?", "<?php\nf(?,", "<?php\nf(a: ?"] {
        assert_eq!(crate::parse(source).root().text().to_string(), source);
    }
}

#[test]
fn an_enum_case_takes_attributes() {
    use crate::syntax::{ERROR, PHP_ATTRIBUTE_GROUP, PHP_ENUM_CASE_DECLARATION};
    let source = "<?php\nenum E: int {\n    #[\\Override]\n    case A = 1;\n    #[A, B(1)] #[C]\n    case B = 2;\n    case C = 3;\n    #[\\Override]\n    public const X = self::A;\n}\n";
    let parse = crate::parse(source);
    assert_eq!(parse.root().text().to_string(), source);
    assert!(parse.root().descendants().all(|node| node.kind() != ERROR));
    let cases: Vec<_> = parse
        .root()
        .descendants()
        .filter(|node| node.kind() == PHP_ENUM_CASE_DECLARATION)
        .collect();
    let groups = |index: usize| {
        cases[index]
            .children()
            .filter(|node| node.kind() == PHP_ATTRIBUTE_GROUP)
            .count()
    };
    assert_eq!(cases.len(), 3);
    assert_eq!((groups(0), groups(1), groups(2)), (1, 2, 0));
}

#[test]
fn the_pipe_operator_binds_between_comparison_and_concatenation() {
    use crate::syntax::{ERROR, PHP_BINARY_EXPRESSION};
    let source =
        "<?php\n$a = $v |> f(1, ?) |> $o->m(...) == $w . 'x' |> g(?);\n$b = $x | $y;\n$c |= 1;\n";
    let parse = crate::parse(source);
    assert_eq!(parse.root().text().to_string(), source);
    assert!(parse.errors.is_empty(), "{:?}", parse.errors);
    assert!(parse.root().descendants().all(|node| node.kind() != ERROR));
    let binary: Vec<String> = parse
        .root()
        .descendants()
        .filter(|node| node.kind() == PHP_BINARY_EXPRESSION)
        .map(|node| node.text().to_string().trim().to_owned())
        .collect();
    assert_eq!(
        binary,
        [
            // The comparison is the loosest; the pipes group to the left.
            "$v |> f(1, ?) |> $o->m(...) == $w . 'x' |> g(?)",
            "$v |> f(1, ?) |> $o->m(...)",
            "$v |> f(1, ?)",
            "$w . 'x' |> g(?)",
            "$w . 'x'",
            "$x | $y",
        ]
    );
}
