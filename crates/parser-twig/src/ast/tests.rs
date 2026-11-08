//! Port of `internal/parser/twig/ast/ast_test.go`.

use swlsp_syntax::{Kind, SyntaxNode, SyntaxToken};

use super::*;
use crate::parse;

fn parse_no_err(source: &str) -> SyntaxNode {
    let result = parse(source);
    assert!(
        result.errors.is_empty(),
        "unexpected parse errors for {source:?}: {:?}",
        result.errors
    );
    let root = result.root();
    assert_eq!(root.text().to_string(), source);
    root
}

fn find_node(root: &SyntaxNode, kind: Kind) -> Option<SyntaxNode> {
    root.descendants().find(|node| node.kind() == kind)
}

fn find<N: AstNode<Language = swlsp_syntax::Lang>>(root: &SyntaxNode, kind: Kind) -> N {
    find_node(root, kind)
        .and_then(N::cast)
        .unwrap_or_else(|| panic!("no node of kind {}", kind.name()))
}

fn must_token(token: Option<SyntaxToken>, want: &str) {
    let token = token.unwrap_or_else(|| panic!("expected token {want:?}, got none"));
    assert_eq!(token.text(), want);
}

#[test]
fn twig_block() {
    let root = parse_no_err("{% block content %}hello{% endblock %}");
    let block: TwigBlock = find(&root, TWIG_BLOCK);
    must_token(block.name(), "content");

    let starting = block.starting_block().expect("starting block");
    must_token(starting.name(), "content");
    assert_eq!(starting.twig_block().as_ref(), Some(&block));

    assert!(block.body().is_some());

    let ending = block.ending_block().expect("ending block");
    assert_eq!(ending.twig_block().as_ref(), Some(&block));
}

#[test]
fn html_tag_and_attributes() {
    let root = parse_no_err(r#"<div class="hello">world {{ 42 }}</div>"#);
    let tag: HtmlTag = find(&root, HTML_TAG);
    must_token(tag.name(), "div");
    assert!(!tag.is_self_closing());
    assert!(!tag.is_twig_component());
    assert!(tag.body().is_some());
    assert!(tag.ending_tag().is_some());

    let attributes: Vec<_> = tag.attributes().collect();
    assert_eq!(attributes.len(), 1);
    let attribute = &attributes[0];
    must_token(attribute.name(), "class");

    let value = attribute.value().expect("attribute value");
    let inner = value.inner().expect("value inner");
    assert_eq!(inner.syntax().text().to_string(), "hello");
    must_token(value.opening_quote(), "\"");
    must_token(value.closing_quote(), "\"");

    let starting = attribute.html_tag().expect("attribute grandparent");
    assert_eq!(Some(starting), tag.starting_tag());
}

#[test]
fn html_tag_attributes_iteration() {
    let root = parse_no_err(r#"<div first="1" second="2" third="3"></div>"#);
    let tag: HtmlTag = find(&root, HTML_TAG);
    let first = tag.attributes().next().expect("first attribute");
    must_token(first.name(), "first");
    assert_eq!(tag.attributes().count(), 3);

    let empty_root = parse_no_err("<div></div>");
    let empty_tag: HtmlTag = find(&empty_root, HTML_TAG);
    assert_eq!(empty_tag.attributes().count(), 0);
}

#[test]
fn self_closing_and_ending_tag() {
    let root = parse_no_err("<br/>");
    let tag: HtmlTag = find(&root, HTML_TAG);
    assert!(tag.is_self_closing());

    let root = parse_no_err("<span>x</span>");
    let ending: HtmlEndingTag = find(&root, HTML_ENDING_TAG);
    must_token(ending.name(), "span");
    assert!(!ending.is_twig_component());
    assert!(ending.html_tag().is_some());
}

#[test]
fn twig_component() {
    let root = parse_no_err(r#"<twig:my:component foo="bar"></twig:my:component>"#);
    let tag: HtmlTag = find(&root, HTML_TAG);
    assert!(tag.is_twig_component());
    let starting = tag.starting_tag().expect("starting tag");
    assert!(starting.is_twig_component());
    must_token(starting.name(), "twig:my:component");
    assert_eq!(starting.html_tag().as_ref(), Some(&tag));

    let ending: HtmlEndingTag = find(&root, HTML_ENDING_TAG);
    assert!(ending.is_twig_component());
    must_token(ending.name(), "twig:my:component");
}

#[test]
fn twig_binary_expression() {
    let root = parse_no_err("{{ 1 + 2 }}");
    let binary: TwigBinaryExpression = find(&root, TWIG_BINARY_EXPRESSION);
    must_token(binary.operator(), "+");
    let lhs = binary.lhs_expression().expect("lhs");
    assert_eq!(lhs.syntax().text().to_string(), " 1");
    let rhs = binary.rhs_expression().expect("rhs");
    assert_eq!(rhs.syntax().text().to_string(), " 2");
    assert_ne!(lhs, rhs);
}

#[test]
fn twig_literal_string() {
    let root = parse_no_err(r#"{{ "hello" }}"#);
    let literal: TwigLiteralString = find(&root, TWIG_LITERAL_STRING);
    let inner = literal.inner().expect("inner");
    assert_eq!(inner.syntax().text().to_string(), "hello");
    must_token(literal.opening_quote(), "\"");
    must_token(literal.closing_quote(), "\"");
    assert_ne!(literal.opening_quote(), literal.closing_quote());
}

#[test]
fn twig_literal_string_single_quote() {
    let root = parse_no_err("{{ 'x' }}");
    let literal: TwigLiteralString = find(&root, TWIG_LITERAL_STRING);
    must_token(literal.opening_quote(), "'");
    must_token(literal.closing_quote(), "'");
}

#[test]
fn twig_literal_string_interpolation() {
    let root = parse_no_err(r#"{{ "a #{b} c" }}"#);
    let inner: TwigLiteralStringInner = find(&root, TWIG_LITERAL_STRING_INNER);
    let interpolations: Vec<_> = inner.interpolations().collect();
    assert_eq!(interpolations.len(), 1);
    assert_eq!(interpolations[0].syntax().text().to_string(), "#{b}");
}

#[test]
fn html_string() {
    let root = parse_no_err(r#"<a href="index"></a>"#);
    let string: HtmlString = find(&root, HTML_STRING);
    let inner = string.inner().expect("inner");
    assert_eq!(inner.syntax().text().to_string(), "index");
    must_token(string.opening_quote(), "\"");
    must_token(string.closing_quote(), "\"");
}

#[test]
fn twig_extends() {
    let root = parse_no_err(r#"{% extends "base.html" %}"#);
    let extends: TwigExtends = find(&root, TWIG_EXTENDS);
    must_token(extends.extends_keyword(), "extends");
}

#[test]
fn twig_var() {
    let root = parse_no_err("{{ name }}");
    let var: TwigVar = find(&root, TWIG_VAR);
    let expression = var.expression().expect("expression");
    assert_eq!(expression.syntax().text().to_string(), " name");
}

#[test]
fn twig_literal_name() {
    let root = parse_no_err("{{ name }}");
    let name: TwigLiteralName = find(&root, TWIG_LITERAL_NAME);
    must_token(name.name(), "name");
}

#[test]
fn twig_filter() {
    let root = parse_no_err("{{ user|upper }}");
    let filter: TwigFilter = find(&root, TWIG_FILTER);
    let operand = filter.operand().expect("operand");
    assert_eq!(operand.syntax().text().to_string(), " user");
    let name = filter.filter().expect("filter operand");
    assert_eq!(name.syntax().text().to_string(), "upper");
    assert_ne!(operand, name);
}

#[test]
fn twig_function_call() {
    let root = parse_no_err("{{ path('home', {id: 1}) }}");
    let call: TwigFunctionCall = find(&root, TWIG_FUNCTION_CALL);
    let name = call.name_operand().expect("name operand");
    let literal: TwigLiteralName = find(name.syntax(), TWIG_LITERAL_NAME);
    must_token(literal.name(), "path");

    let arguments = call.arguments().expect("arguments");
    assert_eq!(arguments.syntax().text().to_string(), "('home', {id: 1})");
    assert_eq!(
        arguments
            .syntax()
            .children()
            .filter(|child| child.kind() == TWIG_EXPRESSION)
            .count(),
        2
    );
}

#[test]
fn ludtwig_directives() {
    let root = parse_no_err("{# ludtwig-ignore rule-a rule-b #}");
    let directive: LudtwigDirectiveIgnore = find(&root, LUDTWIG_DIRECTIVE_IGNORE);
    assert_eq!(directive.rules(), ["rule-a", "rule-b"]);
    let list: LudtwigDirectiveRuleList = find(&root, LUDTWIG_DIRECTIVE_RULE_LIST);
    assert_eq!(list.rule_names(), ["rule-a", "rule-b"]);
}

#[test]
fn ludtwig_directive_file_ignore() {
    let root = parse_no_err("{# ludtwig-ignore-file rule-x #}");
    let directive: LudtwigDirectiveFileIgnore = find(&root, LUDTWIG_DIRECTIVE_FILE_IGNORE);
    assert_eq!(directive.rules(), ["rule-x"]);
}

#[test]
fn ludtwig_directive_empty_rules() {
    let root = parse_no_err("{# ludtwig-ignore-file #}");
    let directive: LudtwigDirectiveFileIgnore = find(&root, LUDTWIG_DIRECTIVE_FILE_IGNORE);
    assert!(directive.rules().is_empty());
}

#[test]
fn cast_rejects_wrong_kind() {
    let root = parse_no_err("{{ 1 }}");
    let var = find_node(&root, TWIG_VAR).expect("var");
    assert!(TwigBlock::cast(var.clone()).is_none());
    assert!(TwigVar::cast(var).is_some());
}

#[test]
fn trim_helpers() {
    let cases = [
        ("{% block foo %}{% endblock %}", Trim::None, Trim::None),
        ("{%- block foo -%}{%- endblock -%}", Trim::All, Trim::All),
        (
            "{%~ block foo ~%}{%~ endblock ~%}",
            Trim::KeepNewlines,
            Trim::KeepNewlines,
        ),
        (
            "{%~ block foo -%}{% endblock %}",
            Trim::KeepNewlines,
            Trim::All,
        ),
    ];
    for (source, leading, trailing) in cases {
        let root = parse_no_err(source);
        let block: TwigBlock = find(&root, TWIG_BLOCK);
        let starting = block.starting_block().expect("starting block");
        assert_eq!(
            leading_trim(starting.syntax()),
            leading,
            "{source:?} leading"
        );
        assert_eq!(
            trailing_trim(starting.syntax()),
            trailing,
            "{source:?} trailing"
        );
    }
}

#[test]
fn trim_var_delimiter() {
    let root = parse_no_err("{{- name -}}");
    let var: TwigVar = find(&root, TWIG_VAR);
    assert_eq!(leading_trim(var.syntax()), Trim::All);
    assert_eq!(trailing_trim(var.syntax()), Trim::All);
}

#[test]
fn trim_comment_delimiter() {
    let root = parse_no_err("{#~ a comment ~#}");
    let comment: TwigComment = find(&root, TWIG_COMMENT);
    assert_eq!(leading_trim(comment.syntax()), Trim::KeepNewlines);
    assert_eq!(trailing_trim(comment.syntax()), Trim::KeepNewlines);
}
