use swlsp_lsp::{Context, GotoDefinitionProvider, TextDocument};
use swlsp_uriutil::file_uri;

use super::*;
use crate::definition::twig_common::test_support::*;

#[test]
fn twig_macro_definition_navigates_to_declaration() {
    let root = tempfile::tempdir().unwrap();
    let cache = tempfile::tempdir().unwrap();
    let root_path = p(root.path());
    let macro_path = format!("{root_path}/templates/macros/forms.html.twig");
    let page_path = format!("{root_path}/templates/page.html.twig");
    let macro_source = "{% macro input(name) %}{% endmacro %}";
    let page_source =
        "{% import 'macros/forms.html.twig' as forms %}\n{{ forms.input('email') }}\n";
    write(&macro_path, macro_source);
    write(&page_path, page_source);
    let twig = twig_index(cache.path());
    index(&*twig, &macro_path, macro_source);
    index(&*twig, &page_path, page_source);

    let offset = offset_of(page_source, "forms.input") + "forms.".len() as u32 + 2;
    let locations = TwigMacroDefinitionProvider::new(Some(twig)).get_definition(
        &Context::background(),
        &request_at(
            TextDocument::new(file_uri(&page_path), page_source, 1),
            offset,
        ),
    );
    assert_eq!(locations.len(), 1);
    assert_eq!(locations[0].uri, file_uri(&macro_path));
    assert_eq!(locations[0].range.start.line, 0);
    assert_eq!(
        locations[0].range.start.character,
        offset_of(macro_source, "input")
    );
}
