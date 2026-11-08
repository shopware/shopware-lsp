use swlsp_indexer::ParsedFile;
use swlsp_parser_twig::query as twigquery;
use swlsp_parser_twig::syntax::TWIG_LITERAL_STRING;
use swlsp_syntax::{SyntaxNode, SyntaxNodeExt};

use crate::*;

fn caret(source: &str) -> (String, u32) {
    let offset = source.find("<caret>").expect("caret");
    (source.replacen("<caret>", "", 1), offset as u32)
}

fn root_of(path: &str, source: &str) -> SyntaxNode {
    ParsedFile::new(path, source.as_bytes().to_vec())
        .syntax_tree()
        .expect("tree")
        .root()
}

#[test]
fn macros_in_document_collect_signature_and_ranges() {
    let source = "{## Renders a form input. ##}
{% macro input(
    ## HTML field name.
    name,
    ## Initial field value.
    value = '',
    required = false,
) %}
<input name=\"{{ name }}\" value=\"{{ value }}\">
{% endmacro %}
";
    let path = "/project/templates/macros/forms.html.twig";
    let macros = macros_in_document(path, &root_of(path, source));
    assert_eq!(macros.len(), 1);
    let definition = &macros[0];
    assert_eq!(definition.name, "input");
    assert_eq!(
        definition.signature(),
        "input(name, value = '', required = false)"
    );
    assert_eq!(definition.documentation, "Renders a form input.");
    assert_eq!(definition.templates, ["macros/forms.html.twig"]);
    assert_eq!(&source[definition.name_range], "input");
    let names: Vec<&str> = definition
        .parameters
        .iter()
        .map(|p| p.name.as_str())
        .collect();
    assert_eq!(names, ["name", "value", "required"]);
    assert_eq!(definition.parameters[0].documentation, "HTML field name.");
    assert_eq!(
        definition.parameters[1].documentation,
        "Initial field value."
    );
    assert_eq!(definition.parameters[2].documentation, "");
}

#[test]
fn macro_references_resolve_namespace_direct_alias_and_self() {
    let source = "{% import 'macros/forms.html.twig' as forms %}
{% from 'macros/forms.html.twig' import input as field %}
{% import _self as local %}
{{ forms.input('name') }}
{{ field('name') }}
{{ local.helper() }}
{% macro helper() %}{% endmacro %}
";
    let path = "/project/templates/page.html.twig";
    let root = root_of(path, source);
    let references = macro_references_in_document(path, &root);
    let input: Vec<_> = references
        .iter()
        .filter(|r| r.name == "input" && r.role == MacroReferenceRole::Usage)
        .collect();
    let helper: Vec<_> = references.iter().filter(|r| r.name == "helper").collect();
    assert_eq!(input.len(), 3);
    for reference in input {
        assert_eq!(reference.templates, ["macros/forms.html.twig"]);
    }
    assert_eq!(helper.len(), 2);
    assert_eq!(helper[0].role, MacroReferenceRole::Declaration);
    assert_eq!(helper[1].role, MacroReferenceRole::Usage);
    assert_eq!(helper[1].templates, ["page.html.twig"]);

    let offset = source.find("forms.input").unwrap() + "forms.".len();
    let node = root.node_at((offset as u32).into()).unwrap();
    let context = macro_completion_at(path, &root, &node).expect("context");
    assert_eq!(context.templates, ["macros/forms.html.twig"]);
}

#[test]
fn macro_completion_at_incomplete_namespace_accessor() {
    let source = "{% import 'macros/forms.html.twig' as forms %}\n{{ forms. }}\n";
    let path = "/project/templates/page.html.twig";
    let root = root_of(path, source);
    let offset = source.rfind("forms.").unwrap() + "forms.".len();
    let node = root.node_at((offset as u32).into()).unwrap();
    let context = macro_completion_at(path, &root, &node).expect("context");
    assert_eq!(context.templates, ["macros/forms.html.twig"]);
}

#[test]
fn twig_test_expressions() {
    let source =
        "{{ first is positive }} {{ second is not positive(1) }} {{ third is same as(3) }}";
    let expressions = test_expressions(&swlsp_parser_twig::parse(source).root());
    let names: Vec<&str> = expressions.iter().map(|e| e.name.as_str()).collect();
    assert_eq!(names, ["positive", "positive", "same as"]);
    for expression in &expressions {
        assert_eq!(expression.name, &source[expression.range]);
    }
}

#[test]
fn twig_test_completion_at() {
    for (with_caret, prefix, found) in [
        ("{% if value is <caret> %}", "", true),
        ("{% if value is not <caret> %}", "", true),
        ("{{ value is pos<caret>itive }}", "pos", true),
        ("{% set valid = value is pos<caret> %}", "pos", true),
        ("{% if value <caret> %}", "", false),
        ("{# value is <caret> #}", "", false),
        ("{% if value is positive <caret> %}", "", false),
    ] {
        let (source, offset) = caret(with_caret);
        let expression = test_completion_at(
            &swlsp_parser_twig::parse(&source).root(),
            source.as_bytes(),
            offset,
        );
        assert_eq!(expression.is_some(), found, "{with_caret}");
        assert_eq!(
            expression.map(|e| e.name).unwrap_or_default(),
            prefix,
            "{with_caret}"
        );
    }
}

#[test]
fn types_tag_declarations_support_optional_multiline_and_escaped_types() {
    let source = r#"{% types {
    user: '\App\Entity\User',
    products?: 'array<App\\Entity\\Product>',
    "createdAt": 'DateTimeImmutable',
} %}"#;
    let declarations = types_tag_declarations(source.as_bytes());
    assert_eq!(declarations.len(), 3);
    assert_eq!(
        (declarations[0].name.as_str(), declarations[0].ty.as_str()),
        ("user", r"App\Entity\User")
    );
    assert!(!declarations[0].optional);
    assert_eq!(
        (declarations[1].name.as_str(), declarations[1].ty.as_str()),
        ("products", r"array<App\Entity\Product>")
    );
    assert!(declarations[1].optional);
    assert_eq!(
        (declarations[2].name.as_str(), declarations[2].ty.as_str()),
        ("createdAt", "DateTimeImmutable")
    );
    for declaration in &declarations {
        assert_eq!(declaration.name, &source[declaration.name_range]);
    }
}

#[test]
fn types_tag_declarations_include_documentation_comments() {
    let source = "{% types {
    ## Unique identifier.
    ## Used in generated markup.
    id: 'string',

    # A regular comment remains non-documentation.
    ## Whether several items can be open.
    multiple?: 'boolean',
} %}";
    let declarations = types_tag_declarations(source.as_bytes());
    assert_eq!(declarations.len(), 2);
    assert_eq!(
        declarations[0].documentation,
        "Unique identifier.\nUsed in generated markup."
    );
    assert_eq!(
        declarations[1].documentation,
        "Whether several items can be open."
    );
}

#[test]
fn types_tag_completion_and_class_references() {
    let (source, offset) = caret(
        r"{% types {
    user: 'App\\Ent<caret>ity\\User',
    products?: 'array<App\\Entity\\Product>',
} %}",
    );
    let bytes = source.as_bytes();
    let context = types_tag_completion_at(bytes, offset).expect("context");
    assert_eq!(context.prefix, r"App\Ent");
    assert_eq!(&source[context.range], r"App\\Entity\\User");
    let references = types_tag_class_references(bytes);
    assert_eq!(references.len(), 2);
    assert_eq!(references[0].name, r"App\Entity\User");
    assert_eq!(references[1].name, r"App\Entity\Product");
    for reference in &references {
        assert_eq!(reference.raw, &source[reference.range]);
    }
    let selected = types_tag_class_reference_at(bytes, source.find("Product").unwrap() as u32 + 2)
        .expect("selected");
    assert_eq!(selected.name, r"App\Entity\Product");
}

#[test]
fn types_tag_queries_ignore_comments_and_raw_bodies() {
    let source = r"{# {% types { fake: 'App\\Fake' } %} #}
{% raw %}{% types { raw: 'App\\Raw' } %}{% endraw %}
{% types { real: 'App\\Real' } %}";
    let declarations = types_tag_declarations(source.as_bytes());
    assert_eq!(declarations.len(), 1);
    assert_eq!(declarations[0].name, "real");
}

#[test]
fn enum_references_find_enum_and_enum_cases_first_arguments() {
    let source = r#"{{ enum('App\\Status') }}
{{ enum_cases("App\\Mode") }}
{{ enum(variable) }}
{{ other('App\\Ignored') }}"#;
    let root = swlsp_parser_twig::parse(source).root();
    let references = enum_references(&root);
    assert_eq!(references.len(), 2);
    assert_eq!(references[0].name, r"App\Status");
    assert_eq!(references[1].name, r"App\Mode");
    assert_eq!(&source[references[0].range], r"App\\Status");
    let literal = twigquery::nodes(&root, &[TWIG_LITERAL_STRING])[0].clone();
    assert_eq!(enum_reference_at(&literal), Some(references[0].clone()));
}

#[test]
fn escape_twig_class_name_cases() {
    assert_eq!(escape_twig_class_name(r"\App\Status"), r"App\\Status");
}
