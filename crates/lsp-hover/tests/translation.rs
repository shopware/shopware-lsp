mod common;

use std::sync::Arc;

use common::*;
use swlsp_lsp::TextDocument;
use swlsp_lsp_hover::TranslationHoverProvider;
use swlsp_translation::Index;

#[test]
fn translation_hover_shows_locales_and_locations() {
    let cache = tempfile::tempdir().unwrap();
    let index = Arc::new(Index::new(cache.path(), None).unwrap());
    index_file(
        index.as_ref(),
        "/project/translations/messages.en.yaml",
        "hello.world: Hello world\n",
    );
    index_file(
        index.as_ref(),
        "/project/translations/messages.de.yaml",
        "hello.world: Hallo Welt\n",
    );
    let provider = TranslationHoverProvider::new("/project", Some(index), None);
    let source = "{{ 'hello.world'|trans }}";
    let document = TextDocument::new("file:///project/template.twig", source, 1);
    let request = request_at(document, source.find("hello.world").unwrap() + 2);
    let result = hover(&provider, &request).expect("hover");
    let value = &result.contents.value;
    assert!(value.contains("**Symfony translation** `hello.world` · `messages`"));
    assert!(value.contains("**en**: Hello world"));
    assert!(value.contains("**de**: Hallo Welt"));
    assert!(value.contains("`translations/messages.en.yaml:1`"));
    assert!(result.range.is_some());
}
