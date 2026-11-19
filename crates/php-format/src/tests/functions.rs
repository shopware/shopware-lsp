//! Sections 2.6, 4.4, 4.5, 4.7, 4.8 and 7: functions, calls and closures.

use super::{fixed, stable};

#[test]
fn method_braces_stand_on_their_own_lines() {
    stable(
        "class ClassName
{
    public function fooBarBaz($arg1, &$arg2, $arg3 = [])
    {
        // method body
    }
}

function fooBarBaz($arg1, &$arg2, $arg3 = [])
{
    // function body
}
",
    );
    fixed(
        "function fooBarBaz ( $arg1 , & $arg2,$arg3=[] ) {\n    return 1;\n}\n",
        "function fooBarBaz($arg1, &$arg2, $arg3 = [])\n{\n    return 1;\n}\n",
    );
}

#[test]
fn an_empty_body_is_abbreviated() {
    stable(
        "class Point
{
    public function __construct(private int $x, private int $y) {}

    public function __clone(
        public readonly int $x,
        public readonly int $y,
    ) {}
}
",
    );
    fixed("function f()\n{\n}\n", "function f() {}\n");
}

#[test]
fn parameters_over_several_lines_stand_one_per_line_with_a_trailing_comma() {
    stable(
        "class ClassName
{
    public function aVeryLongMethodName(
        ClassTypeHint $arg1,
        &$arg2,
        array $arg3 = [],
    ) {
        // method body
    }
}
",
    );
    fixed(
        "function f(ClassTypeHint $arg1,\n    &$arg2, array $arg3 = [])\n{\n    return 1;\n}\n",
        "function f(\n    ClassTypeHint $arg1,\n    &$arg2,\n    array $arg3 = [],\n) {\n    return 1;\n}\n",
    );
    // On one line there is no trailing comma.
    fixed(
        "function f($a, $b,)\n{\n    return 1;\n}\n",
        "function f($a, $b)\n{\n    return 1;\n}\n",
    );
}

#[test]
fn return_types_follow_the_closing_parenthesis() {
    stable(
        "class ReturnTypeVariations
{
    public function functionName(int $arg1, $arg2): string
    {
        return 'foo';
    }

    public function anotherFunction(
        string $foo,
        string $bar,
        int $baz,
    ): string {
        return 'foo';
    }

    public function nullable(?string $arg1, ?int &$arg2): ?string
    {
        return 'foo';
    }
}
",
    );
    fixed(
        "function f(? string $a, int ... $b) : ? string\n{\n    return 'foo';\n}\n",
        "function f(?string $a, int ...$b): ?string\n{\n    return 'foo';\n}\n",
    );
    fixed(
        "function process(string $algorithm, & ... $parts)\n{\n    // processing\n}\n",
        "function process(string $algorithm, &...$parts)\n{\n    // processing\n}\n",
    );
}

#[test]
fn calls_have_no_spaces_around_their_parentheses() {
    stable("bar();\n$foo->bar($arg1);\nFoo::bar($arg2, $arg3);\n");
    fixed(
        "bar ();\n$foo -> bar( $arg1 );\nFoo :: bar($arg2 ,$arg3);\n",
        "bar();\n$foo->bar($arg1);\nFoo::bar($arg2, $arg3);\n",
    );
}

#[test]
fn a_static_call_stays_with_its_class() {
    fixed(
        "Foo\n    ::bar()\n    ->baz();\n",
        "Foo::bar()\n    ->baz();\n",
    );
}

#[test]
fn arguments_over_several_lines_stand_one_per_line() {
    stable(
        "$foo->bar(
    $longArgument,
    $longerArgument,
    $muchLongerArgument,
);
",
    );
    fixed(
        "$foo->bar($longArgument,\n    $longerArgument, $muchLongerArgument);\n",
        "$foo->bar(\n    $longArgument,\n    $longerArgument,\n    $muchLongerArgument,\n);\n",
    );
    // One argument over several lines does not split the list.
    stable(
        "somefunction($foo, $bar, [
    // ...
], $baz);

$app->get('/hello/{name}', function ($name) use ($app) {
    return 'Hello ' . $app->escape($name);
});
",
    );
}

#[test]
fn named_arguments_and_callable_references() {
    stable("somefunction($a, b: $b, c: 'c');\n$f = strlen(...);\n$g = $a->b(...);\n");
    fixed(
        "somefunction($a, b : $b, c:'c');\n$f = strlen( ... );\n",
        "somefunction($a, b: $b, c: 'c');\n$f = strlen(...);\n",
    );
}

#[test]
fn method_chains_are_indented_once() {
    stable(
        "$someInstance
    ->create()
    ->prepare()
    ->run();
",
    );
    fixed(
        "$someInstance\n->create()\n        ->prepare()\n  ->run();\n",
        "$someInstance\n    ->create()\n    ->prepare()\n    ->run();\n",
    );
}

#[test]
fn closures_have_spaces_around_their_keywords() {
    stable(
        "$closureWithArgs = function ($arg1, $arg2) {
    // body
};

$closureWithArgsAndVars = function ($arg1, $arg2) use ($var1, $var2) {
    // body
};
",
    );
    // This example of the specification is 85 characters long. At the
    // recommended 80 it takes the form the specification gives for long
    // argument lists; at the soft limit it stays.
    let example = "<?php\n\n$closureWithArgsVarsAndReturn = function ($arg1, $arg2) use ($var1, $var2): bool {\n    // body\n};\n";
    assert_eq!(
        super::formatted(example),
        "<?php\n\n$closureWithArgsVarsAndReturn = function (\n    $arg1,\n    $arg2,\n) use ($var1, $var2): bool {\n    // body\n};\n"
    );
    let soft = crate::Options {
        line_width: crate::SOFT_LINE_WIDTH,
        ..crate::Options::default()
    };
    assert_eq!(crate::format(example, soft).as_deref(), Some(example));
    fixed(
        "$f = function($arg1,$arg2)use($var1, $var2) : bool\n{\n    return true;\n};\n",
        "$f = function ($arg1, $arg2) use ($var1, $var2): bool {\n    return true;\n};\n",
    );
}

#[test]
fn closure_lists_over_several_lines() {
    stable(
        "$longArgs_noVars = function (
    $longArgument,
    $longerArgument,
    $muchLongerArgument,
) {
    // body
};

$noArgs_longVars = function () use (
    $longVar1,
    $longerVar2,
    $muchLongerVar3,
) {
    // body
};

$longArgs_longVars = function (
    $longArgument,
    $longerArgument,
    $muchLongerArgument,
) use (
    $longVar1,
    $longerVar2,
    $muchLongerVar3,
) {
    // body
};

$longArgs_shortVars = function (
    $longArgument,
    $longerArgument,
    $muchLongerArgument,
) use ($var1) {
    // body
};

$shortArgs_longVars = function ($arg) use (
    $longVar1,
    $longerVar2,
    $muchLongerVar3,
) {
    // body
};

$foo->bar(
    $arg1,
    function ($arg2) use ($var1) {
        // body
    },
    $arg3,
);
",
    );
}

#[test]
fn short_closures_have_no_space_after_fn() {
    stable(
        "$func = fn(int $x, int $y): int => $x + $y;

$func = fn(int $x, int $y): int
    => $x + $y;

$func = fn(
    int $x,
    int $y,
): int
    => $x + $y;

$result = $collection->reduce(fn(int $x, int $y): int => $x + $y, 0);
",
    );
    fixed(
        "$func = fn (int $x, int $y) : int=>$x + $y;\n$g = fn($x) =>\n    $x * 2;\n",
        "$func = fn(int $x, int $y): int => $x + $y;\n$g = fn($x)\n    => $x * 2;\n",
    );
    fixed(
        "$f = fn &($x) => $x;\nrequire ('a.php');\n",
        "$f = fn&($x) => $x;\nrequire('a.php');\n",
    );
}

#[test]
fn a_chain_over_several_lines_starts_its_first_method_on_a_new_line() {
    fixed(
        "$query = $this->createQueryBuilder('a')\n    ->where('b')\n    ->getQuery();\n",
        "$query = $this\n    ->createQueryBuilder('a')\n    ->where('b')\n    ->getQuery();\n",
    );
    // Properties before the first method stay with the object, and a chain
    // on one line stays there.
    fixed(
        "$a = $this->repository->search($b)\n    ->first();\n$c = $d->e()->f();\n",
        "$a = $this->repository\n    ->search($b)\n    ->first();\n$c = $d->e()->f();\n",
    );
    // A call over several lines does not split the chain it is in.
    stable("$a = $this->b([\n    1,\n])->c();\n");
}

#[test]
fn a_comment_before_the_brace_of_a_split_signature_follows_the_brace() {
    fixed(
        "function f(\n    $a,\n): void // why\n{\n    g();\n}\n",
        "function f(\n    $a,\n): void { // why\n    g();\n}\n",
    );
}
