//! Port of `internal/parser/twig/parser/{parser,regression,fuzz}_test.go`.

use swlsp_syntax::{SyntaxNode, debug_tree};

use super::*;

fn find_nodes(root: &SyntaxNode, kind: Kind) -> Vec<SyntaxNode> {
    root.descendants()
        .filter(|node| node.kind() == kind)
        .collect()
}

/// Parses `source`, asserting losslessness and no errors.
fn parse_clean(source: &str) -> SyntaxNode {
    let result = parse(source);
    let root = result.root();
    assert!(
        result.errors.is_empty(),
        "unexpected errors for {source:?}: {:?}\n{}",
        result.errors,
        debug_tree(&root)
    );
    assert_eq!(root.text().to_string(), source, "lossless violated");
    root
}

fn first_word(node: &SyntaxNode) -> String {
    node.children_with_tokens()
        .filter_map(|element| element.into_token())
        .find(|token| token.kind() == TK_WORD)
        .map(|token| token.text().to_owned())
        .unwrap_or_default()
}

#[test]
fn root_parses_plain_text_losslessly() {
    let root = parse_clean("hello world");
    // Plain text is wrapped in an HTML_TEXT node by the real grammar.
    let want = "ROOT@0..11\n  HTML_TEXT@0..11\n    TK_WORD@0..5 \"hello\"\n    TK_WHITESPACE@5..6 \" \"\n    TK_WORD@6..11 \"world\"";
    assert_eq!(debug_tree(&root), want);
}

#[test]
fn parse_empty() {
    let root = parse_clean("");
    assert_eq!(debug_tree(&root), "ROOT@0..0");
}

#[test]
fn quoted_html_attribute_may_contain_greater_than() {
    let root = parse_clean(r#"<button data-action="click->live#emit"></button>"#);
    let attributes = find_nodes(&root, HTML_ATTRIBUTE);
    assert_eq!(attributes.len(), 1);
    assert_eq!(
        attributes[0].text().to_string(),
        r#" data-action="click->live#emit""#
    );
}

#[test]
fn quoted_html_attribute_may_contain_less_than() {
    let root = parse_clean(r#"<sw-button :disabled="selection.length < 1" data-label="a<!--b" />"#);
    let strings: Vec<_> = find_nodes(&root, HTML_STRING_INNER)
        .iter()
        .map(|node| node.text().to_string())
        .collect();
    assert_eq!(strings, ["selection.length < 1", "a<!--b"]);
}

#[test]
fn vue_html_attribute_names_include_arguments_and_modifiers() {
    let root = parse_clean(
        r#"<sw-card :title.sync="title" v-bind:count.number="count" @save.stop="save" v-on:close.once="close" #footer />"#,
    );
    let names: Vec<_> = find_nodes(&root, HTML_ATTRIBUTE)
        .iter()
        .map(first_word)
        .collect();
    assert_eq!(
        names,
        [
            ":title.sync",
            "v-bind:count.number",
            "@save.stop",
            "v-on:close.once",
            "#footer"
        ]
    );
}

#[test]
fn html_attribute_names_reclassify_twig_keywords() {
    let root = parse_clean(
        r#"<th :style="{ width: column.width }" @click="select($event)" block="name" v-bind:for="value"></th>"#,
    );
    let names: Vec<_> = find_nodes(&root, HTML_ATTRIBUTE)
        .iter()
        .map(first_word)
        .collect();
    assert_eq!(names, [":style", "@click", "block", "v-bind:for"]);
}

#[test]
fn parser_predicates() {
    let source = "{%- if";
    let tokens = lexer::lex(source);
    assert_eq!(
        tokens.iter().map(|token| token.kind).collect::<Vec<_>>(),
        [TK_CURLY_PERCENT_MINUS, TK_WHITESPACE, TK_IF]
    );
    let p = new_parser(source, tokens);
    assert!(p.at_twig_block_open());
    assert!(!p.at_twig_var_open());
    assert!(!p.at_twig_comment_open());
    assert!(p.at_twig_tag(TK_IF));
    assert!(!p.at_twig_tag(TK_ENDIF));
}

#[test]
fn component_block_accepts_quoted_name() {
    let root = parse_clean("{% component 'Alert' with {message: 'Hi'} %}Hello{% endcomponent %}");
    assert_eq!(find_nodes(&root, TWIG_COMPONENT_STARTING_BLOCK).len(), 1);
    assert!(find_nodes(&root, TWIG_LITERAL_STRING).len() >= 2);
}

#[test]
fn function_call_accepts_modern_colon_named_arguments() {
    for source in [
        "{{ include('card.html.twig', with_context: false) }}",
        "{{ include('card.html.twig', with_context = false) }}",
    ] {
        let root = parse_clean(source);
        assert_eq!(find_nodes(&root, TWIG_NAMED_ARGUMENT).len(), 1, "{source}");
    }
}

// -- regression_test.go ------------------------------------------------------

/// Raw-text elements followed by an unknown twig-open that recovers to EOF
/// must not panic (totality invariant).
#[test]
fn parse_raw_text_eof_totality() {
    for input in [
        "<script<{%",
        "<style </ {% ",
        "<textarea for {{~",
        "<style ] {%- ",
        "<script>{%",
        "<title x {%",
    ] {
        let result = parse(input);
        assert_eq!(result.root().text().to_string(), input, "lossless broken");
    }
}

#[test]
fn parse_twig_control_flow_in_html_attributes() {
    let root = parse_clean(r#"<element {% if enabled %}foo="yes"{% endif %} {{ attributes }}/>"#);
    let in_attribute_list = |kind: Kind| {
        root.descendants().any(|node| {
            node.kind() == kind
                && node
                    .parent()
                    .is_some_and(|parent| parent.kind() == HTML_ATTRIBUTE_LIST)
        })
    };
    assert!(in_attribute_list(TWIG_IF), "{}", debug_tree(&root));
    assert!(in_attribute_list(TWIG_VAR), "{}", debug_tree(&root));
}

#[test]
fn parse_twig_documentation_comments_on_bindings() {
    let source = r"{% types {
    ## The user displayed by the page.
    user?: 'App\\User',
} %}
{% set
    ## The user's given name.
    first_name,
    ## The user's family name.
    last_name
    = user.first_name, user.last_name
%}
{% for
    ## The product identifier.
    product_id,
    ## The product for the current iteration.
    product
    in products
%}{% endfor %}
{% macro input(
    ## The HTML field name.
    name,
    ## The initial field value.
    value = null,
) %}{% endmacro %}";
    let root = parse_clean(source);
    assert_eq!(find_nodes(&root, TWIG_COMMENT).len(), 6);
}

// -- fuzz_test.go seeds ------------------------------------------------------

const NASTY_SEEDS: &[&str] = &[
    "",
    "{",
    "{%",
    "{{",
    "{#",
    "{% block",
    "{% block a %}",
    "{{ a b c",
    "<",
    "</",
    r#"<div class="{{ x }}">"#,
    "<a href='{{ '}}'>",
    r#"{{ "a\"b'c" }}"#,
    "#{",
    r##"{{ "#{name}" }}"##,
    "<!--",
    "<!",
    "\r\n\r\n{%- if -%}\r\n",
    "{% ééé %}",
    "\u{1F600}\u{1F4A9}<{{ }}",
    "{{{{{{{{{{{{{{{{",
    "{%%%%%%%%%%%%%%%%",
    "<a<b<c<d<e<f<g<h",
    "{% component 'x' with { a: { b: [1, 2",
    "twig:foo:bar:baz",
    "{{(A,",
    "<script<{%",
    "<style </ {% ",
    "<style ] {%- ",
    "{{(a,}}",
];

#[test]
fn nasty_seeds_are_lossless_with_valid_error_ranges() {
    for source in NASTY_SEEDS {
        let result = parse(source);
        assert_eq!(
            result.root().text().to_string(),
            *source,
            "lossless invariant broken"
        );
        let length = source.len() as u32;
        for error in &result.errors {
            assert!(
                error.range.start() <= error.range.end() && u32::from(error.range.end()) <= length,
                "invalid error range {:?} for {source:?}",
                error.range
            );
        }
        let _ = result.debug_dump();
    }
}

/// Every prefix of a realistic template parses losslessly (incomplete input
/// while typing).
#[test]
fn every_prefix_is_lossless() {
    let source = r#"{% sw_extends '@Storefront/base.html.twig' %}
{% block content %}
    <div class="card {{ cls|default('x') }}" {% if a %}data-a="1"{% endif %}>
        {{ "Hello #{name}"|trans({'%n%': n}) }}
        {# ludtwig-ignore rule-a #}
        {% for item in items|filter(i => i.active) %}{{ item.label }}{% else %}-{% endfor %}
        <script>var a = "</div>";</script>
    </div>
{% endblock %}"#;
    for end in (0..=source.len()).filter(|&index| source.is_char_boundary(index)) {
        let prefix = &source[..end];
        assert_eq!(parse(prefix).root().text().to_string(), prefix);
    }
}
