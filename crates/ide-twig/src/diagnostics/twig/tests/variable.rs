use std::path::Path;
use std::sync::Arc;

use swlsp_php::PhpIndex;
use swlsp_twig::TwigIndexer;
use swlsp_uriutil::file_uri;

use super::*;
use crate::diagnostics::twig::TwigUndefinedVariableAnalyzer;

const CONTROLLER: &str = r"<?php
namespace App;

class Product { public string $name; }

class PageController
{
    public function page(Product $product): Response
    {
        return $this->render('page.html.twig', ['product' => $product, 'title' => 'Page']);
    }
}
";

/// A project whose controller renders `page.html.twig`, with `templates`
/// written and indexed.
fn project(root: &Path, templates: &[(&str, &str)]) -> (PhpIndex, Arc<TwigIndexer>) {
    let php = php_index(&root.join("cache"));
    let twig = twig_index(&root.join("cache"));
    twig.set_dependencies(Some(php.clone()), None);
    index_file(&php, "/project/src/PageController.php", CONTROLLER);
    for (name, source) in templates {
        let path = root.join("templates").join(name);
        std::fs::create_dir_all(path.parent().unwrap()).unwrap();
        std::fs::write(&path, plain(source)).unwrap();
        index_file(twig.as_ref(), path.to_str().unwrap(), &plain(source));
    }
    (php, twig)
}

fn check(templates: &[(&str, &str)]) {
    let directory = tempdir();
    let (php, twig) = project(directory.path(), templates);
    let analyzer = TwigUndefinedVariableAnalyzer::new(Some(twig), Some(php));
    for (name, fixture) in templates {
        let path = directory.path().join("templates").join(name);
        check_problems(&analyzer, &file_uri(path.to_str().unwrap()), fixture);
    }
}

#[test]
fn undefined_variables_in_a_rendered_template() {
    check(&[(
        "page.html.twig",
        "{{ product.name }}{{ title }}{{ {|twig.variable.undefined:missing|} }}
{% set local = 1 %}{{ local }}
{% for item in items ?? [] %}{{ item }}{{ loop.index }}{% endfor %}
{{ [1]|map(value => value) }}
{% if optional is defined %}{{ optional }}{% endif %}
{{ fallback|default('') }}
{{ product is empty }}
{% with {scoped: 1} %}{{ scoped }}{% endwith %}
{# @var declared string #}{{ declared }}
{{ _self }}{{ _context }}
{% macro show(item) %}{{ item }}{{ {|twig.variable.undefined:product|} }}{% endmacro %}
",
    )]);
}

#[test]
fn templates_around_a_template_provide_variables() {
    check(&[
        (
            "page.html.twig",
            "{% extends 'base.html.twig' %}
{% set heading = title %}
{% block body %}{{ shared }}{% include 'card.html.twig' with {item: product} %}{% endblock %}
",
        ),
        (
            "base.html.twig",
            "{% set shared = 1 %}{{ heading }}{{ product }}{{ {|twig.variable.undefined:absent|} }}",
        ),
        (
            "card.html.twig",
            "{{ item }}{{ title }}{{ {|twig.variable.undefined:other|} }}",
        ),
    ]);
}

#[test]
fn nothing_is_reported_without_a_known_context() {
    check(&[
        ("orphan.html.twig", "{{ anything }}"),
        (
            "open.html.twig",
            "{% include 'target.html.twig' with data %}",
        ),
        ("target.html.twig", "{{ anything }}"),
    ]);
}

#[test]
fn dump_type_shows_the_type_where_it_stands() {
    use crate::diagnostics::twig::TwigDumpTypeAnalyzer;
    let directory = tempdir();
    let (php, twig) = project(directory.path(), &[("page.html.twig", "")]);
    let path = directory.path().join("templates/page.html.twig");
    let (_, problems) = check_problems(
        &TwigDumpTypeAnalyzer::new(Some(twig), Some(php)),
        &file_uri(path.to_str().unwrap()),
        "{% {|twig.type.dumped:dump_type|} product %}
{% {|twig.type.dumped:dump_type|} product.name %}
{% set label = product.name %}
{% for item in [product] %}{% {|twig.type.dumped:dump_type|} item %}{% endfor %}
{% {|twig.type.dumped:dump_type|} missing %}
{% {|twig.type.dumped:dump_type|} %}
{% {|twig.type.dumped:dump_type|} product|first %}
",
    );
    let messages: Vec<&str> = problems
        .iter()
        .map(|problem| problem.message.as_str())
        .collect();
    assert_eq!(
        messages,
        [
            r"Dumped type: App\Product",
            "Dumped type: string",
            r"Dumped type: App\Product",
            "Dumped type: mixed (nothing is known about it)",
            r#"Dumped context: label: string, product: App\Product, title: "Page""#,
            "dump_type takes a variable or an attribute path such as product.name",
        ]
    );
}

#[test]
fn rendering_an_abstract_template_is_reported() {
    use crate::diagnostics::twig::TemplateAnalyzer;
    let directory = tempdir();
    let (php, twig) = project(
        directory.path(),
        &[
            (
                "page.html.twig",
                "{# @twigstan-abstract #}{% block body %}{% endblock %}",
            ),
            ("child.html.twig", "{% extends 'page.html.twig' %}"),
        ],
    );
    index_file(
        &php,
        "/project/vendor/symfony/AbstractController.php",
        "<?php\nnamespace Symfony\\Bundle\\FrameworkBundle\\Controller;\nabstract class AbstractController { protected function render(string $view, array $parameters = []) {} }\n",
    );
    let controller = CONTROLLER.replace(
        "class PageController",
        "class PageController extends \\Symfony\\Bundle\\FrameworkBundle\\Controller\\AbstractController",
    );
    let analyzer = TemplateAnalyzer::new(Some(twig), Some(php));
    check_problems(
        &analyzer,
        "file:///project/src/PageController.php",
        &controller
            .replace(
                "'page.html.twig'",
                "'{|twig.template.abstract:page.html.twig|}'",
            )
            .replace(
                "return $this->render(",
                "$this->render('child.html.twig', []);\n        return $this->render(",
            ),
    );
    // Extending it is what it is for.
    let child = directory.path().join("templates/child.html.twig");
    check_problems(
        &analyzer,
        &file_uri(child.to_str().unwrap()),
        "{% extends 'page.html.twig' %}",
    );
}

#[test]
fn unknown_providers_keep_a_template_unchecked() {
    check(&[
        (
            "page.html.twig",
            "{% include 'element/cms-element-' ~ product.name ~ '.html.twig' %}
{% include 'element/cms-element-text.html.twig' %}
{% apply upper %}{{ product.name }}{% endapply %}
<ul>{% for item in [product] %}{% if title %}<li>{% else %}<li class=\"a\">{% endif %}{{ item.name }}</li>{% endfor %}</ul>
",
        ),
        // Any template the runtime-built name may be gets its variables from
        // an include the analysis cannot see.
        ("element/cms-element-text.html.twig", "{{ element }}"),
    ]);
}

#[test]
fn a_render_call_with_an_open_context_keeps_the_template_unchecked() {
    let directory = tempdir();
    let (php, twig) = project(
        directory.path(),
        &[("spread.html.twig", ""), ("parameter.html.twig", "")],
    );
    index_file(
        &php,
        "/project/src/OpenController.php",
        r"<?php
namespace App;

class OpenController
{
    public function spread($data)
    {
        return $this->render('spread.html.twig', [...$data->getVars(), 'title' => 'a']);
    }

    public function parameter(array $parameters)
    {
        return $this->render('parameter.html.twig', $parameters);
    }
}
",
    );
    let analyzer = TwigUndefinedVariableAnalyzer::new(Some(twig), Some(php));
    for name in ["spread.html.twig", "parameter.html.twig"] {
        let path = directory.path().join("templates").join(name);
        check_problems(
            &analyzer,
            &file_uri(path.to_str().unwrap()),
            "{{ title }}{{ anything }}",
        );
    }
}
