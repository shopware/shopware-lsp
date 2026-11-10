use swlsp_parser_twig::query as twigquery;
use swlsp_parser_twig::syntax::TWIG_LITERAL_STRING;

// -- snippet ------------------------------------------------------------------

fn twig_literals(code: &str) -> Vec<swlsp_syntax::SyntaxNode> {
    let parse = swlsp_parser_twig::parse(code);
    twigquery::nodes(&parse.tree.root(), &[TWIG_LITERAL_STRING])
}

#[test]
fn snippet_definition_pattern_matching() {
    for (code, key, frontend, admin) in [
        (
            "{{ 'checkout.cart.title'|trans }}",
            "checkout.cart.title",
            true,
            false,
        ),
        (
            "{{ $tc('sw-settings.index.title') }}",
            "sw-settings.index.title",
            false,
            true,
        ),
        (
            "{{ $t('global.actions.save') }}",
            "global.actions.save",
            false,
            true,
        ),
        (
            "{{ $tc('sw-product.list.count', items.length) }}",
            "sw-product.list.count",
            false,
            true,
        ),
        (
            "{{ 'checkout.cart.items'|trans({'%count%': count}) }}",
            "checkout.cart.items",
            true,
            false,
        ),
    ] {
        let node = twig_literals(code)
            .into_iter()
            .find(|candidate| twigquery::string_value(candidate).as_deref() == Some(key))
            .unwrap_or_else(|| panic!("no string node {key}"));
        assert_eq!(
            twigquery::string_in_filter(&node, &["trans"]),
            frontend,
            "{code}"
        );
        assert_eq!(
            twigquery::string_in_function(&node, &["$tc", "$t"]),
            admin,
            "{code}"
        );
        assert_eq!(twigquery::string_value(&node).as_deref(), Some(key));
    }
}

#[test]
fn snippet_definition_edge_cases() {
    for (code, frontend, admin) in [
        (
            "{% block content %}{{ $tc('my.key') }}{% endblock %}",
            false,
            true,
        ),
        ("{% if $tc('condition.key') %}yes{% endif %}", false, true),
        ("{% set label = 'my.label'|trans %}", true, false),
        (
            "{{ $tc('first.key') }} - {{ $tc('second.key') }}",
            false,
            true,
        ),
    ] {
        let strings = twig_literals(code);
        assert!(!strings.is_empty());
        assert_eq!(
            twigquery::string_in_filter(&strings[0], &["trans"]),
            frontend,
            "{code}"
        );
        assert_eq!(
            twigquery::string_in_function(&strings[0], &["$tc", "$t"]),
            admin,
            "{code}"
        );
    }
}
