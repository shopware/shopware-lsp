//! What the formatter leaves alone.

use super::{formatted, skipped};
use crate::Skipped;

#[test]
fn a_document_with_syntax_errors_is_left_as_it_is() {
    assert_eq!(skipped("<?php\nfunction f( {\n"), Skipped::SyntaxError);
    assert_eq!(skipped("<?php\nclass {\n"), Skipped::SyntaxError);
}

#[test]
fn an_empty_file_stays_empty() {
    assert_eq!(
        crate::format("", crate::Options::default()).as_deref(),
        Some("")
    );
}

#[test]
fn strings_are_not_touched() {
    let source = "<?php\n\n$a = \"x  {$b['c']}\n   y\";\n$d = 'e  ,  f';\n";
    assert_eq!(formatted(source), source);
}

#[test]
fn operators_that_would_join_stay_apart() {
    assert_eq!(
        formatted("<?php\n\n$a = - -$b;\n$c = + +$d;\n"),
        "<?php\n\n$a = - -$b;\n$c = + +$d;\n"
    );
}

#[test]
fn a_reference_is_not_moved_into_a_call() {
    // `new $a->b` creates an object of the class named by `$a->b`: the
    // parentheses follow the property, and a call in the chain keeps them out.
    assert_eq!(
        formatted("<?php\n\n$c = new $a->b;\n$d =& $e;\n$f = new $g;\n$h = new $i->j()->k;\n"),
        "<?php\n\n$c = new $a->b();\n$d = &$e;\n$f = new $g();\n$h = new $i->j()->k;\n"
    );
}

#[test]
fn parentheses_that_hold_one_expression_take_no_trailing_comma() {
    let source = "<?php\n\ninclude(\n    $a\n);\n$b = new (\n    $c\n)($d);\nexit(\n    1\n);\n$e = empty(\n    $f\n);\n";
    assert_eq!(formatted(source), source);
    // A method may be named like a construct.
    assert_eq!(
        formatted("<?php\n\n$a->include(\n    $b\n);\nisset(\n    $c\n);\n"),
        "<?php\n\n$a->include(\n    $b,\n);\nisset(\n    $c,\n);\n"
    );
}

#[test]
fn variable_variables_keep_their_spelling() {
    assert_eq!(
        formatted("<?php\n\n$$a=${'b'} + ${$c}[0];\nfoo( $$d ,$e );\n"),
        "<?php\n\n$$a = ${'b'} + ${$c}[0];\nfoo($$d, $e);\n"
    );
}

#[test]
fn a_template_keeps_its_lines_and_gets_its_spaces_set() {
    let source = "<?php declare(strict_types=1) ?>\n<html>\n<body>\n    <?php if( $a==1 ) : ?>\n        <b><?=$b ?></b>\n    <?php   endif ;\n    foreach($c as $d){\n        echo  $d ,\"\\n\";\n    }\n    ?>\n</body>\n</html>\n";
    assert_eq!(
        formatted(source),
        "<?php declare(strict_types=1) ?>\n<html>\n<body>\n    <?php if ($a == 1): ?>\n        <b><?= $b ?></b>\n    <?php endif;\n    foreach ($c as $d) {\n        echo $d, \"\\n\";\n    }\n    ?>\n</body>\n</html>\n"
    );
}

#[test]
fn a_type_in_parentheses_stands_apart_from_the_visibility() {
    let source = "<?php\n\nclass A\n{\n    protected (B&C)|null $p = null;\n    public private(set) int $q;\n}\n";
    assert_eq!(formatted(source), source);
    assert_eq!(
        formatted("<?php\n\nclass A\n{\n    protected(B&C)|null $p = null;\n}\n"),
        "<?php\n\nclass A\n{\n    protected (B&C)|null $p = null;\n}\n"
    );
}

#[test]
fn print_is_written_like_a_call_but_not_before_a_cast() {
    assert_eq!(
        formatted("<?php\n\nprint ($a . \"\\n\");\nprint (int) $a;\n"),
        "<?php\n\nprint($a . \"\\n\");\nprint (int) $a;\n"
    );
}

#[test]
fn arguments_and_parameters_hold_no_empty_lines() {
    assert_eq!(
        formatted(
            "<?php\n\nfunction f(\n    int $a,\n\n    int $b,\n) {\n    g(\n        $a,\n\n        // note\n        $b,\n\n        $c,\n    );\n    $x = [\n        1,\n\n        2,\n    ];\n}\n"
        ),
        "<?php\n\nfunction f(\n    int $a,\n    int $b,\n) {\n    g(\n        $a,\n        // note\n        $b,\n        $c,\n    );\n    $x = [\n        1,\n\n        2,\n    ];\n}\n"
    );
}

#[test]
fn an_empty_statement_after_a_semicolon_takes_a_line_of_its_own() {
    assert_eq!(
        formatted("<?php\n\n$a = 1;;\nif ($a) {\n    b();;\n};\n"),
        "<?php\n\n$a = 1;\n;\nif ($a) {\n    b();\n    ;\n};\n"
    );
}

#[test]
fn skipped_elements_of_a_split_list_are_set_apart() {
    assert_eq!(
        formatted("<?php\n\nlist(\n    $a,,,\n    $b\n) = $c;\n"),
        "<?php\n\nlist(\n    $a, , ,\n    $b,\n) = $c;\n"
    );
}
