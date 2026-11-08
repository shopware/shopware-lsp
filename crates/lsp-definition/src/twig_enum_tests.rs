use swlsp_lsp::{Context, GotoDefinitionProvider, TextDocument};
use swlsp_uriutil::file_uri;

use super::*;
use crate::twig_common::test_support::*;

#[test]
fn twig_enum_definition_navigates_to_php_enum() {
    let root = tempfile::tempdir().unwrap();
    let cache = tempfile::tempdir().unwrap();
    let root_path = p(root.path());
    let enum_path = format!("{root_path}/src/Status.php");
    let php = php_index(cache.path());
    index_php(
        &php,
        &enum_path,
        "<?php namespace App; enum Status { case Open; }",
    );
    let source = "{{ enum_cases('App\\\\Status') }}";
    let offset = offset_of(source, "Status") + 2;
    let mut request = request_at(
        TextDocument::new(
            file_uri(&format!("{root_path}/templates/status.html.twig")),
            source,
            1,
        ),
        offset,
    );
    request.syntax.language = None;
    let locations =
        TwigEnumDefinitionProvider::new(Some(php)).get_definition(&Context::background(), &request);
    assert_eq!(locations.len(), 1);
    assert_eq!(locations[0].uri, file_uri(&enum_path));
}
