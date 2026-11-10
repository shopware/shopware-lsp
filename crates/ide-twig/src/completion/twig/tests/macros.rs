use super::*;
use crate::completion::twig::TwigMacroCompletionProvider;

#[test]
fn twig_macro_completion_for_namespace_and_direct_imports() {
    let dir = tmp();
    let twig = twig_index(&dir);
    index(
        twig.as_ref(),
        "/project/templates/macros/forms.html.twig",
        "{## Renders a form input. ##}\n{% macro input(name, value = '') %}{% endmacro %}",
    );
    let provider = TwigMacroCompletionProvider::new(Some(twig));
    for (source, needle, label) in [
        (
            "{% import 'macros/forms.html.twig' as forms %}\n{{ forms.input() }}\n",
            "forms.input",
            "input",
        ),
        (
            "{% from 'macros/forms.html.twig' import input as field %}\n{{ field() }}\n",
            "field()",
            "field",
        ),
    ] {
        let document = Arc::new(TextDocument::new(
            "file:///project/templates/page.html.twig",
            source,
            1,
        ));
        let offset = (source.rfind(needle).unwrap() + needle.len() - 1) as u32;
        let items = complete(&provider, &request_with_node(document, offset, offset));
        let found = require_completion(&items, label);
        assert!(found.detail.contains("input(name, value = '')"));
        assert_eq!(documentation(&found), "Renders a form input.");
    }
}
