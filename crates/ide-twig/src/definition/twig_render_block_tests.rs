use swlsp_lsp::{Context, GotoDefinitionProvider, TextDocument};
use swlsp_uriutil::file_uri;

use super::*;
use crate::definition::twig_common::test_support::*;

#[test]
fn twig_render_block_definition_navigates_overrides_and_parents() {
    let root = tempfile::tempdir().unwrap();
    let php_cache = tempfile::tempdir().unwrap();
    let twig_cache = tempfile::tempdir().unwrap();
    let root_path = p(root.path());
    let php = php_index(php_cache.path());
    let framework_path = format!("{root_path}/vendor/AbstractController.php");
    let framework = "<?php
namespace Symfony\\Bundle\\FrameworkBundle\\Controller;
abstract class AbstractController {}";
    write(&framework_path, framework);
    index_php(&php, &framework_path, framework);
    let twig = twig_index(twig_cache.path());
    for (relative, source) in [
        ("base.html.twig", "{% block content %}{% endblock %}"),
        (
            "child.html.twig",
            "{% extends 'base.html.twig' %}\n{% block content %}{% endblock %}",
        ),
    ] {
        let path = format!("{root_path}/templates/{relative}");
        write(&path, source);
        index(&*twig, &path, source);
    }

    let source = "<?php
namespace App;
class Controller extends \\Symfony\\Bundle\\FrameworkBundle\\Controller\\AbstractController {
    public function page(): void {
        $this->renderBlockView(
            block: 'content',
            view: 'child.html.twig',
        );
    }
}";
    let path = format!("{root_path}/src/Controller.php");
    write(&path, source);
    index_php(&php, &path, source);
    let offset = offset_of(source, "'content'") + 2;
    let locations = TwigRenderBlockDefinitionProvider::new(Some(twig), Some(php)).get_definition(
        &Context::background(),
        &request_at(TextDocument::new(file_uri(&path), source, 1), offset),
    );
    assert_eq!(locations.len(), 2);
    let mut uris: Vec<_> = locations
        .iter()
        .map(|location| location.uri.clone())
        .collect();
    uris.sort();
    let mut expected = vec![
        file_uri(&format!("{root_path}/templates/base.html.twig")),
        file_uri(&format!("{root_path}/templates/child.html.twig")),
    ];
    expected.sort();
    assert_eq!(uris, expected);
}
