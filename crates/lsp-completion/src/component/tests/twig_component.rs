use std::path::Path;
use std::sync::Arc;

use swlsp_indexer::{Indexer, ParsedFile};
use swlsp_lsp::{CompletionProvider, Context, TextDocument};
use swlsp_lsp_protocol as protocol;
use swlsp_php::PhpIndex;
use swlsp_twig::TwigIndexer;
use swlsp_twigcomponent::Index;

use super::{edit, node_at, p, require_completion};
use crate::TwigComponentCompletionProvider;

fn completions_at_path(
    provider: &TwigComponentCompletionProvider,
    path: &str,
    source: &str,
) -> Vec<protocol::CompletionItem> {
    let document = Arc::new(TextDocument::new(format!("file://{path}"), source, 1));
    let offset = source.len() as u32;
    let last = &source[source.len() - 1..];
    let node = node_at(&document, source.rfind(last).unwrap() as u32);
    let request = crate::test_support::completion_request(document, node, offset);
    provider.get_completions(&Context::background(), &request)
}

fn completions(
    provider: &TwigComponentCompletionProvider,
    source: &str,
) -> Vec<protocol::CompletionItem> {
    completions_at_path(provider, "/project/templates/page.html.twig", source)
}

fn index_file(indexer: &dyn Indexer, path: &Path, source: &[u8]) {
    indexer
        .index(&ParsedFile::new(p(path), source.to_vec()))
        .unwrap();
}

#[test]
fn twig_component_completion_names_and_props() {
    let root = tempfile::tempdir().unwrap();
    let cache = root.path().join(".cache");
    let twig = Arc::new(TwigIndexer::new(&cache, None).unwrap());
    let components = Arc::new(Index::new(&cache, None).unwrap());
    components.set_dependencies(None, None, Some(twig.clone()));
    let template_path = root.path().join("templates/components/Alert.html.twig");
    let template = b"{# @prop variant string Visual style #}
{% props message, variant = 'info' %}
{% block content %}{% endblock %}";
    index_file(twig.as_ref(), &template_path, template);
    index_file(components.as_ref(), &template_path, template);
    let provider = TwigComponentCompletionProvider::new(Some(components));

    for source in ["{{ component('Al", "<twig:Al", "{% component 'Al"] {
        require_completion(&completions(&provider, source), "Alert");
    }
    let items = completions(&provider, r#"<twig:Alert message="Hello" va"#);
    require_completion(&items, "variant");
    assert!(items.iter().all(|item| item.label != "message"));
    let items = completions(&provider, "<twig:Alert :va");
    require_completion(&items, ":variant");
    let items = completions(&provider, r#"<twig:Alert><twig:block name="co"#);
    require_completion(&items, "content");
}

#[test]
fn twig_live_component_completion_shows_mapped_writable_props() {
    let root = tempfile::tempdir().unwrap();
    let cache = root.path().join(".cache");
    let php = PhpIndex::open(&cache, None).unwrap();
    let twig = Arc::new(TwigIndexer::new(&cache, None).unwrap());
    let components = Arc::new(Index::new(&cache, None).unwrap());
    components.set_dependencies(Some(php.clone()), None, Some(twig));
    let class_path = root.path().join("src/Twig/Components/Search.php");
    let class = br#"<?php
namespace App\Twig\Components;
use Symfony\UX\LiveComponent\Attribute\AsLiveComponent;
use Symfony\UX\LiveComponent\Attribute\LiveProp;
use Symfony\UX\TwigComponent\Attribute\ExposeInTemplate;
#[AsLiveComponent]
final class Search {
    #[ExposeInTemplate(name: 'headline')]
    private string $title = '';

    #[LiveProp(writable: true)]
    public string $query = '';
}"#;
    index_file(&php, &class_path, class);
    index_file(components.as_ref(), &class_path, class);
    let provider = TwigComponentCompletionProvider::new(Some(components));

    let items = completions(&provider, "<twig:Se");
    let search = require_completion(&items, "Search");
    assert!(search.detail.contains("live component"));

    let items = completions(&provider, "<twig:Search ");
    let headline = require_completion(&items, "headline");
    assert_eq!(headline.detail, "string");
    let query = require_completion(&items, "query");
    assert!(query.detail.contains("writable live prop"));
    assert!(items.iter().all(|item| item.label != "title"));
}

#[test]
fn twig_live_action_completion_in_helper_and_data_attribute() {
    let root = tempfile::tempdir().unwrap();
    let cache = root.path().join(".cache");
    let php = PhpIndex::open(&cache, None).unwrap();
    let twig = Arc::new(TwigIndexer::new(&cache, None).unwrap());
    let components = Arc::new(Index::new(&cache, None).unwrap());
    components.set_dependencies(Some(php.clone()), None, Some(twig.clone()));
    let class_path = root.path().join("src/Twig/Components/Cart.php");
    let class = br#"<?php
namespace App\Twig\Components;
use Symfony\UX\LiveComponent\Attribute\AsLiveComponent;
use Symfony\UX\LiveComponent\Attribute\LiveAction;
use Symfony\UX\LiveComponent\Attribute\LiveArg;
#[AsLiveComponent]
final class Cart {
    #[LiveAction]
    public function save(
        #[LiveArg('itemId')] int $id,
        ?string $note = null,
    ): void {}

    #[LiveAction]
    public function removeItem(): void {}
}"#;
    index_file(&php, &class_path, class);
    index_file(components.as_ref(), &class_path, class);
    let template_path = root.path().join("templates/components/Cart.html.twig");
    index_file(twig.as_ref(), &template_path, b"Cart");
    let template_path = p(&template_path);
    let provider = TwigComponentCompletionProvider::new(Some(components));

    for source in [
        r#"<button data-live-action-param="debounce(300)|sa"#,
        "{{ live_action('rem",
    ] {
        let items = completions_at_path(&provider, &template_path, source);
        let save = require_completion(&items, "save");
        assert!(save.detail.contains("int $id"), "{}", save.detail);
        require_completion(&items, "removeItem");
    }

    let items = completions_at_path(
        &provider,
        &template_path,
        r#"<button data-live-action-param="save" data-live-"#,
    );
    let item_id = require_completion(&items, "data-live-item-id-param");
    assert!(item_id.detail.contains("LiveArg(itemId)"));
    assert_eq!(edit(&item_id).new_text, r#"data-live-item-id-param="$0""#);
    require_completion(&items, "data-live-note-param");

    let items = completions_at_path(
        &provider,
        &template_path,
        r#"<button data-live-action-param="save" data-live-note-param="" data-live-"#,
    );
    assert!(
        items
            .iter()
            .all(|item| item.label != "data-live-note-param")
    );

    let items = completions_at_path(&provider, &template_path, "{{ live_action('save', { it");
    let item_id = require_completion(&items, "itemId");
    assert_eq!(item_id.insert_text, "itemId: $0");
    require_completion(&items, "note");
}
