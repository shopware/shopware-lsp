use swlsp_syntax::TextSize;

use crate::{DiagnosticId, TextDocument, comment_suppression_reader};

/// Lines (zero-based) on which `code` is suppressed.
fn suppressed(
    uri: &str,
    source: &str,
    kinds: &'static [swlsp_syntax::Kind],
    code: &str,
) -> Vec<usize> {
    let document = TextDocument::new(uri, source, 1);
    let suppressions = comment_suppression_reader(kinds)(&document);
    let mut offset = 0;
    let mut lines = Vec::new();
    for (index, line) in source.split('\n').enumerate() {
        let code_offset = offset + (line.len() - line.trim_start().len());
        if suppressions.suppresses(
            TextSize::from(code_offset as u32),
            &DiagnosticId::from(code),
        ) {
            lines.push(index);
        }
        offset += line.len() + 1;
    }
    lines
}

const TWIG: &[swlsp_syntax::Kind] = &[swlsp_parser_twig::syntax::TWIG_COMMENT];
const YAML: &[swlsp_syntax::Kind] = &[swlsp_parser_yaml::syntax::TK_COMMENT];

#[test]
fn twig_comments_suppress_the_next_code_line_or_their_own() {
    let source = "{# @noinspection twig.macro.missing #}\n\n{# other #}\n{{ a() }}\n{{ b() }} {# @noinspection twig.macro.missing #}\n{{ c() }}\n";
    assert_eq!(
        suppressed("file:///t.html.twig", source, TWIG, "twig.macro.missing"),
        [3, 4]
    );
    assert!(suppressed("file:///t.html.twig", source, TWIG, "twig.template.missing").is_empty());
}

#[test]
fn directives_without_codes_suppress_everything_and_codes_match_case_insensitively() {
    let source = "{# @noinspection #}\n{{ a() }}\n{# @NoInspection Twig.Macro.Missing, other #}\n{{ b() }}\n";
    assert_eq!(
        suppressed("file:///t.html.twig", source, TWIG, "twig.macro.missing"),
        [1, 3]
    );
    assert_eq!(
        suppressed("file:///t.html.twig", source, TWIG, "anything"),
        [1]
    );
}

#[test]
fn yaml_comments_use_the_same_rules() {
    let source = "services:\n  # @noinspection symfony.service.missing\n  app.a: '@missing'\n  app.b: '@missing' # @noinspection all\n  app.c: '@missing'\n";
    assert_eq!(
        suppressed(
            "file:///services.yaml",
            source,
            YAML,
            "symfony.service.missing"
        ),
        [2, 3]
    );
}

#[test]
fn sources_without_the_marker_suppress_nothing() {
    let source = "{# noinspection-like text #}\n{{ a() }}\n";
    assert!(suppressed("file:///t.html.twig", source, TWIG, "x").is_empty());
}
