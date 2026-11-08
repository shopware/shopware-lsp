mod common;

use std::sync::Arc;

use common::*;
use swlsp_lsp::TextDocument;
use swlsp_lsp_hover::TwigMacroHoverProvider;
use swlsp_twig::TwigIndexer;

#[test]
fn twig_macro_hover_shows_signature_and_usage_count() {
    let cache = tempfile::tempdir().unwrap();
    let index = Arc::new(TwigIndexer::new(cache.path(), None).unwrap());
    index_file(
        index.as_ref(),
        "/project/templates/macros/forms.html.twig",
        "{## Renders a form input. ##}
{% macro input(name, value = '') %}{% endmacro %}",
    );
    let source = "{% import 'macros/forms.html.twig' as forms %}
{{ forms.input('email') }}
";
    index_file(index.as_ref(), "/project/templates/page.html.twig", source);
    let document = TextDocument::new("file:///project/templates/page.html.twig", source, 1);
    let request = request_at(
        document,
        source.find("forms.input").unwrap() + "forms.".len() + 2,
    );
    let result = hover(
        &TwigMacroHoverProvider::new("/project", Some(index)),
        &request,
    )
    .expect("hover");
    let value = &result.contents.value;
    assert!(value.contains("input(name, value = '')"));
    assert!(value.contains("Renders a form input."));
    assert!(value.contains("macros/forms.html.twig"));
    assert!(value.contains("1 indexed use"));
}
