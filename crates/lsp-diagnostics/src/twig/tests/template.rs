use swlsp_indexer::Indexer;

use super::*;
use crate::twig::TemplateAnalyzer;

const MISSING: &str = "twig.template.missing";

struct Fixture {
    analyzer: TemplateAnalyzer,
    _dirs: Vec<tempfile::TempDir>,
}

fn fixture() -> Fixture {
    let twig_dir = tempdir();
    let twig = twig_index(twig_dir.path());
    index_file(twig.as_ref(), "/project/templates/base.html.twig", "base");
    index_file(
        twig.as_ref(),
        "/project/MyBundle/src/Resources/views/card.html.twig",
        "card",
    );
    index_file(
        twig.as_ref(),
        "/project/templates/admin/product/existing.html.twig",
        "existing",
    );
    let php_dir = tempdir();
    let php = php_index(php_dir.path());
    index_file(
        &php,
        "/project/src/TemplateAssistant.php",
        "<?php\n/** @param string $template #Template */\nfunction resolve_template(string $template): void {}\n",
    );
    Fixture {
        analyzer: TemplateAnalyzer::new(Some(twig), Some(php)),
        _dirs: vec![twig_dir, php_dir],
    }
}

#[test]
fn template_diagnostics_for_twig() {
    let fixture = fixture();
    let document = doc(
        "file:///project/templates/page.html.twig",
        "{% extends 'base.html.twig' %}\n{% use '@MyBundle/card.html.twig' %}\n{% include 'missing.html.twig' %}\n{{ source('also-missing.html.twig') }}",
    );
    let result = analyze(&fixture.analyzer, &document);
    assert_eq!(result.len(), 2);
    for problem in &result {
        assert_eq!(problem.id.as_str(), MISSING);
    }
}

#[test]
fn template_diagnostics_for_shopware_bundle_namespaces() {
    let dir = tempdir();
    let index = twig_index(dir.path());
    let path = "/project/vendor/store.shopware.com/swagcmsextensions/src/Resources/views/storefront/card.html.twig";
    index_file(index.as_ref(), path, "card");
    // An application template outside the bundle hierarchy must not satisfy
    // a Shopware include just because its relative name matches.
    index_file(
        index.as_ref(),
        "/project/templates/app-only.html.twig",
        "app",
    );
    let analyzer = TemplateAnalyzer::new(Some(index.clone()), None);
    let cases: &[(&str, bool)] = &[
        (
            r#"{% sw_include "@SwagCmsExtensions/storefront/card.html.twig" %}"#,
            false,
        ),
        (
            r#"{% sw_extends "@SwagCmsExtensions/storefront/card.html.twig" %}"#,
            false,
        ),
        (
            r#"{% sw_extends {template: "@SwagCmsExtensions/storefront/card.html.twig", scopes: ['default']} %}"#,
            false,
        ),
        (
            r#"{% sw_include "@UnknownBundle/storefront/card.html.twig" %}"#,
            false,
        ),
        (r#"{% sw_include "storefront/card.html.twig" %}"#, false),
        (
            r#"{% sw_include "@SwagCmsExtensions/" ~ "storefront/card.html.twig" %}"#,
            false,
        ),
        (
            r#"{% sw_include ["missing.html.twig", "@SwagCmsExtensions/storefront/card.html.twig"] %}"#,
            false,
        ),
        (
            r#"{% sw_include "@SwagCmsExtensions/storefront/missing.html.twig" %}"#,
            true,
        ),
        (
            r#"{% sw_include "@SwagCmsExtensions/app-only.html.twig" %}"#,
            true,
        ),
        (r#"{% sw_include "app-only.html.twig" %}"#, false),
        (
            r#"{% include "@SwagCmsExtensions/storefront/card.html.twig" %}"#,
            true,
        ),
        (
            r#"{% extends "@SwagCmsExtensions/storefront/card.html.twig" %}"#,
            true,
        ),
        (
            r#"{{ include("@SwagCmsExtensions/storefront/card.html.twig") }}"#,
            true,
        ),
        (
            r#"{% sw_include include("@SwagCmsExtensions/storefront/card.html.twig") %}"#,
            true,
        ),
        (
            r#"{% include "@swagcmsextensions/storefront/card.html.twig" %}"#,
            false,
        ),
    ];
    for (source, missing) in cases {
        let document = doc("file:///project/page.html.twig", source);
        let problems = analyze(&analyzer, &document);
        if *missing {
            assert_eq!(problems.len(), 1, "{source}");
            assert_eq!(problems[0].id.as_str(), MISSING);
            // Diagnostics still report the spelling and range in the document.
            assert_eq!(
                range_text(&document, problems[0].range),
                payload_str(&problems[0], "templateName"),
                "{source}"
            );
        } else {
            assert!(problems.is_empty(), "{source}: {problems:?}");
        }
    }

    index.removed_files(&[path.to_owned()]).unwrap();
    let problems = analyze(
        &analyzer,
        &doc_v(
            "file:///project/page.html.twig",
            r#"{% sw_include "@SwagCmsExtensions/storefront/card.html.twig" %}"#,
            2,
        ),
    );
    assert_eq!(problems.len(), 1);
}

#[test]
fn template_diagnostics_resolve_storefront_dist_templates() {
    let dir = tempdir();
    let index = twig_index(dir.path());
    let dist = "/project/src/Storefront/Resources/app/storefront/dist";
    index_file(
        index.as_ref(),
        &format!("{dist}/assets/icon/solid/arrow-medium-right.svg"),
        "<svg></svg>",
    );
    index_file(
        index.as_ref(),
        &format!("{dist}/storefront/fragment.html.twig"),
        "fragment",
    );
    // A Twig-syntax file outside every loader root must not become a
    // suggestion carrying its absolute path.
    index_file(
        index.as_ref(),
        "/Users/example/project/custom/apps/MyApp/Resources/scripts/ping/ping.twig",
        "ping",
    );
    let analyzer = TemplateAnalyzer::new(Some(index), None);
    let document = doc(
        "file:///project/src/Storefront/Resources/views/storefront/layout/breadcrumb.html.twig",
        "{% set icon = source('@Storefront/assets/icon/solid/arrow-medium-right.svg') %}\n{% include '@Storefront/storefront/fragment.html.twig' %}\n{{ source('@Storefront/assets/icon/missing.svg') }}\n{% include 'ping.twig' %}",
    );
    let result = analyze(&analyzer, &document);
    assert_eq!(result.len(), 2);
    assert_eq!(
        result[0].message,
        "Template '@Storefront/assets/icon/missing.svg' not found"
    );
    assert_eq!(result[1].message, "Template 'ping.twig' not found");
    for suggestion in suggestions(&result[1]) {
        assert!(!suggestion.contains("/Users/"));
        assert!(!suggestion.contains("Resources/scripts"));
    }
}

#[test]
fn template_diagnostics_for_twig_template_expressions() {
    let fixture = fixture();
    let document = doc(
        "file:///project/templates/page.html.twig",
        "{% include '@Storefront/storefront/section/cms-section-' ~ section.type ~ '.html.twig' %}
{% include 'ignored-by-tag.html.twig' ignore missing %}
{{ include('ignored-by-function.html.twig', ignore_missing: true) }}
{{ source('ignored-source.html.twig', true) }}
{% include ['missing-fallback.html.twig', 'base.html.twig'] %}
{% include ['missing-one.html.twig', 'missing-two.html.twig'] %}
{% include 'base' ~ '.html.twig' %}
{% include 'missing/' ~ 'card.html.twig' %}
{% form_theme form with ['base.html.twig', 'missing-theme.html.twig'] %}",
    );
    let result = analyze(&fixture.analyzer, &document);
    assert_eq!(result.len(), 3);
    assert_eq!(
        result[0].message,
        "None of the fallback templates were found: 'missing-one.html.twig', 'missing-two.html.twig'"
    );
    assert_eq!(
        payload_strings(&result[0], "templateNames"),
        vec!["missing-one.html.twig", "missing-two.html.twig"]
    );
    assert_eq!(
        result[1].message,
        "Template 'missing/card.html.twig' not found"
    );
    assert_eq!(
        range_text(&document, result[1].range),
        "'missing/' ~ 'card.html.twig'"
    );
    assert_eq!(
        result[2].message,
        "Template 'missing-theme.html.twig' not found"
    );
}

#[test]
fn template_diagnostics_for_typed_php_render() {
    let fixture = fixture();
    let document = doc(
        "file:///project/Controller.php",
        "<?php
namespace App;
class Controller extends \\Symfony\\Bundle\\FrameworkBundle\\Controller\\AbstractController {
    public function page(): void {
        $this->render('base.html.twig');
        $this->render('missing.html.twig');
    }
}",
    );
    let result = analyze(&fixture.analyzer, &document);
    assert_eq!(result.len(), 1);
    assert_eq!(result[0].message, "Template 'missing.html.twig' not found");
}

#[test]
fn template_diagnostics_include_typo_suggestions() {
    let fixture = fixture();
    let document = doc(
        "file:///project/templates/page.html.twig",
        "{% extends 'bsae.html.twig' %}",
    );
    let result = analyze(&fixture.analyzer, &document);
    assert_eq!(result.len(), 1);
    assert!(suggestions(&result[0]).contains(&"base.html.twig".to_owned()));
    assert_eq!(range_text(&document, result[0].range), "bsae.html.twig");
}

#[test]
fn template_diagnostics_validate_php_doc_assistant_references() {
    let fixture = fixture();
    let document = doc(
        "file:///project/src/Usage.php",
        "<?php\nresolve_template('bsae.html.twig');\nresolve_template('base.html.twig');\n",
    );
    let result = analyze(&fixture.analyzer, &document);
    assert_eq!(result.len(), 1);
    assert_eq!(result[0].id.as_str(), MISSING);
    assert_eq!(range_text(&document, result[0].range), "bsae.html.twig");
    assert!(suggestions(&result[0]).contains(&"base.html.twig".to_owned()));
}

#[test]
fn template_diagnostics_for_attribute_and_annotation_declarations() {
    let fixture = fixture();
    let document = doc(
        "file:///project/src/ArticleController.php",
        "<?php
namespace App\\Controller;
use Symfony\\Bridge\\Twig\\Attribute\\Template;
class ArticleController {
    #[Template('attribute-missing.html.twig')]
    public function attribute(): array { return []; }

    /** @Template(template=\"annotation-missing.html.twig\") */
    public function annotation(): array { return []; }
}",
    );
    let result = analyze(&fixture.analyzer, &document);
    assert_eq!(result.len(), 1);
    assert_eq!(
        range_text(&document, result[0].range),
        "attribute-missing.html.twig"
    );
}

#[test]
fn template_diagnostics_ignore_generic_template_tags() {
    let fixture = fixture();
    let document = doc(
        "file:///project/src/Collection.php",
        "<?php
/** @template TElement */
class Collection {
    /**
     * @template T
     * @param callable(TElement): T $callback
     * @return array<T>
     */
    public function map(callable $callback): array { return []; }

    /** @template-covariant T */
    public function values(): array { return []; }
}",
    );
    assert!(analyze(&fixture.analyzer, &document).is_empty());
}

#[test]
fn template_diagnostics_guess_empty_controller_declarations() {
    let fixture = fixture();
    let document = doc(
        "file:///project/src/Admin/ProductController.php",
        "<?php
namespace App\\Controller\\Admin;
use Symfony\\Bridge\\Twig\\Attribute\\Template;
class ProductController {
    #[Template]
    public function listAction(): array { return []; }

    /** @Template() */
    public function editAction(): array { return []; }

    /** @Template */
    public function existingAction(): array { return []; }

    #[Template(name: 'not-a-template')]
    public function ignored(): array { return []; }

    public function bodyString(): string { return '@Template'; }
}",
    );
    let result = analyze(&fixture.analyzer, &document);
    assert_eq!(result.len(), 1);
    assert_eq!(range_text(&document, result[0].range), "Template");
    assert_eq!(
        payload_str(&result[0], "templateName"),
        "admin/product/list.html.twig"
    );
}
