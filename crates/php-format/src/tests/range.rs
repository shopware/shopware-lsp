//! Formatting a selection.

use crate::{Options, try_format_range};

/// Formats the part of `source` between the two `|` marks.
#[track_caller]
fn selected(marked: &str) -> String {
    let start = marked.find('|').expect("start mark");
    let end = marked.rfind('|').expect("end mark") - 1;
    let source = marked.replace('|', "");
    let parse = swlsp_parser_php::parse(&source);
    try_format_range(
        &source,
        &parse.root(),
        !parse.errors.is_empty(),
        start..end,
        Options::default(),
    )
    .expect("formatted")
}

#[test]
fn only_the_selected_lines_change() {
    assert_eq!(
        selected("<?php\n\n$a=1;\n|$b=2;|\n$c=3;\n"),
        "<?php\n\n$a=1;\n$b = 2;\n$c=3;\n"
    );
}

#[test]
fn a_selection_inside_a_line_takes_the_whole_line() {
    assert_eq!(
        selected("<?php\n\n$a=1;\n$b=|2|;\n$c=3;\n"),
        "<?php\n\n$a=1;\n$b = 2;\n$c=3;\n"
    );
}

#[test]
fn a_selection_that_ends_at_a_line_start_leaves_that_line() {
    assert_eq!(
        selected("<?php\n\n|$a=1;\n|$b=2;\n"),
        "<?php\n\n$a = 1;\n$b=2;\n"
    );
}

#[test]
fn a_formatted_selection_changes_nothing() {
    let source = "<?php\n\n$a=1;\n$b = 2;\n$c=3;\n";
    assert_eq!(selected("<?php\n\n$a=1;\n|$b = 2;|\n$c=3;\n"), source);
}

#[test]
fn selected_lines_take_their_indentation_and_leave_the_others() {
    assert_eq!(
        selected("<?php\n\nfunction f()\n{\n  $a=1;\n|  $b=2;|\n  $c=3;\n}\n"),
        "<?php\n\nfunction f()\n{\n  $a=1;\n    $b = 2;\n  $c=3;\n}\n"
    );
}

#[test]
fn a_change_that_needs_another_grows_the_selection_to_its_construct() {
    // The braces around the body open on the selected line and close after
    // the next one: the whole `if` is formatted, and nothing else.
    assert_eq!(
        selected("<?php\n\n$a=1;\n|if ($a)|\n    b( );\n$c=3;\n"),
        "<?php\n\n$a=1;\nif ($a) {\n    b();\n}\n$c=3;\n"
    );
}

#[test]
fn a_selection_of_everything_formats_the_document() {
    assert_eq!(
        selected("|<?php\n$a=1;\n$b=2;\n|"),
        "<?php\n\n$a = 1;\n$b = 2;\n"
    );
}

#[test]
fn a_document_with_syntax_errors_is_left_alone() {
    let source = "<?php\nfunction f( {\n";
    let parse = swlsp_parser_php::parse(source);
    assert!(
        try_format_range(
            source,
            &parse.root(),
            true,
            0..source.len(),
            Options::default()
        )
        .is_err()
    );
}
