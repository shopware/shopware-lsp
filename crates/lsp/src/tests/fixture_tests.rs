use swlsp_syntax::TextRange;

use crate::Problem;

use crate::fixture::{Marked, normalize, parse, render};

fn range(start: u32, end: u32) -> TextRange {
    TextRange::new(start.into(), end.into())
}

#[test]
fn parses_nested_and_empty_markers() {
    let (source, marked) = parse("a {|outer:b {|inner:c|} d|} e{|empty:|}");
    assert_eq!(source, "a b c d e");
    assert_eq!(
        marked,
        [
            Marked::new("outer", range(2, 7)),
            Marked::new("inner", range(4, 5)),
            Marked::new("empty", range(9, 9)),
        ]
    );
}

#[test]
fn renders_what_it_parses() {
    for fixture in [
        "plain",
        "{|a:|}start",
        "end{|a:|}",
        "x {|outer:y {|inner:z|}|} w",
        "x {|outer:{|inner:y|} z|} w",
        "x {|first:y|}{|second:z|}",
        "<?php {% {|a:é|} %} 😀{|b:😀|}",
        "{|a:|}{|b:|}",
    ] {
        let (source, marked) = parse(fixture);
        assert_eq!(render(&source, &marked), fixture);
    }
}

#[test]
fn normalizes_marker_order_of_equal_problems() {
    assert_eq!(normalize("{|b:x|}"), "{|b:x|}");
    let (source, _) = parse("xy");
    let rendered = render(
        &source,
        &[Marked::new("b", range(0, 2)), Marked::new("a", range(0, 2))],
    );
    assert_eq!(rendered, "{|a:{|b:xy|}|}");
}

#[test]
fn keeps_lone_closing_delimiters_outside_markers() {
    let (source, marked) = parse("{{ a|} }}");
    assert_eq!(source, "{{ a|} }}");
    assert!(marked.is_empty());
}

#[test]
#[should_panic(expected = "unclosed markers")]
fn rejects_unclosed_markers() {
    parse("{|a:x");
}

#[test]
fn check_returns_the_reported_problems_in_markup_order() {
    let (document, problems) =
        crate::fixture::check("file:///a.txt", "{|b:one|}\n{|a:two|}", |_| {
            vec![
                Problem {
                    id: "a".into(),
                    range: range(4, 7),
                    ..Problem::default()
                },
                Problem {
                    id: "b".into(),
                    range: range(0, 3),
                    ..Problem::default()
                },
            ]
        });
    assert_eq!(document.source(), "one\ntwo");
    let codes: Vec<&str> = problems.iter().map(|problem| problem.id.as_str()).collect();
    assert_eq!(codes, ["b", "a"]);
}

#[test]
#[should_panic(expected = "  same\n- {|a:x|}\n+ x\n")]
fn check_shows_the_lines_that_differ() {
    crate::fixture::check("file:///a.txt", "same\n{|a:x|}", |_| Vec::new());
}
