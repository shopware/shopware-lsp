use std::sync::Arc;

use swlsp_indexer::{Indexer, ParsedFile};
use swlsp_lsp::{CompletionProvider, Context, TextDocument};
use swlsp_lsp_protocol as protocol;
use swlsp_php::PhpIndex;
use swlsp_twigcomponent::Index;

use super::{edit, node_at, p, require_completion};
use crate::LiveComponentEventCompletionProvider;

fn completions(
    provider: &LiveComponentEventCompletionProvider,
    path: &str,
    source: &str,
) -> Vec<protocol::CompletionItem> {
    let document = Arc::new(TextDocument::new(swlsp_uriutil::file_uri(path), source, 1));
    let offset = source.len() as u32;
    let node = node_at(&document, offset.saturating_sub(1));
    let request = crate::test_support::completion_request(document, node, offset);
    provider.get_completions(&Context::background(), &request)
}

#[test]
fn live_component_event_completion_in_php_and_twig() {
    let root = tempfile::tempdir().unwrap();
    let cache = root.path().join(".cache");
    let php = PhpIndex::open(&cache, None).unwrap();
    let components = Arc::new(Index::new(&cache, None).unwrap());
    components.set_dependencies(Some(php.clone()), None, None);
    let class_path = root.path().join("src/Twig/Components/Cart.php");
    let class = br#"<?php
namespace App\Twig\Components;
use Symfony\UX\LiveComponent\Attribute\AsLiveComponent;
use Symfony\UX\LiveComponent\Attribute\LiveArg;
use Symfony\UX\LiveComponent\Attribute\LiveListener;
#[AsLiveComponent]
final class Cart {
    #[LiveListener('productAdded')]
    public function refresh(
        #[LiveArg('productId')] int $id,
        #[LiveArg] string $source,
    ): void {}
}"#;
    php.index(&ParsedFile::new(p(&class_path), class.to_vec()))
        .unwrap();
    components
        .index(&ParsedFile::new(p(&class_path), class.to_vec()))
        .unwrap();
    let provider = LiveComponentEventCompletionProvider::new(Some(components));
    let emitter = p(&root.path().join("src/Emitter.php"));

    let items = completions(
        &provider,
        &emitter,
        "<?php class Emitter { function save() { $this->emit('prod",
    );
    let event = require_completion(&items, "productAdded");
    assert!(event.detail.contains("Cart::refresh"), "{}", event.detail);

    let items = completions(
        &provider,
        &emitter,
        "<?php class Emitter { function save() { $this->emit('productAdded', ['pro",
    );
    let product = require_completion(&items, "productId");
    assert_eq!(product.insert_text, "'productId' => $0");
    assert!(product.detail.contains("LiveArg(productId)"));
    require_completion(&items, "source");

    let template = p(&root.path().join("templates/Cart.html.twig"));
    let items = completions(
        &provider,
        &template,
        r#"<button data-action="live#emit" data-live-event-param="prod"#,
    );
    require_completion(&items, "productAdded");

    let items = completions(
        &provider,
        &template,
        r#"<button data-action="live#emit" data-live-event-param="productAdded" data-live-"#,
    );
    let attribute = require_completion(&items, "data-live-product-id-param");
    assert_eq!(
        edit(&attribute).new_text,
        r#"data-live-product-id-param="$0""#
    );
    require_completion(&items, "data-live-source-param");
}
