//! Section 5: control structures.

use super::{fixed, stable};

#[test]
fn if_elseif_else() {
    stable(
        "if ($expr1) {
    // if body
} elseif ($expr2) {
    // elseif body
} else {
    // else body;
}
",
    );
    fixed(
        "if($expr1){\n    a();\n}\nelse if ($expr2)\n{\n    b();\n}\nelse\n    c();\n",
        "if ($expr1) {\n    a();\n} elseif ($expr2) {\n    b();\n} else {\n    c();\n}\n",
    );
}

#[test]
fn bodies_are_enclosed_by_braces() {
    fixed(
        "if ($a) return 1;\nwhile ($b) $c++;\nforeach ($d as $e) echo $e;\nfor ($i = 0; $i < 3; $i++) f($i);\ndo g(); while ($h);\n",
        "if ($a) {\n    return 1;\n}\nwhile ($b) {\n    $c++;\n}\nforeach ($d as $e) {\n    echo $e;\n}\nfor ($i = 0; $i < 3; $i++) {\n    f($i);\n}\ndo {\n    g();\n} while ($h);\n",
    );
    // Nested bodies without braces keep the `else` of their `if`.
    fixed(
        "if ($a) if ($b) c(); else d();\n",
        "if ($a) {\n    if ($b) {\n        c();\n    } else {\n        d();\n    }\n}\n",
    );
}

#[test]
fn conditions_over_several_lines() {
    stable(
        "if (
    $expr1
    && $expr2
) {
    // if body
} elseif (
    $expr3
    && $expr4
) {
    // elseif body
}

while (
    $expr1
    && $expr2
) {
    // structure body
}

do {
    // structure body;
} while (
    $expr1
    && $expr2
);
",
    );
    fixed(
        "if ($expr1\n    && $expr2) {\n    a();\n}\n",
        "if (\n    $expr1\n    && $expr2\n) {\n    a();\n}\n",
    );
}

#[test]
fn switch_and_match() {
    stable(
        "switch ($expr) {
    case 0:
        echo 'First case, with a break';
        break;
    case 1:
        echo 'Second case, which falls through';
        // no break
    case 2:
    case 3:
    case 4:
        echo 'Third case, return instead of break';
        return;
    default:
        echo 'Default case';
        break;
}

switch (
    $expr1
    && $expr2
) {
    // structure body
}

$returnValue = match ($expr) {
    0 => 'First case',
    1, 2, 3 => multipleCases(),
    default => 'Default case',
};
",
    );
    fixed(
        "switch($expr){\ncase 0:\necho 'a';\nbreak;\ndefault:\n  echo 'b';\n}\n$r = match($expr){\n  0=>'First case',\n  default=>'Default case'\n};\n",
        "switch ($expr) {\n    case 0:\n        echo 'a';\n        break;\n    default:\n        echo 'b';\n}\n$r = match ($expr) {\n    0 => 'First case',\n    default => 'Default case',\n};\n",
    );
}

#[test]
fn the_arms_of_a_match_stand_one_per_line() {
    fixed(
        "$a = match ($b) { 1, 2 => 'c', default => 'd' };\n$e = match (true) {};\n",
        "$a = match ($b) {\n    1, 2 => 'c',\n    default => 'd',\n};\n$e = match (true) {};\n",
    );
}

#[test]
fn loops() {
    stable(
        "while ($expr) {
    // structure body
}

do {
    // structure body;
} while ($expr);

for ($i = 0; $i < 10; $i++) {
    // for body
}

for (
    $i = 0;
    $i < 10;
    $i++
) {
    // for body
}

foreach ($iterable as $key => $value) {
    // foreach body
}
",
    );
    fixed(
        "for($i=0;$i<10;$i++){\n    a();\n}\nforeach($iterable as $key=>$value)\n{\n    b();\n}\n",
        "for ($i = 0; $i < 10; $i++) {\n    a();\n}\nforeach ($iterable as $key => $value) {\n    b();\n}\n",
    );
}

#[test]
fn try_catch_finally() {
    stable(
        "try {
    // try body
} catch (FirstThrowableType $e) {
    // catch body
} catch (OtherThrowableType | AnotherThrowableType $e) {
    // catch body
} finally {
    // finally body
}
",
    );
    fixed(
        "try\n{\n    a();\n}\ncatch(A|B $e)\n{\n    b();\n}\nfinally\n{\n    c();\n}\n",
        "try {\n    a();\n} catch (A | B $e) {\n    b();\n} finally {\n    c();\n}\n",
    );
}

#[test]
fn the_alternative_syntax_keeps_its_keywords() {
    fixed(
        "if ($a):\necho 1;\nelseif ($b):\necho 2;\nelse:\necho 3;\nendif;\nforeach ($c as $d):\necho $d;\nendforeach;\n",
        "if ($a):\n    echo 1;\nelseif ($b):\n    echo 2;\nelse:\n    echo 3;\nendif;\nforeach ($c as $d):\n    echo $d;\nendforeach;\n",
    );
}

#[test]
fn boolean_operators_stand_on_one_side_of_the_line() {
    // At the end of every line, or at the beginning of every line.
    stable("if (\n    $a &&\n    $b\n) {\n    c();\n}\n");
    fixed(
        "if (\n    $a &&\n    $b\n    || $c\n) {\n    d();\n}\n",
        "if (\n    $a\n    && $b\n    || $c\n) {\n    d();\n}\n",
    );
}

#[test]
fn a_fall_through_comment_stays_in_the_body_of_its_case() {
    fixed(
        "switch ($a) {\ncase 1:\nb();\n// no break\ncase 2:\nc();\n}\n",
        "switch ($a) {\n    case 1:\n        b();\n        // no break\n    case 2:\n        c();\n}\n",
    );
}

#[test]
fn a_comment_between_a_brace_and_its_branch_keeps_the_branch_in_place() {
    stable("if ($a) {\n    b();\n}\n// otherwise\nelseif ($c) {\n    d();\n}\n");
    // The inserted brace follows the condition, not the comment about it.
    fixed(
        "if ($a) // why\n    b();\n",
        "if ($a) { // why\n    b();\n}\n",
    );
}

#[test]
fn a_body_is_indented_from_the_line_its_structure_starts_on() {
    fixed(
        "if (!($a\n    || $b)) {\n        return false;\n    }\n",
        "if (\n    !($a\n        || $b)\n) {\n    return false;\n}\n",
    );
    // A closure in a condition does not split it.
    stable("if (array_filter($a, function ($b) {\n    return $b;\n})) {\n    return false;\n}\n");
}
