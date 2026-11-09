use swlsp_indexer::Indexer;

use super::*;
use crate::twig::TemplateAnalyzer;

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
    check_problems(
        &fixture.analyzer,
        "file:///project/templates/page.html.twig",
        "{% extends 'base.html.twig' %}\n{% use '@MyBundle/card.html.twig' %}\n{% include '{|twig.template.missing:missing.html.twig|}' %}\n{{ source('{|twig.template.missing:also-missing.html.twig|}') }}",
    );
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
    let cases = [
        r#"{% sw_include "@SwagCmsExtensions/storefront/card.html.twig" %}"#,
        r#"{% sw_extends "@SwagCmsExtensions/storefront/card.html.twig" %}"#,
        r#"{% sw_extends {template: "@SwagCmsExtensions/storefront/card.html.twig", scopes: ['default']} %}"#,
        r#"{% sw_include "@UnknownBundle/storefront/card.html.twig" %}"#,
        r#"{% sw_include "storefront/card.html.twig" %}"#,
        r#"{% sw_include "@SwagCmsExtensions/" ~ "storefront/card.html.twig" %}"#,
        r#"{% sw_include ["missing.html.twig", "@SwagCmsExtensions/storefront/card.html.twig"] %}"#,
        r#"{% sw_include "{|twig.template.missing:@SwagCmsExtensions/storefront/missing.html.twig|}" %}"#,
        r#"{% sw_include "{|twig.template.missing:@SwagCmsExtensions/app-only.html.twig|}" %}"#,
        r#"{% sw_include "app-only.html.twig" %}"#,
        r#"{% include "{|twig.template.missing:@SwagCmsExtensions/storefront/card.html.twig|}" %}"#,
        r#"{% extends "{|twig.template.missing:@SwagCmsExtensions/storefront/card.html.twig|}" %}"#,
        r#"{{ include("{|twig.template.missing:@SwagCmsExtensions/storefront/card.html.twig|}") }}"#,
        r#"{% sw_include include("{|twig.template.missing:@SwagCmsExtensions/storefront/card.html.twig|}") %}"#,
        r#"{% include "@swagcmsextensions/storefront/card.html.twig" %}"#,
    ];
    for fixture in cases {
        let (document, problems) =
            check_problems(&analyzer, "file:///project/page.html.twig", fixture);
        for problem in &problems {
            // Diagnostics still report the spelling and range in the document.
            assert_eq!(
                range_text(&document, problem.range),
                payload_str(problem, "templateName"),
                "{fixture}"
            );
        }
    }

    index.removed_files(&[path.to_owned()]).unwrap();
    check_problems_v(
        &analyzer,
        "file:///project/page.html.twig",
        r#"{% sw_include "{|twig.template.missing:@SwagCmsExtensions/storefront/card.html.twig|}" %}"#,
        2,
    );
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
    let (_, result) = check_problems(
        &analyzer,
        "file:///project/src/Storefront/Resources/views/storefront/layout/breadcrumb.html.twig",
        "{% set icon = source('@Storefront/assets/icon/solid/arrow-medium-right.svg') %}\n{% include '@Storefront/storefront/fragment.html.twig' %}\n{{ source('{|twig.template.missing:@Storefront/assets/icon/missing.svg|}') }}\n{% include '{|twig.template.missing:ping.twig|}' %}",
    );
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
    let (_, result) = check_problems(
        &fixture.analyzer,
        "file:///project/templates/page.html.twig",
        "{% include '@Storefront/storefront/section/cms-section-' ~ section.type ~ '.html.twig' %}
{% include 'ignored-by-tag.html.twig' ignore missing %}
{{ include('ignored-by-function.html.twig', ignore_missing: true) }}
{{ source('ignored-source.html.twig', true) }}
{% include ['missing-fallback.html.twig', 'base.html.twig'] %}
{% include {|twig.template.missing:['missing-one.html.twig', 'missing-two.html.twig']|} %}
{% include 'base' ~ '.html.twig' %}
{% include {|twig.template.missing:'missing/' ~ 'card.html.twig'|} %}
{% form_theme form with ['base.html.twig', '{|twig.template.missing:missing-theme.html.twig|}'] %}",
    );
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
        result[2].message,
        "Template 'missing-theme.html.twig' not found"
    );
}

#[test]
fn template_diagnostics_for_typed_php_render() {
    let fixture = fixture();
    let (_, result) = check_problems(
        &fixture.analyzer,
        "file:///project/Controller.php",
        "<?php
namespace App;
class Controller extends \\Symfony\\Bundle\\FrameworkBundle\\Controller\\AbstractController {
    public function page(): void {
        $this->render('base.html.twig');
        $this->render('{|twig.template.missing:missing.html.twig|}');
    }
}",
    );
    assert_eq!(result[0].message, "Template 'missing.html.twig' not found");
}

#[test]
fn template_diagnostics_include_typo_suggestions() {
    let fixture = fixture();
    let (_, result) = check_problems(
        &fixture.analyzer,
        "file:///project/templates/page.html.twig",
        "{% extends '{|twig.template.missing:bsae.html.twig|}' %}",
    );
    assert!(suggestions(&result[0]).contains(&"base.html.twig".to_owned()));
}

#[test]
fn template_diagnostics_validate_php_doc_assistant_references() {
    let fixture = fixture();
    let (_, result) = check_problems(
        &fixture.analyzer,
        "file:///project/src/Usage.php",
        "<?php\nresolve_template('{|twig.template.missing:bsae.html.twig|}');\nresolve_template('base.html.twig');\n",
    );
    assert!(suggestions(&result[0]).contains(&"base.html.twig".to_owned()));
}

#[test]
fn template_diagnostics_for_attribute_and_annotation_declarations() {
    let fixture = fixture();
    check_problems(
        &fixture.analyzer,
        "file:///project/src/ArticleController.php",
        "<?php
namespace App\\Controller;
use Symfony\\Bridge\\Twig\\Attribute\\Template;
class ArticleController {
    #[Template('{|twig.template.missing:attribute-missing.html.twig|}')]
    public function attribute(): array { return []; }

    /** @Template(template=\"annotation-missing.html.twig\") */
    public function annotation(): array { return []; }
}",
    );
}

#[test]
fn template_diagnostics_ignore_generic_template_tags() {
    let fixture = fixture();
    check_problems(
        &fixture.analyzer,
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
}

#[test]
fn template_diagnostics_guess_empty_controller_declarations() {
    let fixture = fixture();
    let (_, result) = check_problems(
        &fixture.analyzer,
        "file:///project/src/Admin/ProductController.php",
        "<?php
namespace App\\Controller\\Admin;
use Symfony\\Bridge\\Twig\\Attribute\\Template;
class ProductController {
    #[{|twig.template.missing:Template|}]
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
    assert_eq!(
        payload_str(&result[0], "templateName"),
        "admin/product/list.html.twig"
    );
}
