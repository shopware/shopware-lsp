//! The fixture corpus (adopted from shopware-cli) lives in `testdata/formatter`.

use super::*;

fn options(insert_spaces: bool, tab_size: usize, twig_block_indent_children: bool) -> Options {
    Options {
        insert_spaces,
        tab_size,
        twig_block_indent_children,
    }
}

fn assert_formatting(name: &str, input: &str, expected: &str, options: Options) {
    let formatted = format(input, options);
    assert_eq!(formatted, expected, "{name} ({options:?})");
    let second = format(&formatted, options);
    assert_eq!(
        second, expected,
        "{name} ({options:?}): formatter must be idempotent"
    );
}

/// Each fixture holds input, Administration output and optionally Storefront
/// output separated by five hyphens. Every expected output is idempotent.
#[test]
fn formatting_fixtures() {
    let directory = concat!(env!("CARGO_MANIFEST_DIR"), "/testdata/formatter");
    let mut paths: Vec<_> = std::fs::read_dir(directory)
        .expect("read fixture directory")
        .map(|entry| entry.expect("fixture entry").path())
        .filter(|path| path.is_file())
        .collect();
    paths.sort();
    assert!(!paths.is_empty());
    for path in paths {
        let name = path.file_name().unwrap().to_string_lossy().into_owned();
        let data = std::fs::read_to_string(&path).expect("read fixture");
        let parts: Vec<&str> = data
            .splitn(3, "-----")
            .map(|part| part.trim_matches('\n'))
            .collect();
        assert!(parts.len() >= 2, "{name}: missing expected output");
        assert_formatting(&name, parts[0], parts[1], options(true, 4, false));
        if parts.len() == 3 {
            assert_formatting(&name, parts[0], parts[2], options(true, 4, true));
        }
    }
}

#[test]
fn formatting_preserves_tilde_whitespace_control() {
    let formatted = format(
        "{%~ if enabled ~%}{{~ value ~}}{%~ endif ~%}",
        options(true, 2, true),
    );
    assert_eq!(
        formatted,
        "{%~ if enabled ~%}\n  {{~ value ~}}\n{%~ endif ~%}"
    );
}

#[test]
fn formatting_uses_tabs_from_lsp_options() {
    let formatted = format("<div><span>text</span></div>", options(false, 8, true));
    assert_eq!(formatted, "<div>\n\t<span>text</span>\n</div>");
}

#[test]
fn formatting_options_are_isolated_across_concurrent_calls() {
    let input = "{% block content %}<div><span>text</span></div>{% endblock %}";
    let all = [options(true, 2, true), options(false, 8, false)];
    let expected: Vec<String> = all.iter().map(|option| format(input, *option)).collect();
    assert_ne!(expected[0], expected[1]);

    std::thread::scope(|scope| {
        for worker in 0..16 {
            let index = worker % all.len();
            let (option, expected) = (all[index], &expected[index]);
            scope.spawn(move || {
                for _ in 0..50 {
                    assert_eq!(&format(input, option), expected);
                }
            });
        }
    });
}

#[test]
fn formatting_preserves_twig_documentation_comments() {
    let input = "{##- Main page content. -##}\n{% block content %}{% endblock %}";
    assert_eq!(format(input, options(true, 4, true)), input);
}

#[test]
fn formatting_preserves_inline_documentation_comment_lines() {
    let input = r"{% types {
    ## User displayed by the page.
    user: 'App\\User',
} %}
{% set
    ## Number of unread messages.
    unread_count = messages|length
%}
{% for
    ## Product identifier.
    product_id,
    ## Product in the current iteration.
    product
    in products
%}{% endfor %}
{% macro input(
    ## HTML field name.
    name,
) %}{% endmacro %}";
    let formatted = format(input, options(true, 4, true));
    for documentation in [
        "## User displayed by the page.\n",
        "## Number of unread messages.\n",
        "## Product identifier.\n",
        "## Product in the current iteration.\n",
        "## HTML field name.\n",
    ] {
        assert!(
            formatted.contains(documentation),
            "{documentation:?} missing in {formatted:?}"
        );
    }
    assert_eq!(format(&formatted, options(true, 4, true)), formatted);
}

#[test]
fn default_options_use_tabs() {
    assert_eq!(
        format("<div><span>text</span></div>", Options::default()),
        "<div>\n\t<span>text</span>\n</div>"
    );
}

#[test]
fn incomplete_input_is_preserved() {
    for source in [
        "<<Success>>",
        "<div",
        "{% block a %}<span>",
        "{{ a",
        "{% if a %}<b>x</b>",
    ] {
        // Must not panic; unclosed constructs fall back to raw text.
        let _ = format(source, options(true, 4, true));
    }
    assert_eq!(format("<<Success>>", options(true, 4, true)), "<<Success>>");
}
