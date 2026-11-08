use swlsp_lsp::{Context, GotoDefinitionProvider, TextDocument};
use swlsp_lsp_protocol as protocol;
use swlsp_uriutil::file_uri;

use super::*;
use crate::twig_common::test_support::*;

fn parameter_definitions(
    provider: &TwigIncludeParameterDefinitionProvider,
    source: &str,
    needle: &str,
) -> Vec<protocol::Location> {
    let offset = offset_of(source, needle) + 1;
    provider.get_definition(
        &Context::background(),
        &request_at(
            TextDocument::new("file:///project/templates/page.html.twig", source, 1),
            offset,
        ),
    )
}

#[test]
fn twig_include_parameter_definition_navigates_to_target_and_parent() {
    let root = tempfile::tempdir().unwrap();
    let root_path = p(root.path());
    let twig = twig_index(&root.path().join(".cache"));
    let base_path = format!("{root_path}/templates/base.html.twig");
    let card_path = format!("{root_path}/templates/card.html.twig");
    for (path, source) in [
        (&base_path, "{{ inherited }}"),
        (&card_path, "{% extends 'base.html.twig' %}\n{{ title }}"),
    ] {
        write(path, source);
        index(&*twig, path, source);
    }
    let provider = TwigIncludeParameterDefinitionProvider::new(Some(twig), None);

    let title = parameter_definitions(
        &provider,
        "{% include 'card.html.twig' with {'title': value} %}",
        "title",
    );
    assert_eq!(title.len(), 1);
    assert_eq!(title[0].uri, file_uri(&card_path));
    assert_eq!(title[0].range.start.line, 1);
    assert_eq!(title[0].range.start.character, 3);

    let inherited = parameter_definitions(
        &provider,
        "{{ include('card.html.twig', {inherited: value}) }}",
        "inherited",
    );
    assert_eq!(inherited.len(), 1);
    assert_eq!(inherited[0].uri, file_uri(&base_path));
}
