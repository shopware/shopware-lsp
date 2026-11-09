use swlsp_parser_twig::query as twigquery;
use swlsp_parser_twig::syntax::TWIG_BLOCK;
use swlsp_syntax::{SyntaxNodeExt, TextRange};

use crate::*;

#[test]
fn twig_parse() {
    let content = "{% block foo %}{% endblock %}";
    let file = parse_twig("test", content);
    assert_eq!(file.path, "test");
    let block = &file.blocks["foo"];
    assert_eq!(block.name, "foo");
    assert_eq!(&content[block.name_range], "foo");
    assert_eq!(block.line, 1);
    assert!(!block.hash.is_empty());
    assert_eq!(block.text, "{% block foo %}{% endblock %}");
}

#[test]
fn twig_parse_sw_extends() {
    let file = parse_twig(
        "test",
        "{% sw_extends '@Storefront/storefront/base.html.twig' %}",
    );
    assert_eq!(file.path, "test");
    assert_eq!(file.extends_file, "@Storefront/storefront/base.html.twig");
}

#[test]
fn twig_parse_scoped_sw_extends() {
    let file = parse_twig(
        "test",
        "{% sw_extends { template: '@Storefront/storefront/base.html.twig', scopes: ['default'] } %}",
    );
    assert_eq!(file.extends_file, "@Storefront/storefront/base.html.twig");
}

#[test]
fn nested_block() {
    let tpl = "
{% block a %}
\t{% block b %}
\t\t{% block c %}
\t\t{% endblock %}
\t{% endblock %}
{% endblock %}
";
    let file = parse_twig("test", tpl);
    assert_eq!(file.blocks.len(), 3);
    assert_eq!(file.blocks["a"].line, 2);
    assert!(!file.blocks["a"].hash.is_empty());
    assert_eq!(file.blocks["b"].line, 3);
    assert_eq!(file.blocks["c"].line, 4);
}

#[test]
fn blocks_with_html_content() {
    let tpl = r#"{% block base_body %}
    <body>
        {% block base_header %}
            <header>
                {% block base_header_inner %}{% endblock %}
            </header>
        {% endblock %}

        {% block base_content %}
            <div class="content">
                content here
            </div>
        {% endblock %}
    </body>
{% endblock %}"#;
    let file = parse_twig("test", tpl);
    let body = &file.blocks["base_body"];
    assert_eq!(body.line, 1);
    assert!(body.text.contains("<body>"));
    assert!(body.text.contains("{% endblock %}"));
    assert!(file.blocks.contains_key("base_header"));
    assert!(file.blocks.contains_key("base_header_inner"));
    assert!(
        file.blocks["base_content"]
            .text
            .contains(r#"<div class="content">"#)
    );
}

#[test]
fn block_with_version_comment() {
    let tpl = "{% sw_extends '@Storefront/storefront/base.html.twig' %}

{# shopware-block: abc123def456@6.4.15.0 #}
{% block foo %}
    content
{% endblock %}
";
    let file = parse_twig("test", tpl);
    let comment = file.blocks["foo"]
        .version_comment
        .as_ref()
        .expect("version comment");
    assert_eq!(comment.hash, "abc123def456");
    assert_eq!(comment.version, "6.4.15.0");
    assert_eq!(comment.line, 3);
}

#[test]
fn block_deprecation_preserves_version_and_migration_hint() {
    let file = parse_twig(
        "test",
        "{# @deprecated tag:v6.7.0 - use page_new #}\n{% block page_old %}content{% endblock %}",
    );
    assert_eq!(
        file.blocks["page_old"].deprecation,
        "tag:v6.7.0 - use page_new"
    );
}

#[test]
fn redundant_block_override_requires_exact_resolved_parent() {
    let block = TwigBlock {
        hash: "same".into(),
        text: "{% block content %}same{% endblock %}".into(),
        ..TwigBlock::default()
    };
    let parent = TwigBlockHash {
        hash: block.hash.clone(),
        text: block.text.clone(),
        absolute_path: "/project/parent.html.twig".into(),
        ..TwigBlockHash::default()
    };
    let mut resolution = UpstreamResolution {
        candidates: vec![parent.clone()],
        chain_paths: vec![parent.absolute_path.clone()],
        parent_resolved: true,
    };
    assert_eq!(
        redundant_block_override(&block, &resolution),
        Some(parent.clone())
    );
    resolution.parent_resolved = false;
    assert!(redundant_block_override(&block, &resolution).is_none());
    resolution.parent_resolved = true;
    resolution.chain_paths = vec!["/project/different.html.twig".into()];
    assert!(redundant_block_override(&block, &resolution).is_none());
    resolution.chain_paths = vec![parent.absolute_path.clone()];
    resolution.candidates[0].text = "{% block content %}different{% endblock %}".into();
    assert!(redundant_block_override(&block, &resolution).is_none());
}

#[test]
fn parse_block_body_preserves_exact_whitespace_and_range() {
    let block = "{% block content %}\n    <div>same</div>\n{% endblock %}";
    let (body, range) = parse_block_body(block).expect("body");
    assert_eq!(body, "\n    <div>same</div>");
    assert_eq!(&block[range], body);
    assert_eq!(range, TextRange::new(19.into(), 39.into()));
    assert!(!is_parent_delegation(&body));
    assert!(is_parent_delegation("\n  {{ parent() }}\n"));
}

#[test]
fn documentation_comment_text_supports_twig_329_delimiters() {
    for (source, expected) in [
        ("{## Description #}", "Description"),
        ("{## Description ##}", "Description"),
        ("{##- Description #}", "Description"),
        ("{## Description -##}", "Description"),
        ("{##~ Description ~##}", "Description"),
        (" ## Binding docs ", "Binding docs"),
    ] {
        assert_eq!(
            documentation_comment_text(source).as_deref(),
            Some(expected),
            "{source}"
        );
    }
    assert!(documentation_comment_text("{##}").is_none());
    assert!(documentation_comment_text("{# regular #}").is_none());
}

#[test]
fn documentation_before_combines_comments_and_honors_boundaries() {
    let source = "{## First line. ##}
{# regular comment #}
{## Second line. #}
{% block documented %}{% endblock %}
{## Not attached. #}
Visible text
{% block undocumented %}{% endblock %}";
    let parse = swlsp_parser_twig::parse(source);
    assert!(parse.errors.is_empty());
    let blocks = twigquery::nodes(&parse.root(), &[TWIG_BLOCK]);
    assert_eq!(blocks.len(), 2);
    assert_eq!(
        documentation_before(&blocks[0]),
        "First line.\nSecond line."
    );
    assert_eq!(documentation_before(&blocks[1]), "");
}

#[test]
fn documentation_for_node_finds_binding_comments() {
    let source = "{% set
    ## Number of unread messages.
    unread_count = messages|length
%}
{% for
    ## Product identifier.
    product_id,
    ## Product in the current iteration.
    product
    in products
%}{% endfor %}";
    let parse = swlsp_parser_twig::parse(source);
    assert!(parse.errors.is_empty());
    let root = parse.root();
    for (name, expected) in [
        ("unread_count", "Number of unread messages."),
        ("product_id", "Product identifier."),
        ("product\n", "Product in the current iteration."),
    ] {
        let offset = source.find(name).expect(name);
        let node = root.node_at((offset as u32).into()).expect(name);
        assert_eq!(documentation_for_node(&node), expected, "{name}");
    }
}

#[test]
fn compare_fold_matches_lowercase_ordering() {
    let values = [
        "",
        "Product",
        "product",
        "ProductDetail",
        "product_list",
        "Änderung",
        "änderung",
    ];
    for left in values {
        for right in values {
            assert_eq!(
                swlsp_textutil::lowercase(left).cmp(&swlsp_textutil::lowercase(right)),
                compare_fold(left, right),
                "{left} / {right}"
            );
        }
    }
    assert_eq!(
        compare_fold("ProductDetail", "product_list"),
        std::cmp::Ordering::Greater
    );
}
