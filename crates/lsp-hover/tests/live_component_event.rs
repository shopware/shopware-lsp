mod common;

use std::sync::Arc;

use common::*;
use swlsp_lsp::TextDocument;
use swlsp_lsp_hover::LiveComponentEventHoverProvider;
use swlsp_lsp_protocol as protocol;
use swlsp_twigcomponent::Index;

fn live_event_hover_at(
    provider: &LiveComponentEventHoverProvider,
    path: &std::path::Path,
    source: &str,
    needle: &str,
) -> protocol::Hover {
    let document = TextDocument::new(uri(path), source, 1);
    let request = request_at(document, source.find(needle).unwrap() + 1);
    hover(provider, &request).expect("hover")
}

#[test]
fn live_component_event_hover_shows_listener_and_aliased_payload() {
    let root = tempfile::tempdir().unwrap();
    let cache = root.path().join(".cache");
    let php = php_index(&cache.join("php"), &[]);
    let components = Arc::new(Index::new(cache.join("components"), None).unwrap());
    components.set_dependencies(Some(php.clone()), None, None);
    let class_path = p(&root.path().join("src/Twig/Components/Cart.php"));
    let class = r"<?php
namespace App\Twig\Components;
use Symfony\UX\LiveComponent\Attribute\AsLiveComponent;
use Symfony\UX\LiveComponent\Attribute\LiveArg;
use Symfony\UX\LiveComponent\Attribute\LiveListener;
#[AsLiveComponent]
final class Cart {
    #[LiveListener('productAdded')]
    public function refresh(
        #[LiveArg('productId')] int $id,
    ): void {}
}";
    index_file(&php, &class_path, class);
    index_file(components.as_ref(), &class_path, class);
    let provider = LiveComponentEventHoverProvider::new(Some(components));
    let path = root.path().join("templates/Cart.html.twig");
    let source = r#"<button data-action="live#emit" data-live-event-param="productAdded" data-live-product-id-param="42"></button>"#;

    let event = live_event_hover_at(&provider, &path, source, "productAdded");
    assert!(event.contents.value.contains("Symfony UX Live event"));
    assert!(event.contents.value.contains("Cart::refresh"));
    assert!(event.contents.value.contains("int $id"));

    let argument = live_event_hover_at(&provider, &path, source, "product-id");
    assert!(argument.contents.value.contains("Live event argument"));
    assert!(argument.contents.value.contains("PHP type: `int`"));
    assert!(argument.contents.value.contains("$id"));
    assert!(argument.contents.value.contains("#[LiveArg]"));
}
