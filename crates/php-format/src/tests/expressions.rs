//! Sections 6, 10 and 11: operators, heredocs and arrays; and comments.

use super::{fixed, stable};

#[test]
fn unary_operators_stand_by_their_operand() {
    stable("$i++;\n++$j;\n$intValue = (int) $input;\n$a = !$b;\n$c = -$d;\n$e = $f - -$g;\n");
    fixed(
        "$i ++;\n++ $j;\n$intValue = ( int )$input;\n",
        "$i++;\n++$j;\n$intValue = (int) $input;\n",
    );
}

#[test]
fn binary_and_ternary_operators_are_surrounded_by_spaces() {
    stable(
        "if ($a === $b) {
    $foo = $bar ?? $a ?? $b;
} elseif ($a > $b) {
    $foo = $a + $b * $c;
}

$variable = $foo ? 'foo' : 'bar';
$variable = $foo ?: 'bar';
",
    );
    fixed(
        "$foo=$a+$b*$c.$d;\n$v = $foo?'foo':'bar';\n$w = $foo ? : 'bar';\n$x = $a instanceof B&&$c|$d;\n",
        "$foo = $a + $b * $c . $d;\n$v = $foo ? 'foo' : 'bar';\n$w = $foo ?: 'bar';\n$x = $a instanceof B && $c | $d;\n",
    );
}

#[test]
fn expressions_continue_one_level_deeper() {
    fixed(
        "$a = $b\n&& $c\n&& $d;\n$e = $f\n? $g\n: $h;\nreturn $i\n?? $j\n?? $k;\n",
        "$a = $b\n    && $c\n    && $d;\n$e = $f\n    ? $g\n    : $h;\nreturn $i\n    ?? $j\n    ?? $k;\n",
    );
}

#[test]
fn a_value_on_the_line_after_its_assignment_keeps_its_operands_aligned() {
    stable("$a =\n    'b' .\n    'c' . // why\n    'd';\n");
}

#[test]
fn arrays_use_the_short_syntax_and_one_value_per_line_when_split() {
    stable(
        "$arr = [];

$arr1 = ['single', 'line', 'declaration'];

$arr2 = [
    'multi',
    'line',
    'declaration',
    ['values' => 1, 5, 7],
    [
        'nested',
        'array',
    ],
];
",
    );
    fixed(
        "$a = array( 'a'=>1,'b' => array(2) );\n$b = ['multi', 'line',\n    'declaration'];\n$c = [ 'x', 'y', ];\n[$d, , $e] = $f;\n",
        "$a = ['a' => 1, 'b' => [2]];\n$b = [\n    'multi',\n    'line',\n    'declaration',\n];\n$c = ['x', 'y'];\n[$d, , $e] = $f;\n",
    );
}

#[test]
fn a_list_over_several_lines_ends_with_a_comma() {
    fixed(
        "list($a,\n    $b) = $c;\n",
        "list(\n    $a,\n    $b,\n) = $c;\n",
    );
}

#[test]
fn heredocs_start_on_the_line_of_their_statement() {
    stable(
        "function allowed()
{
    $allowedHeredoc = <<<COMPLIANT
        This
        is
        a
        compliant
        heredoc
        COMPLIANT;

    $allowedNowdoc = <<<'COMPLIANT'
        This
        is
        a
        compliant
        nowdoc
        COMPLIANT;

    var_dump(
        'foo',
        <<<'COMPLIANT'
            This
            is
            a
            compliant
            parameter
            COMPLIANT,
        'bar',
    );
}
",
    );
    fixed(
        "function notAllowed()\n{\n    $a =\n<<<'EXAMPLE'\nThis\n  is\nEXAMPLE;\n    $b = <<<EXAMPLE\n            Too $deep\n            EXAMPLE . 'x';\n}\n",
        "function notAllowed()\n{\n    $a = <<<'EXAMPLE'\n        This\n          is\n        EXAMPLE;\n    $b = <<<EXAMPLE\n        Too $deep\n        EXAMPLE . 'x';\n}\n",
    );
}

#[test]
fn comments_stay_where_they_are() {
    stable(
        "/**
 * A function.
 *
 * @return int
 */
function f()
{
    // before
    $a = 1; // after

    /* block */
    return $a /* inline */ + 1;
    // last
}

$b = [
    // first
    1,
    2, // second
    // dangling
];
",
    );
    fixed(
        "function f()\n{\n        /**\n   * Doc.\n       */\n  // line\n    return 1;\n}\n",
        "function f()\n{\n    /**\n     * Doc.\n     */\n    // line\n    return 1;\n}\n",
    );
    // A comment before a closing brace belongs to the block.
    fixed(
        "if ($a) {\n    b();\n// otherwise\n} else {\n    c();\n}\n",
        "if ($a) {\n    b();\n    // otherwise\n} else {\n    c();\n}\n",
    );
}
