use std::sync::Arc;

use swlsp_ide::codelens::testing::*;
use swlsp_indexer::Indexer;
use swlsp_twig::TwigIndexer;

use super::*;

// -- twig --

#[test]
fn twig_code_lens_links_references_inheritance_and_blocks() {
    let root = tempfile::tempdir().unwrap();
    let root = root.path();
    let base_path = root.join("templates/base.html.twig");
    let child_path = root.join("templates/child.html.twig");
    let grand_path = root.join("templates/grand.html.twig");
    let page_path = root.join("templates/page.html.twig");
    let base_source = "{% block title %}Base{% endblock %}\n{% block body %}Body{% endblock %}\n";
    let child_source = "{% extends 'base.html.twig' %}\n{% block title %}Child{% endblock %}\n";
    let grand_source = "{% extends 'child.html.twig' %}\n{% block title %}Grand{% endblock %}\n{% block body %}Grand body{% endblock %}\n";
    let page_source = "{% include 'base.html.twig' %}\n";
    let sources = [
        (&base_path, base_source),
        (&child_path, child_source),
        (&grand_path, grand_source),
        (&page_path, page_source),
    ];
    for (path, source) in sources {
        write(path, source);
    }
    let twig = Arc::new(TwigIndexer::new(root.join(".cache"), None).unwrap());
    for (path, source) in sources {
        twig.index(&parsed(path, source)).unwrap();
    }
    let provider = TwigCodeLensProvider::new(Some(twig));

    let base = lenses_for(&provider, &base_path, base_source);
    assert_eq!(
        targets(by_title(&base, "Open extending template")),
        vec![target(&child_path, 1)]
    );
    assert_eq!(
        targets(by_title(&base, "Open template reference")),
        vec![target(&page_path, 1)]
    );
    let title_implementations = by_title(&base, "Open 2 block implementations");
    assert_eq!(title_implementations.range.start.line, 0);
    assert_eq!(
        targets(title_implementations),
        vec![target(&child_path, 2), target(&grand_path, 2)]
    );
    let body_implementation = by_title(&base, "Open block implementation");
    assert_eq!(body_implementation.range.start.line, 1);
    assert_eq!(targets(body_implementation), vec![target(&grand_path, 3)]);

    let child = lenses_for(&provider, &child_path, child_source);
    assert_eq!(
        targets(by_title(&child, "Open extending template")),
        vec![target(&grand_path, 1)]
    );
    assert_eq!(
        targets(by_title(&child, "Open parent block")),
        vec![target(&base_path, 1)]
    );
    assert_eq!(
        targets(by_title(&child, "Open block implementation")),
        vec![target(&grand_path, 2)]
    );

    let grand = lenses_for(&provider, &grand_path, grand_source);
    assert_eq!(
        targets(by_title(&grand, "Open 2 parent blocks")),
        vec![target(&base_path, 1), target(&child_path, 2)]
    );
    assert_eq!(
        targets(by_title(&grand, "Open parent block")),
        vec![target(&base_path, 2)]
    );

    let unsaved = lenses_for(
        &provider,
        &grand_path,
        "{% extends 'base.html.twig' %}\n{% block title %}Grand{% endblock %}\n",
    );
    assert_eq!(
        targets(by_title(&unsaved, "Open parent block")),
        vec![target(&base_path, 1)]
    );
}

#[test]
fn twig_code_lens_links_same_name_template_overrides() {
    let root = tempfile::tempdir().unwrap();
    let root = root.path();
    let first = root.join("app/templates/card.html.twig");
    let second = root.join("plugin/templates/card.html.twig");
    let twig = Arc::new(TwigIndexer::new(root.join(".cache"), None).unwrap());
    for path in [&first, &second] {
        write(path, "card");
        twig.index(&parsed(path, "card")).unwrap();
    }
    let lenses = lenses_for(&TwigCodeLensProvider::new(Some(twig)), &first, "card");
    assert_eq!(titles(&lenses), vec!["Open template override"]);
    assert_eq!(targets(&lenses[0]), vec![target(&second, 1)]);
}

// -- twig components --

#[test]
fn twig_component_related_code_lens_links_template_class_usages_and_blocks() {
    let root = tempfile::tempdir().unwrap();
    let root = root.path();
    let cache = root.join(".cache");
    let class_path = root.join("src/Twig/Components/Alert.php");
    let template_path = root.join("templates/components/alert.html.twig");
    let usage_path = root.join("templates/page.html.twig");
    let class_source = r"<?php
namespace App\Twig\Components;

use Symfony\UX\TwigComponent\Attribute\AsTwigComponent;

#[AsTwigComponent(name: 'Alert', template: 'components/alert.html.twig')]
final class Alert {}
";
    let template_source = "{% block headline %}Default headline{% endblock %}";
    let usage_source = "{{ component('Alert') }}\n<twig:Alert>\n    <twig:block name=\"headline\">Custom headline</twig:block>\n</twig:Alert>\n";
    let files = [
        (&class_path, class_source),
        (&template_path, template_source),
        (&usage_path, usage_source),
    ];
    for (path, source) in files {
        write(path, source);
    }
    let php = php_index(&cache);
    let twig = Arc::new(TwigIndexer::new(&cache, None).unwrap());
    let components = Arc::new(swlsp_twigcomponent::Index::new(&cache, None).unwrap());
    components.set_dependencies(Some(php.clone()), None, Some(twig.clone()));
    for (path, source) in files {
        let file = parsed(path, source);
        twig.index(&file).unwrap();
        components.index(&file).unwrap();
        if path.extension().is_some_and(|ext| ext == "php") {
            php.index(&file).unwrap();
        }
    }
    let provider = TwigComponentRelatedCodeLensProvider::new(Some(components), Some(php));
    let template = lenses_for(&provider, &template_path, template_source);
    assert_eq!(titles(&template), vec!["Open UX component"]);
    assert_eq!(
        targets(&template[0]),
        vec![
            target(&class_path, 7),
            target(&usage_path, 1),
            target(&usage_path, 2),
        ]
    );
    let usage = lenses_for(&provider, &usage_path, usage_source);
    assert_eq!(titles(&usage), vec!["Open component block"]);
    assert_eq!(usage[0].range.start.line, 2);
    assert_eq!(targets(&usage[0]), vec![target(&template_path, 1)]);
}

#[test]
fn twig_component_related_code_lens_skips_unrelated_twig() {
    let root = tempfile::tempdir().unwrap();
    let components =
        Arc::new(swlsp_twigcomponent::Index::new(root.path().join(".cache"), None).unwrap());
    let lenses = lenses_for(
        &TwigComponentRelatedCodeLensProvider::new(Some(components), None),
        &root.path().join("templates/page.html.twig"),
        "<h1>Page</h1>",
    );
    assert!(lenses.is_empty());
}
