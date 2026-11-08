use std::sync::Arc;

use swlsp_lsp::{Context, GotoDefinitionProvider, TextDocument};
use swlsp_lsp_protocol as protocol;
use swlsp_twigcomponent::Index;
use swlsp_uriutil::file_uri;

use super::*;
use crate::twig_common::test_support::*;

fn live_event_definitions_at(
    provider: &LiveComponentEventDefinitionProvider,
    path: &str,
    source: &str,
    needle: &str,
) -> Vec<protocol::Location> {
    let offset = offset_of(source, needle) + 1;
    provider.get_definition(
        &Context::background(),
        &request_at(TextDocument::new(file_uri(path), source, 1), offset),
    )
}

#[test]
fn live_component_event_definition_navigates_listener_and_argument() {
    let root = tempfile::tempdir().unwrap();
    let root_path = p(root.path());
    let cache = root.path().join(".cache");
    let php = php_index(&cache);
    let components = Arc::new(Index::new(&cache, None).unwrap());
    components.set_dependencies(Some(php.clone()), None, None);

    let class_path = format!("{root_path}/src/Twig/Components/Cart.php");
    let class = "<?php
namespace App\\Twig\\Components;
use Symfony\\UX\\LiveComponent\\Attribute\\AsLiveComponent;
use Symfony\\UX\\LiveComponent\\Attribute\\LiveArg;
use Symfony\\UX\\LiveComponent\\Attribute\\LiveListener;
#[AsLiveComponent]
final class Cart {
    #[LiveListener('productAdded')]
    public function refresh(
        #[LiveArg('productId')] int $id,
    ): void {}
}";
    write(&class_path, class);
    index_php(&php, &class_path, class);
    index(&*components, &class_path, class);
    let provider = LiveComponentEventDefinitionProvider::new(Some(components));

    let template_path = format!("{root_path}/templates/Cart.html.twig");
    let template = r#"<button data-action="click->live#emit" data-live-event-param="productAdded" data-live-product-id-param="42"></button>"#;
    let events = live_event_definitions_at(&provider, &template_path, template, "productAdded");
    assert_eq!(events.len(), 1);
    assert_eq!(events[0].uri, file_uri(&class_path));
    assert_eq!(events[0].range.start.line, 7);

    let arguments = live_event_definitions_at(&provider, &template_path, template, "product-id");
    assert_eq!(arguments.len(), 1);
    assert_eq!(arguments[0].uri, file_uri(&class_path));
    assert_eq!(arguments[0].range.start.line, 9);

    let emitter_path = format!("{root_path}/src/Emitter.php");
    let emitter = "<?php class Emitter { public function save(): void {
    $this->emit('productAdded', ['productId' => 42]);
} }";
    let php_locations =
        live_event_definitions_at(&provider, &emitter_path, emitter, "productAdded");
    assert_eq!(php_locations.len(), 1);
    assert_eq!(php_locations[0].uri, file_uri(&class_path));
}
