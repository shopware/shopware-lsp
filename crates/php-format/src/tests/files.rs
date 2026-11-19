//! Sections 2 and 3: files, lines, keywords, the header of a file.

use super::{fixed, formatted, stable};
use crate::{Options, format};

#[test]
fn header_blocks_are_set_apart_by_one_empty_line() {
    let compliant = "<?php

/**
 * This file contains an example of coding styles.
 */

declare(strict_types=1);

namespace Vendor\\Package;

use Vendor\\Package\\{ClassA as A, ClassB, ClassC as C};
use Vendor\\Package\\SomeNamespace\\ClassD as D;
use Vendor\\Package\\AnotherNamespace\\ClassE as E;
use SomeVendor\\Pack\\ANamespace\\SubNamespace\\ClassF;

use function Vendor\\Package\\{functionA, functionB, functionC};
use function Another\\Vendor\\functionD;

use const Vendor\\Package\\{CONSTANT_A, CONSTANT_B, CONSTANT_C};
use const Another\\Vendor\\CONSTANT_D;

/**
 * FooBar is an example class.
 */
class FooBar
{
    // ... additional PHP code ...
}
";
    assert_eq!(formatted(compliant), compliant);
    let messy = "<?php
/**
 * This file contains an example of coding styles.
 */
declare( strict_types = 1 );
namespace Vendor\\Package;


use Vendor\\Package\\{ ClassA as A, ClassB, ClassC as C };

use Vendor\\Package\\SomeNamespace\\ClassD as D;
use Vendor\\Package\\AnotherNamespace\\ClassE as E;
use SomeVendor\\Pack\\ANamespace\\SubNamespace\\ClassF;
use function Vendor\\Package\\{functionA, functionB, functionC};
use function Another\\Vendor\\functionD;
use const Vendor\\Package\\{CONSTANT_A, CONSTANT_B, CONSTANT_C};
use const Another\\Vendor\\CONSTANT_D;
/**
 * FooBar is an example class.
 */
class FooBar {
    // ... additional PHP code ...
}";
    assert_eq!(formatted(messy), compliant);
}

#[test]
fn imports_are_ordered_by_kind_and_have_no_leading_backslash() {
    fixed(
        "use const A\\B;\nuse function A\\f;\nuse \\A\\C;\nuse A\\Enum\\D;\n",
        "use A\\C;\nuse A\\Enum\\D;\n\nuse function A\\f;\n\nuse const A\\B;\n",
    );
    fixed(
        "use A\\B, C\\D;\nuse function e, f;\n",
        "use A\\B;\nuse C\\D;\n\nuse function e;\nuse function f;\n",
    );
}

#[test]
fn compound_namespaces_over_several_lines_end_with_a_comma() {
    stable(
        "use Vendor\\Package\\SomeNamespace\\{
    SubnamespaceOne\\ClassA,
    SubnamespaceOne\\ClassB,
    SubnamespaceTwo\\ClassY,
    ClassZ,
};
",
    );
    fixed(
        "use Vendor\\Package\\{ClassA,\n  ClassB};\n",
        "use Vendor\\Package\\{\n    ClassA,\n    ClassB,\n};\n",
    );
}

#[test]
fn the_closing_tag_is_left_out_and_the_file_ends_with_one_line_feed() {
    assert_eq!(
        formatted("<?php\n\necho 1;\n\n\n?>\n"),
        "<?php\n\necho 1;\n"
    );
    assert_eq!(formatted("<?php echo 1;"), "<?php\n\necho 1;\n");
    assert_eq!(formatted("<?php\r\n\r\necho 1;\r\n"), "<?php\n\necho 1;\n");
}

#[test]
fn one_statement_per_line_without_trailing_white_space() {
    fixed(
        "$a = 1;   $b = 2;  \n\n\n\n$c = 3;\n",
        "$a = 1;\n$b = 2;\n\n$c = 3;\n",
    );
}

#[test]
fn code_is_indented_by_four_spaces() {
    fixed(
        "function f()\n{\n\tif ($a) {\n\t\treturn 1;\n\t}\n}\n",
        "function f()\n{\n    if ($a) {\n        return 1;\n    }\n}\n",
    );
}

#[test]
fn keywords_types_and_constants_are_lower_case() {
    fixed(
        "FUNCTION f(INT $a, String $b = NULL): Bool\n{\n    RETURN TRUE;\n}\n",
        "function f(int $a, string $b = null): bool\n{\n    return true;\n}\n",
    );
    // Names that read like keywords keep their case.
    stable(
        "use App\\Enum\\Status;\n\n$a = Foo::LIST;\n$b = $c->Print();\n$d = new Enum\\Match();\n",
    );
}

#[test]
fn casts_take_their_short_form() {
    fixed(
        "$a = (integer)$b + ( BOOLEAN ) $c . (double)  $d;\n",
        "$a = (int) $b + (bool) $c . (float) $d;\n",
    );
}

#[test]
fn declare_has_no_spaces_and_a_block_declare_has_its_brace_on_the_line() {
    fixed(
        "declare( ticks = 1 )\n{\n    // some code\n}\n",
        "declare(ticks=1) {\n    // some code\n}\n",
    );
}

#[test]
fn the_opening_tag_keeps_its_declare_only_where_a_project_asks_for_it() {
    let source = "<?php declare(strict_types=1);\n\nnamespace A;\n";
    assert_eq!(
        formatted(source),
        "<?php\n\ndeclare(strict_types=1);\n\nnamespace A;\n"
    );
    let shopware = Options {
        declare_on_opening_tag: true,
        ..Options::default()
    };
    assert_eq!(format(source, shopware).as_deref(), Some(source));
    // The option keeps what is there; it does not join the lines.
    assert_eq!(
        format("<?php\ndeclare(strict_types=1);\n", shopware).as_deref(),
        Some("<?php\n\ndeclare(strict_types=1);\n")
    );
}

#[test]
fn long_lines_are_split_where_a_list_a_chain_or_an_operator_allows() {
    // 80 characters fit.
    let fits = format!("$a = foo('{}');\n", "x".repeat(67));
    assert_eq!(fits.len(), 81);
    stable(&fits);
    // A project may ask for the soft limit of 120 characters.
    let longer = format!("<?php\n\n$a = foo('{}', $b);\n", "x".repeat(90));
    let soft = Options {
        line_width: crate::SOFT_LINE_WIDTH,
        ..Options::default()
    };
    assert_eq!(format(&longer, soft).as_deref(), Some(longer.as_str()));
    assert_eq!(
        formatted(&longer),
        format!(
            "<?php\n\n$a = foo(\n    '{}',\n    $b,\n);\n",
            "x".repeat(90)
        )
    );
    fixed(
        &format!(
            "$result = $this->service->process($firstArgument, $secondArgument, '{}', $fourthArgument);\n",
            "x".repeat(50)
        ),
        &format!(
            "$result = $this->service->process(\n    $firstArgument,\n    $secondArgument,\n    '{}',\n    $fourthArgument,\n);\n",
            "x".repeat(50)
        ),
    );
    fixed(
        &format!(
            "$result = $this->repository->createQueryBuilder()->select()->where()->andWhere()->orderBy()->setMaxResults()->get{}();\n",
            "X".repeat(30)
        ),
        &format!(
            "$result = $this->repository\n    ->createQueryBuilder()\n    ->select()\n    ->where()\n    ->andWhere()\n    ->orderBy()\n    ->setMaxResults()\n    ->get{}();\n",
            "X".repeat(30)
        ),
    );
    fixed(
        &format!(
            "if ($firstCondition{0} && $secondCondition{0} || $thirdCondition{0} && $fourthCondition{0}) {{\n    a();\n}}\n",
            "X".repeat(20)
        ),
        &format!(
            "if (\n    $firstCondition{0} && $secondCondition{0}\n    || $thirdCondition{0}\n    && $fourthCondition{0}\n) {{\n    a();\n}}\n",
            "X".repeat(20)
        ),
    );
    // One long string offers no place to split: the limit is a soft one.
    stable(&format!("$a = '{}';\n", "x".repeat(150)));
}

#[test]
fn the_interpreter_line_of_a_script_is_kept() {
    assert_eq!(
        formatted("#!/usr/bin/env php\n<?php\necho  .5;\n"),
        "#!/usr/bin/env php\n<?php\n\necho .5;\n"
    );
}

#[test]
fn long_lines_are_also_split_at_ternaries_closures_values_and_flags() {
    let x = "X".repeat(40);
    fixed(
        &format!(
            "$value = $condition{x} ? $this->first{x}($argument) : $this->second{x}($argument);\n"
        ),
        &format!(
            "$value = $condition{x}\n    ? $this->first{x}($argument)\n    : $this->second{x}($argument);\n"
        ),
    );
    fixed(
        &format!(
            "$callback = fn(Some{x} $first, Other{x} $second): bool => $first->id{x} === $second->id;\n"
        ),
        &format!(
            "$callback = fn(\n    Some{x} $first,\n    Other{x} $second,\n): bool\n    => $first->id{x} === $second->id;\n"
        ),
    );
    fixed(
        &format!(
            "class A\n{{\n    public const SOME_VERY_LONG_CONSTANT_NAME_{x} = 'some.event.name.that.is.long.{x}';\n}}\n"
        ),
        &format!(
            "class A\n{{\n    public const SOME_VERY_LONG_CONSTANT_NAME_{x} =\n        'some.event.name.that.is.long.{x}';\n}}\n"
        ),
    );
    fixed(
        &format!(
            "$flags = \\JSON_PRETTY_PRINT{x} | \\JSON_UNESCAPED_SLASHES{x} | \\JSON_UNESCAPED_UNICODE | \\JSON_HEX_TAG;\n"
        ),
        &format!(
            "$flags = \\JSON_PRETTY_PRINT{x}\n    | \\JSON_UNESCAPED_SLASHES{x}\n    | \\JSON_UNESCAPED_UNICODE\n    | \\JSON_HEX_TAG;\n"
        ),
    );
}
