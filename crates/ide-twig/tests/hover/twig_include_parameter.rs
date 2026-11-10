use std::sync::Arc;

use crate::common::*;
use swlsp_ide_twig::hover::TwigIncludeParameterHoverProvider;
use swlsp_lsp::TextDocument;
use swlsp_twig::TwigIndexer;

#[test]
fn twig_include_parameter_hover_describes_target_input() {
    let root = tempfile::tempdir().unwrap();
    let index = Arc::new(TwigIndexer::new(root.path().join(".cache"), None).unwrap());
    index_file(
        index.as_ref(),
        &p(&root.path().join("templates/card.html.twig")),
        "{{ title }}",
    );
    let provider = TwigIncludeParameterHoverProvider::new(p(root.path()), Some(index), None);
    let source = "{% include 'card.html.twig' with {'title': value} %}";
    let document = TextDocument::new("file:///project/templates/page.html.twig", source, 1);
    let request = request_at(document, source.find("title").unwrap() + 1);
    let result = hover(&provider, &request).expect("hover");
    let value = &result.contents.value;
    assert!(value.contains("`title`"));
    assert!(value.contains("`card.html.twig`"));
    assert!(value.contains("templates/card.html.twig"));
    let range = result.range.expect("range");
    assert_eq!(range.start.character, 35);
    assert_eq!(range.end.character, 40);
}
