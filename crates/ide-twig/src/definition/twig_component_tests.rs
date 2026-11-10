use std::path::Path;
use std::sync::Arc;

use swlsp_lsp::{Context, GotoDefinitionProvider, TextDocument};
use swlsp_lsp_protocol as protocol;
use swlsp_php::PhpIndex;
use swlsp_twig::TwigIndexer;
use swlsp_twigcomponent::Index;
use swlsp_uriutil::file_uri;

use super::*;
use crate::definition::twig_common::test_support::*;

struct Fixture {
    php: PhpIndex,
    twig: Arc<TwigIndexer>,
    components: Arc<Index>,
}

fn fixture(cache: &Path, with_php: bool) -> Fixture {
    let php = php_index(&cache.join("php"));
    let twig = twig_index(cache);
    let components = Arc::new(Index::new(cache, None).unwrap());
    components.set_dependencies(with_php.then(|| php.clone()), None, Some(twig.clone()));
    Fixture {
        php,
        twig,
        components,
    }
}

fn definitions_at(
    provider: &TwigComponentDefinitionProvider,
    path: &str,
    source: &str,
    offset: u32,
) -> Vec<protocol::Location> {
    provider.get_definition(
        &Context::background(),
        &request_at(TextDocument::new(file_uri(path), source, 1), offset),
    )
}

#[test]
fn twig_component_definition_navigates_to_anonymous_template() {
    let root = tempfile::tempdir().unwrap();
    let root_path = p(root.path());
    let template_path = format!("{root_path}/templates/components/Alert.html.twig");
    write(&template_path, "<div>Alert</div>");
    let fixture = fixture(&root.path().join(".cache"), false);
    index(&*fixture.twig, &template_path, "<div>Alert</div>");

    let source = "<twig:Alert />";
    let locations = definitions_at(
        &TwigComponentDefinitionProvider::new(Some(fixture.components), None),
        &format!("{root_path}/templates/page.html.twig"),
        source,
        offset_of(source, "Alert") + 2,
    );
    assert_eq!(locations.len(), 1);
    assert_eq!(locations[0].uri, file_uri(&template_path));
    assert_eq!(locations[0].range.start, protocol::Position::default());
}

#[test]
fn twig_component_definition_navigates_computed_getter() {
    let root = tempfile::tempdir().unwrap();
    let root_path = p(root.path());
    let fixture = fixture(&root.path().join(".cache"), true);
    let class_path = format!("{root_path}/src/Twig/Components/Card.php");
    let class = "<?php
namespace App\\Twig\\Components;
use Symfony\\UX\\TwigComponent\\Attribute\\AsTwigComponent;
#[AsTwigComponent]
class Card {
    public function getTotal(): int {}
}";
    write(&class_path, class);
    index_php(&fixture.php, &class_path, class);
    index(&*fixture.components, &class_path, class);
    let template_path = format!("{root_path}/templates/components/Card.html.twig");
    let template = "{{ computed.total }}";
    write(&template_path, template);
    index(&*fixture.twig, &template_path, template);

    let locations = definitions_at(
        &TwigComponentDefinitionProvider::new(Some(fixture.components), Some(fixture.php)),
        &template_path,
        template,
        offset_of(template, "total") + 2,
    );
    assert_eq!(locations.len(), 1);
    assert_eq!(locations[0].uri, file_uri(&class_path));
    assert_eq!(locations[0].range.start.line, 5);
}

#[test]
fn twig_component_definition_navigates_live_action() {
    let root = tempfile::tempdir().unwrap();
    let root_path = p(root.path());
    let fixture = fixture(&root.path().join(".cache"), true);
    let class_path = format!("{root_path}/src/Twig/Components/Cart.php");
    let class = "<?php
namespace App\\Twig\\Components;
use Symfony\\UX\\LiveComponent\\Attribute\\AsLiveComponent;
use Symfony\\UX\\LiveComponent\\Attribute\\LiveAction;
use Symfony\\UX\\LiveComponent\\Attribute\\LiveArg;
#[AsLiveComponent]
final class Cart {
    #[LiveAction]
    public function save(#[LiveArg('itemId')] int $id): void {}
}";
    write(&class_path, class);
    index_php(&fixture.php, &class_path, class);
    index(&*fixture.components, &class_path, class);
    let template_path = format!("{root_path}/templates/components/Cart.html.twig");
    let template = r#"<button data-live-action-param="debounce(300)|save" data-live-item-id-param="42">Save</button>"#;
    write(&template_path, template);
    index(&*fixture.twig, &template_path, template);
    let provider =
        TwigComponentDefinitionProvider::new(Some(fixture.components), Some(fixture.php));

    let locations = definitions_at(
        &provider,
        &template_path,
        template,
        offset_of(template, "save") + 2,
    );
    assert_eq!(locations.len(), 1);
    assert_eq!(locations[0].uri, file_uri(&class_path));
    assert_eq!(locations[0].range.start.line, 8);
    assert_eq!(locations[0].range.start.character, 20);

    let locations = definitions_at(
        &provider,
        &template_path,
        template,
        offset_of(template, "item-id") + 2,
    );
    assert_eq!(locations.len(), 1);
    assert_eq!(locations[0].uri, file_uri(&class_path));
    assert_eq!(locations[0].range.start.line, 8);
    assert!(locations[0].range.start.character > 20);
}
